package kvraft

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"../labgob"
	"../labrpc"
	"../raft-LabD"
)

const Debug = 0

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug > 0 {
		log.Printf(format, a...)
	}
	return
}

type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	SeqId    int
	Key      string
	Value    string
	ClientId int64
	Index    int // Raft服务层传来的Index
	OpType   string
}

type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate int // snapshot if log grows this big，触发快照的阈值

	// Your definitions here.
	seqMap    map[int64]int     // 为了确保seq只执行一次
	waitChMap map[int]chan Op   // 传递由下层Raft服务的appC传过来的command
	kvPersist map[string]string // 存储持久化的KV键值对

	lastIncludeIndex int // 最后包含在快照中的索引
}

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	DPrintf("Server %d: Get called for key %s", kv.me, args.Key)

	// Your code here.
	if kv.killed() {
		reply.Err = ErrWrongLeader
		return
	}
	_, isLeader := kv.rf.GetState()
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}

	// 封装Op传到下层start
	op := Op{
		OpType:   "Get",
		Key:      args.Key,
		SeqId:    args.SeqId,
		ClientId: args.ClientId,
	}
	lastIndex, _, _ := kv.rf.Start(op)
	ch := kv.getWaitCh(lastIndex) // 得到Raft层回来的响应

	defer func() {
		kv.mu.Lock()
		delete(kv.waitChMap, lastIndex)
		kv.mu.Unlock()
	}()

	// 设置超时ticker
	timer := time.NewTicker(300 * time.Millisecond)
	defer timer.Stop()

	select {
	case replyOp := <-ch:
		if op.ClientId != replyOp.ClientId || op.SeqId != replyOp.SeqId {
			reply.Err = ErrWrongLeader
		} else {
			reply.Err = OK
			kv.mu.Lock()
			reply.Value = kv.kvPersist[args.Key]
			kv.mu.Unlock()
			return
		}
	case <-timer.C:
		// 如果超时了则让客户端重试
		reply.Err = ErrWrongLeader
	}
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	if kv.killed() {
		reply.Err = ErrWrongLeader
		return
	}
	_, isLeader := kv.rf.GetState()
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}

	// 封装Op传到下层start
	op := Op{
		OpType:   args.Op,
		Key:      args.Key,
		Value:    args.Value,
		SeqId:    args.SeqId,
		ClientId: args.ClientId,
	}
	lastIndex, _, _ := kv.rf.Start(op)
	ch := kv.getWaitCh(lastIndex)
	DPrintf("Server %d: waiting on channel for index %d", kv.me, lastIndex)

	defer func() {
		kv.mu.Lock()
		delete(kv.waitChMap, lastIndex)
		kv.mu.Unlock()
	}()

	// 设置超时ticker
	timer := time.NewTicker(300 * time.Millisecond)
	defer timer.Stop()

	select {
	case replyOp := <-ch:
		DPrintf("Server %d: received reply op", kv.me)
		if op.ClientId != replyOp.ClientId || op.SeqId != replyOp.SeqId {
			reply.Err = ErrWrongLeader
		} else {
			reply.Err = OK
		}
	case <-timer.C:
		// 如果超时了则让客户端重试
		DPrintf("Server %d: timeout", kv.me)
		reply.Err = ErrWrongLeader
	}
}

// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	// You may need initialization code here.

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	// You may need initialization code here.
	kv.seqMap = make(map[int64]int)
	kv.kvPersist = make(map[string]string)
	kv.waitChMap = make(map[int]chan Op)

	kv.lastIncludeIndex = -1

	// 此处加上以下内容是考虑到可能会存在crash后重连的情况
	snapshot := persister.ReadSnapshot()
	if len(snapshot) > 0 {
		kv.DecodeSnapShot(snapshot)
	}

	go kv.applyMsgHandlerLoop()

	return kv
}

// 用于转接消息的loop，转接来自于raft.ApplyMsg的消息为op，再返回到waitCh中
func (kv *KVServer) applyMsgHandlerLoop() {
	for {
		if kv.killed() {
			return
		}
		select {
		case msg := <-kv.applyCh:
			// Lab3A
			// index := msg.CommandIndex
			// op := msg.Command.(Op) // 使用断言的方法将interface{}类型转换回具体的Op类型
			// if !kv.ifDuplicate(op.ClientId, op.SeqId) {
			// 	kv.mu.Lock()
			// 	switch op.OpType {
			// 	// 对写操作进行服务器状态的修改
			// 	case "Put":
			// 		kv.kvPersist[op.Key] = op.Value
			// 	case "Append":
			// 		kv.kvPersist[op.Key] += op.Value
			// 	}
			// 	kv.seqMap[op.ClientId] = op.SeqId
			// 	kv.mu.Unlock()
			// }
			// // 将所有的op操作（get\put\append）返回给waitCh
			// kv.getWaitCh(index) <- op

			// Lab3B
			// 传来的消息快照已经存储了
			// 处理普通命令
			if msg.CommandValid {
				// 接收到的索引小于等于最后包含在快照中的索引，说明该命令包含在快照之内，应该跳过处理
				if msg.CommandIndex <= kv.lastIncludeIndex {
					return
				}
				index := msg.CommandIndex
				op := msg.Command.(Op)
				if !kv.ifDuplicate(op.ClientId, op.SeqId) {
					kv.mu.Lock()
					switch op.OpType {
					case "Put":
						kv.kvPersist[op.Key] = op.Value
					case "Append":
						kv.kvPersist[op.Key] += op.Value
					}
					kv.seqMap[op.ClientId] = op.SeqId // 更新客户端的序列号用于去重
					kv.mu.Unlock()
				}
				// 如果需要快照并且超出了stateSize
				if kv.maxraftstate != -1 && kv.rf.GetRaftStateSize() > kv.maxraftstate {
					snapshot := kv.PersistSnapShot()           // 创建快照
					kv.rf.Snapshot(msg.CommandIndex, snapshot) // 通知Raft层
				}
				// 将返回的op操作返回给waitCh
				kv.getWaitCh(index) <- op
			}
			// 处理快照安装
			if msg.SnapshotValid {
				kv.mu.Lock()
				// 判断此时是否有竞争
				if kv.rf.CondInstallSnapshot(msg.SnapshotTerm, msg.SnapshotIndex, msg.Snapshot) {
					// 读取快照的数据
					kv.DecodeSnapShot(msg.Snapshot)
					kv.lastIncludeIndex = msg.SnapshotIndex
				}
				kv.mu.Unlock()
			}
		}
	}
}

// 用于判断是否存在重复
func (kv *KVServer) ifDuplicate(clientId int64, seqId int) bool {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	lastSeqId, exist := kv.seqMap[clientId]
	if !exist {
		return false
	}
	return seqId <= lastSeqId
}

func (kv *KVServer) getWaitCh(index int) chan Op {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	ch, exist := kv.waitChMap[index]
	if !exist {
		kv.waitChMap[index] = make(chan Op, 1) // 创建缓冲通道避免阻塞
		ch = kv.waitChMap[index]
	}
	return ch
}

// 恢复快照
func (kv *KVServer) DecodeSnapShot(snapshot []byte) {
	if snapshot == nil || len(snapshot) < 1 {
		return
	}
	r := bytes.NewBuffer(snapshot)
	d := labgob.NewDecoder(r)

	var kvPersist map[string]string
	var seqMap map[int64]int

	if d.Decode(&kvPersist) == nil && d.Decode(&seqMap) == nil {
		// 解码成功，将获取的值赋予对应的字段
		kv.kvPersist = kvPersist
		kv.seqMap = seqMap
	} else {
		fmt.Printf("[Server(%v)] Failed to decode snapshot...", kv.me)
	}
}

// 创建快照
func (kv *KVServer) PersistSnapShot() []byte {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	// 序列化状态机数据
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(kv.kvPersist)
	e.Encode(kv.seqMap)

	data := w.Bytes()
	return data

}
