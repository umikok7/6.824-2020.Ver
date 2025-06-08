package shardkv

// import "../shardmaster"
import (
	"bytes"
	"sync"
	"sync/atomic"
	"time"

	"../shardmaster"

	"../labgob"
	"../labrpc"
	"../raft"
)

const (
	RespondTimeout      = 500 // shard回复client的超时时间 / ms
	ConfigCheckInterval = 50  // config变更检查间隔 / ms
)

const (
	Get          = "Get"
	Put          = "Put"
	Append       = "Append"
	UpdateConfig = "UpdateConfig" // 更新config版本
	GetShard     = "GetShard"     // group已经获得了shard数据，需要将其应用在自己的kvDB并更新shard状态 WaitGet->Exist
	GiveShard    = "GiveShard"    // group已经将shard给出去了，需要更新shard状态 WaitGet->NoExist
	EmptyOp      = "EmptyOp"      // 空日志
)

// shard在某group中的状态
type ShardState int

// shard状态类型
const (
	NoExist  ShardState = iota // 该shard已经不归该group管，稳定态
	Exist                      // 该shard归该group管且完成迁移，稳定态
	WaitGet                    // 该shard归该group管但需要等待迁移完成，中间态
	WaitGive                   // 该shard不归该group管但需要等待将shard迁移走，中间态
)

type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	ClientId    int64
	CmdNum      int
	OpType      string
	Key         string
	Value       string
	NewConfig   shardmaster.Config // 新版本的config，仅UpdateConfig指令有效
	CfgNum      int                // 本次shard迁移的config编号，（仅GetShard和GiveShard指令有效）
	ShardNum    int                // 从别的group获取到的shard编号，仅GetShard和GiveShard指令有效
	ShardData   map[string]string  // shard数据，仅GetShard有效
	SessionData map[int64]Session  // shard前任所有者的Session数据，仅GetShard有效
}

// session跟踪该group为client处理的上一次请求的序列号以及相关响应
type Session struct {
	LastCmdNum int    // 该server为该client处理的上一条指令的序列号
	OpType     string // 最新处理的指令的类型
	Response   Reply  // 对应的响应
}

type Reply struct {
	Err   Err
	Value string // Get时有效
}

type ShardKV struct {
	mu           sync.Mutex
	me           int
	rf           *raft.Raft
	applyCh      chan raft.ApplyMsg
	make_end     func(string) *labrpc.ClientEnd // 将server名称转换为CliendEnd
	gid          int                            // 该ShardKV所属的group的gid
	masters      []*labrpc.ClientEnd            // ShardMaster集群的servers接口
	maxraftstate int                            // snapshot if log grows this big

	// Your definitions here.
	dead                  int32
	mck                   *shardmaster.Clerk              // 关联shardmaster,用于定期联系shardmaster已判断是否配置有变更
	kvDB                  map[int]map[string]string       // 该group的服务器保存的键值对，ShardNum -> {key -> value}
	sessions              map[int64]Session               // 此server的状态机为各个client维护的会话
	notifyMapCh           map[int]chan Reply              // kvserver apply到了等待回复的日志，则通过chan通知对应handler去回复client，key为日志的index
	logLastApplied        int                             // 此kvserver apply的上一个日志的index
	passiveSnapshotBefore bool                            // 标志着applyMsg上一个从channel中取出的是被动快照并已被安装完
	ownedShards           [shardmaster.NShards]ShardState // 该ShardKV(group)负责的shard状态
	preConfig             shardmaster.Config
	curConfig             shardmaster.Config
}

// 检查某个key是否归该group负责，且当前可以提供服务，返回结果
// 使用之前加锁
func (kv *ShardKV) checkKeyInGroup(key string) bool {
	shard := key2shard(key) // 获取key对应的shard序号
	if kv.ownedShards[shard] != Exist {
		return false
	}
	return true
}

// 为leader在kv.notifyMapCh中初始化一个缓冲为1的channel完成与本次applyMsg()的通信
func (kv *ShardKV) createNotifyCh(index int) chan Reply {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	notifyCh := make(chan Reply, 1)
	kv.notifyMapCh[index] = notifyCh
	return notifyCh
}

// ShardKV回复client后关闭对应index的notifyCh
func (kv *ShardKV) closeNotifyCh(index int) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	if _, ok := kv.notifyMapCh[index]; ok {
		close(kv.notifyMapCh[index])
		delete(kv.notifyMapCh, index)
	}
}

func (kv *ShardKV) Get(args *GetArgs, reply *GetReply) {
	DPrintf("DEBUG: Group[%d]ShardKV[%d] Get request: key=%s, clientId=%d, cmdNum=%d",
		kv.gid, kv.me, args.Key, args.ClientId, args.CmdNum)
	// Your code here.
	// 此处实现的是group内非leader的server也能回复ErrWrongGroup
	// 先检查该server所属的group是否仍负责请求的key（可能client手里的配置信息未更新）
	kv.mu.Lock()
	if in := kv.checkKeyInGroup(args.Key); !in {
		reply.Err = ErrWrongGroup
		kv.mu.Unlock()
		return
	}
	kv.mu.Unlock() // 此处一定要记得先解锁！
	// 如果该group确实负责这个key则继续
	getOp := Op{
		ClientId: args.ClientId,
		CmdNum:   args.CmdNum,
		OpType:   Get,
		Key:      args.Key,
	}

	index, _, isLeader := kv.rf.Start(getOp) // 传client的请求操作给raft层进行共识
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
	notifyCh := kv.createNotifyCh(index)

	// 等待请求执行完的回复并开始超时机时
	select {
	case res := <-notifyCh:
		// 返回前再次检查该key是否仍由该group负责，防止共识和应用期间config发生变化
		kv.mu.Lock()
		if in := kv.checkKeyInGroup(args.Key); !in {
			reply.Err = ErrWrongGroup
			kv.mu.Unlock()
			return
		}
		kv.mu.Unlock()
		reply.Err = res.Err
		reply.Value = res.Value // 返回get请求需要的value
	case <-time.After(RespondTimeout * time.Millisecond):
		reply.Err = ErrTimeout
	}
	go kv.closeNotifyCh(index) // 关闭并删除此临时channel，下个请求到来时重新初始化
}

func (kv *ShardKV) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	DPrintf("DEBUG: Group[%d]ShardKV[%d] %s request: key=%s, value=%s, clientId=%d, cmdNum=%d",
		kv.gid, kv.me, args.Op, args.Key, args.Value, args.ClientId, args.CmdNum)
	// Your code here.
	kv.mu.Lock()
	if in := kv.checkKeyInGroup(args.Key); !in {
		reply.Err = ErrWrongGroup
		kv.mu.Unlock()
		return
	}
	// ShardKV过滤重复请求
	if session, exist := kv.sessions[args.ClientId]; exist {
		if args.CmdNum < session.LastCmdNum {
			// 标志这个请求client已经收到正确的回复了，只是这个重复请求到达的太慢了
			kv.mu.Unlock()
			return
		} else if args.CmdNum == session.LastCmdNum {
			// 将session中记录的之前执行该请求的结果直接返回，使得不会多次执行一个请求
			reply.Err = session.Response.Err
			kv.mu.Unlock()
			return
		}
	}
	// 未处理的新请求
	kv.mu.Unlock() // 此处一定要记得先解锁！
	// 如果该group确实负责这个key则继续
	paOp := Op{
		ClientId: args.ClientId,
		CmdNum:   args.CmdNum,
		OpType:   args.Op,
		Key:      args.Key,
		Value:    args.Value,
	}

	index, _, isLeader := kv.rf.Start(paOp) // 传client的请求操作给raft层进行共识
	if !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
	notifyCh := kv.createNotifyCh(index)

	// 等待请求执行完的回复并开始超时机时
	select {
	case res := <-notifyCh:
		// 返回前再次检查该key是否仍由该group负责，防止共识和应用期间config发生变化
		kv.mu.Lock()
		if in := kv.checkKeyInGroup(args.Key); !in {
			reply.Err = ErrWrongGroup
			kv.mu.Unlock()
			return
		}
		kv.mu.Unlock()
		reply.Err = res.Err
	case <-time.After(RespondTimeout * time.Millisecond):
		reply.Err = ErrTimeout
	}
	go kv.closeNotifyCh(index) // 关闭并删除此临时channel，下个请求到来时重新初始化

}

// the tester calls Kill() when a ShardKV instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
func (kv *ShardKV) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *ShardKV) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

func (kv *ShardKV) applyMessage() {
	for !kv.killed() {
		applyMsg := <-kv.applyCh
		if applyMsg.CommandValid {
			// 取出的是command则执行
			op, ok := applyMsg.Command.(Op)
			if !ok {
				DPrintf("convert fail!\n")
			} else {
				// client的指令和组内config变更相关指令分开处理
				if op.OpType == Get || op.OpType == Put || op.OpType == Append {
					kv.executeClientCmd(op, applyMsg.CommandIndex, applyMsg.CommandTerm)
				} else if op.OpType == UpdateConfig || op.OpType == GetShard || op.OpType == GiveShard {
					kv.executeConfigCmd(op, applyMsg.CommandIndex)
				} else if op.OpType == EmptyOp {
					kv.executeEmptyCmd(applyMsg.CommandIndex)
				} else {
					DPrintf("Unexpected Optype!\n")
				}
			}
		} else if applyMsg.SnapshotValid {
			// 取出的是快照
			// 在raft层已经实现follower是否安装快照的判断
			// 只有follower接受了快照才会通过applyCh通知状态机，因此这里状态机只需要安装快照即可
			kv.mu.Lock()
			kv.applySnapshotToSM(applyMsg.StateMachineState) //将快照应用到状态机
			kv.logLastApplied = applyMsg.SnapshotIndex       // 更新logLastApplied避免回滚
			kv.passiveSnapshotBefore = true                  // 刚安装完被动快照，需要提醒下一个从channel中取出的若是指令则注意是否为“跨快照指令”
			kv.mu.Unlock()
			kv.rf.SetPassiveSnapshottingFlag(false)
		} else {
			DPrintf("Group[%d]ShardKV[%d] get an unexpected ApplyMsg!\n", kv.gid, kv.me)
		}
	}
}

// 执行client请求的指令（Get\Put\Append)
func (kv *ShardKV) executeClientCmd(op Op, commandIndex int, commandTerm int) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	// 如果该group已经不负责这个key的shard，那么停止对该key请求的服务，并让其等待超时，向其他server重新请求
	if in := kv.checkKeyInGroup(op.Key); !in {
		return
	}
	if commandIndex <= kv.logLastApplied {
		// 该检查是为了避免将落后的日志被应用到状态机当中
		// 因为在网络中可能因为各种原因导致leader重新发送了已经应用过的日志，这个检查可以避免重复执行
		return
	}

	// 如果上一个取出的是被动快照并且已经安装完，则要注意排除“跨快照指令”
	// 因为被动快照安装完后，后续的指令应该从快照结束的index之后紧接着逐个apply
	if kv.passiveSnapshotBefore {
		if commandIndex-kv.logLastApplied != 1 {
			return // 快照恢复后的第一条指令并不紧接着快照最后一个索引，直接退出
		}
		kv.passiveSnapshotBefore = false
	}
	// 否则就将logLastApplied 更新为较大值applyMsg.CommandIndex
	// 这是执行进度的记录，标记当前已经成功应用到状态机的最高日志索引。
	kv.logLastApplied = commandIndex

	reply := Reply{}
	sessionRec, exist := kv.sessions[op.ClientId]
	if exist && op.OpType != Get && op.CmdNum <= sessionRec.LastCmdNum {
		// 如果apply的指令之前已经apply过并且不是Get指令，则不重复执行，直接返回原先保存的结果
		reply = kv.sessions[op.ClientId].Response
	} else {
		// 没有执行过的指令则实际执行并记录session
		shardNum := key2shard(op.Key)
		// 没有执行过的指令在状态机上执行，重复的Get指令可以重新执行
		switch op.OpType {
		case Get:
			v, existKey := kv.kvDB[shardNum][op.Key]
			if !existKey {
				reply.Err = ErrNoKey
				reply.Value = "" // key不存在则返回对应空字符串
			} else {
				reply.Err = OK
				reply.Value = v
			}
		case Put:
			kv.kvDB[shardNum][op.Key] = op.Value
			reply.Err = OK
		case Append:
			oldValue, existKey := kv.kvDB[shardNum][op.Key]
			if !existKey {
				// 若不存在则取出的为该类型的零值
				reply.Err = ErrNoKey
				kv.kvDB[shardNum][op.Key] = op.Value
			} else {
				reply.Err = OK
				kv.kvDB[shardNum][op.Key] = oldValue + op.Value // 追加到原value的后面
			}
		default:
			DPrintf("Not Client Cmd OpType!\n")
		}

		// 将最近执行过的Put和Append的指令执行结果存放到session中以便后续重复请求直接返回而不重复执行
		if op.OpType != Get {
			session := Session{
				LastCmdNum: op.CmdNum,
				OpType:     op.OpType,
				Response:   reply,
			}
			kv.sessions[op.ClientId] = session
		}
	}

	// 如果任期和leader身份没变，则向对应的client的notifyMapCh发送reply通知对应handle回复client
	if _, existCh := kv.notifyMapCh[commandIndex]; existCh {
		if currentTerm, isLeader := kv.rf.GetState(); isLeader && commandTerm == currentTerm {
			kv.notifyMapCh[commandIndex] <- reply
		}
	}
}

// 执行组内的config变更相关指令（UpdateConfig\GetShard\GiveShard）
// 这些指令用于保证一个group内所有servers对config变更过程的一致认知
func (kv *ShardKV) executeConfigCmd(op Op, commandIndex int) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	if commandIndex <= kv.logLastApplied {
		return
	}

	// 同样的，如果上一个取出的是被动快照并且已安装完成，则要注意排除“跨快照指令”
	if kv.passiveSnapshotBefore {
		if commandIndex-kv.logLastApplied != 1 {
			return
		}
		kv.passiveSnapshotBefore = false
	}
	// 否则就将logLastApplied 更新为较大值applyMsg.CommandIndex
	kv.logLastApplied = commandIndex

	// 实际执行迁移shard相关的指令
	switch op.OpType {
	case UpdateConfig:
		// follower收到后更新的下一个config版本，则更新shard状态（暂时不进行迁移）
		if kv.curConfig.Num+1 == op.CfgNum {
			DPrintf("DEBUG: Group[%d]ShardKV[%d] updating config from %d to %d",
				kv.gid, kv.me, kv.curConfig.Num, op.NewConfig.Num)
			kv.updateShardState(op.NewConfig)
		} else {
			DPrintf("DEBUG: Group[%d]ShardKV[%d] config update rejected: current=%d, new=%d",
				kv.gid, kv.me, kv.curConfig.Num, op.NewConfig.Num)
		}
	case GetShard:
		// 将从前任其他组请求到的shard数据更新到自己的kvDB
		// 重复的GetShard指令由第二个条件过滤
		if kv.curConfig.Num == op.CfgNum && kv.ownedShards[op.ShardNum] == WaitGet {
			kvMap := deepCopyMap(op.ShardData)
			// 修复：确保不覆盖已有数据
			if kv.kvDB[op.ShardNum] == nil {
				kv.kvDB[op.ShardNum] = make(map[string]string)
			}
			kv.kvDB[op.ShardNum] = kvMap        // 将迁移过来的shard数据副本保存在自己的键值服务器中
			kv.ownedShards[op.ShardNum] = Exist // 更新状态为WaitGet->Exist

			// 将shard前任所有者的session中较新的记录应用在自己的session，用于防止shard迁移后重复执行某指令带来结果错误
			for clientId, session := range op.SessionData {
				if ownSession, exist := kv.sessions[clientId]; !exist || session.LastCmdNum > ownSession.LastCmdNum {
					kv.sessions[clientId] = session
				}
			}

			DPrintf("DEBUG: Group[%d]ShardKV[%d] applied GetShard: shard=%d, keys=%d",
				kv.gid, kv.me, op.ShardNum, len(kvMap))
		} else {
			DPrintf("DEBUG: Group[%d]ShardKV[%d] GetShard rejected: configNum=%d/%d, state=%d",
				kv.gid, kv.me, op.CfgNum, kv.curConfig.Num, kv.ownedShards[op.ShardNum])
		}
	case GiveShard:
		// 标志shard已经被迁移到新的所有者，本server真正失去该shard
		if kv.curConfig.Num == op.CfgNum && kv.ownedShards[op.ShardNum] == WaitGive {
			kv.ownedShards[op.ShardNum] = NoExist
			kv.kvDB[op.ShardNum] = map[string]string{} // 清除不再拥有的shard数据
		}
	default:
		DPrintf("Not Config Change CMD Optype!\n")
	}
}

// 空指令虽然没有实际效果，但是还是需要更新相关变量
func (kv *ShardKV) executeEmptyCmd(commandIndex int) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	if commandIndex <= kv.logLastApplied {
		return
	}
	// 同样的避免跨快照安装
	if kv.passiveSnapshotBefore {
		if commandIndex-kv.logLastApplied != 1 {
			return
		}
		kv.passiveSnapshotBefore = false
	}
	// 否则就将更新为较大值
	kv.logLastApplied = commandIndex
}

func (kv *ShardKV) updateShardState(nextConfig shardmaster.Config) {
	// 检查组内shard的变更情况
	for shard, gid := range nextConfig.Shards {
		// 如果曾经有该shard，单新配置无了，则将该shard状态设为等待迁移走
		if kv.ownedShards[shard] == Exist && gid != kv.gid {
			kv.ownedShards[shard] = WaitGive
		}
		// 如果之前没有该shard，新配置又有了，则将该shard状态设为等待从前任所有者迁移shard过来
		if kv.ownedShards[shard] == NoExist && gid == kv.gid {
			if nextConfig.Num == 1 {
				// 代表刚初始化集群，不需要去其他group获取shard，直接设为Exist
				kv.ownedShards[shard] = Exist
				// 添加部分
				// 确保kvDB中此shard已初始化
				if kv.kvDB[shard] == nil {
					kv.kvDB[shard] = make(map[string]string)
				}
			} else {
				kv.ownedShards[shard] = WaitGet
			}
		}
		// 之前有，现在也有；或者之前没有，现在也没有，那么就不需要迁移shard
	}

	// 初始化时preConfig和curConfig的Num都为0，此时只用更新curConfig.Num为1
	if kv.preConfig.Num != 0 || kv.curConfig.Num != 0 {
		kv.preConfig = kv.curConfig
	}
	kv.curConfig = nextConfig
	DPrintf("DEBUG: Group[%d]ShardKV[%d] config updated: ownedShards=%v",
		kv.gid, kv.me, kv.ownedShards)

}

// 将快照应用到state machine
func (kv *ShardKV) applySnapshotToSM(data []byte) {
	if data == nil || len(data) == 0 { // 如果传进来的快照为空或无效则不应用
		return
	}

	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var kvDB map[int]map[string]string
	var sessions map[int64]Session
	var preConfig shardmaster.Config
	var curConfig shardmaster.Config
	var ownedShards [shardmaster.NShards]ShardState

	if d.Decode(&kvDB) != nil || d.Decode(&sessions) != nil || d.Decode(&preConfig) != nil ||
		d.Decode(&curConfig) != nil || d.Decode(&ownedShards) != nil {
		DPrintf("Group[%d]ShardKV[%d] applySnapshotToSM ERROR!\n", kv.gid, kv.me)
	} else {
		kv.kvDB = kvDB
		kv.sessions = sessions
		kv.preConfig = preConfig
		kv.curConfig = curConfig
		kv.ownedShards = ownedShards
	}

	// // 暂时禁用快照恢复
	// DPrintf("DEBUG: Group[%d]ShardKV[%d] snapshot recovery disabled for debugging", kv.gid, kv.me)
	// return
}

// kvserver定期检查是否需要快照（主动进行快照）
func (kv *ShardKV) checkSnapshotNeed() {
	for !kv.killed() {
		var snapshotData []byte
		var snapshotIndex int

		// 如果该server正在进行被动快照
		// 这期间该server不检查是否需要主动快照，避免主、被动快照重叠应用导致上层kvserver状态与下层raft日志不一致
		if kv.rf.GetPassiveFlagAndSetActiveFlag() {
			time.Sleep(time.Millisecond * 50)
			continue
		}

		// 若该server没有开始被动快照，则可以进行检查是否需要主动快照
		// 主动快照标志在GetPassiveFlagAndSetActiveFlag方法中已经设置，目的是为了保证检查被动快照标志和设置主动快照标志的原子性
		nowStateSize := kv.rf.GetRaftStateSize()
		if kv.maxraftstate != -1 && nowStateSize > kv.maxraftstate {
			kv.mu.Lock()

			// 准备进行主动快照
			snapshotIndex = kv.logLastApplied
			// 将snapshot信息编码
			w := new(bytes.Buffer)
			e := labgob.NewEncoder(w)
			e.Encode(kv.kvDB)
			e.Encode(kv.sessions)
			e.Encode(kv.preConfig)
			e.Encode(kv.curConfig)
			e.Encode(kv.ownedShards)
			snapshotData = w.Bytes()
			kv.mu.Unlock()
		}
		if snapshotData != nil {
			kv.rf.Snapshot(snapshotIndex, snapshotData) // 进行快照
		}
		kv.rf.SetActiveSnapshottingFlag(false) // 无论检查完是否需要主动快照都要将主动快照标志修改回false
		time.Sleep(time.Millisecond * 50)

		// // 暂时禁用快照功能
		// DPrintf("DEBUG: Group[%d]ShardKV[%d] snapshot disabled for debugging", kv.gid, kv.me)
		// time.Sleep(time.Millisecond * 1000)
	}
}

// ShardKV定期询问shardmaster以获取更新的配置信息
// 只更新相关的状态标识位（ownedShards），而不实际迁移shard
func (kv *ShardKV) getLastestConfig() {
	for !kv.killed() {
		// 只有leader才进行检查
		if _, isLeader := kv.rf.GetState(); !isLeader {
			time.Sleep(time.Millisecond * ConfigCheckInterval)
			continue
		}

		// 为了保证“按照顺序一次处理一个重新配置”（配置变更不重叠、不遗漏）
		// 确保上一次config更新完成后才能开始下一次config更新,检查不通过就等待80ms后继续下一次检查
		if !kv.updateConfigReady() {
			time.Sleep(time.Millisecond * ConfigCheckInterval)
			continue
		}

		kv.mu.Lock()
		curConfig := kv.curConfig
		kv.mu.Unlock()

		nextConfig := kv.mck.Query(curConfig.Num + 1) // 联系shardmaster获取当前config的下一个config
		// 如果询问到的下一个config确实存在，证明有config需要变更
		if nextConfig.Num == curConfig.Num+1 {
			// 通知同一group内其他servers在同一位置（同一日志index）进入config变更过程
			// 因为非leader的其他节点并不会定期检查config更新的，因此通过raft共识告诉他们需要更新config了
			configUpdateOp := Op{
				OpType:    UpdateConfig,
				NewConfig: nextConfig,
				CfgNum:    nextConfig.Num,
			}
			kv.rf.Start(configUpdateOp)
		}
		time.Sleep(time.Millisecond * ConfigCheckInterval) // 每80ms向shardmaster询问一次最新配置
	}
}

// 检查是否准备好进行config变更，只有该shardkv没有迁移中间状态的shard时才能更新config
// 如果有shard处于迁移中间态说明上一次的config变更还没结束
func (kv *ShardKV) updateConfigReady() bool {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	for _, state := range kv.ownedShards {
		if state != NoExist && state != Exist {
			return false
		}
	}
	return true
}

// 向上一任持有该shard的servers的leader请求shard
// 定期检查该shardkv是否有shard等待从其他group迁移shard过来
func (kv *ShardKV) checkAndGetShard() {
	for !kv.killed() {
		// 只有leader才进行检查
		if _, isLeader := kv.rf.GetState(); !isLeader {
			time.Sleep(time.Millisecond * ConfigCheckInterval)
			continue
		}

		var waitGetShards []int // WaitGet状态的shard序号
		kv.mu.Lock()
		for shard, state := range kv.ownedShards {
			if state == WaitGet {
				waitGetShards = append(waitGetShards, shard)
			}
		}
		// 获取前任所有者的信息要根据上一个config
		preConfig := kv.preConfig
		curConfigNum := kv.curConfig.Num
		kv.mu.Unlock()

		// 使用WaitGroup来保证一轮将所有需要的shard都对应请求一遍再开始下次循环
		var wg sync.WaitGroup

		// 对本轮需要请求的所有shard都向对应的前任group请求
		for _, shard := range waitGetShards {
			wg.Add(1)                              // 每开始一个shard的请求就增加等待计数器
			preGid := preConfig.Shards[shard]      // 获取前任shard所有者的gid
			preServers := preConfig.Groups[preGid] // 根据gid找到该group成员的servers name

			// 向上一任shard所有者的组发送拉取shard请求（依次询问每一个成员，直到找到leader）
			go func(servers []string, configNum int, shardNum int) {
				defer wg.Done() // 在向leader成功请求到该shard或问遍该组成员后仍没有获得shard时减少等待计数器
				for si := 0; si < len(servers); si++ {
					srv := kv.make_end(servers[si]) // 将server name转换成clientEnd
					args := MigrateArgs{
						ConfigNum: configNum,
						ShardNum:  shardNum,
					}
					reply := MigrateReply{}
					ok := srv.Call("ShardKV.MigrateShard", &args, &reply)

					if !ok || (ok && reply.Err == ErrWrongLeader) { // 若联系不上该server或该server不是leader
						DPrintf("Group[%d]ShardKV[%d] request server[%v] for shard[%v] failed or [ErrWrongLeader]!\n", kv.gid, kv.me, servers[si], shardNum)
						continue // 向该组内下一个server再次请求
					}

					if ok && reply.Err == ErrNotReady { // 如果对方的config还没有更新到请求方的版本
						DPrintf("Group[%d]ShardKV[%d] request server[%v] for shard[%v] but [ErrNotReady]!\n", kv.gid, kv.me, servers[si], shardNum)
						break // 等待下次循环再请求该shard
					}

					if ok && reply.Err == OK { // 成功获取到shard数据
						// 生成一个command下放到raft在自己的集群中共识
						// 保证一个group的所有servers在操作序列的同一点完成shard迁移
						getShardOp := Op{
							OpType:      GetShard,
							CfgNum:      configNum,
							ShardNum:    shardNum,
							ShardData:   reply.ShardData,
							SessionData: reply.SessionData,
						}
						kv.rf.Start(getShardOp)
						DPrintf("Group[%d]ShardKV[%d] request server[%v] for shard[%v] [Successfully]!\n", kv.gid, kv.me, servers[si], shardNum)
						break
					}

				}
			}(preServers, curConfigNum, shard)
		}
		wg.Wait() // 阻塞等待所有协程完成

		time.Sleep(time.Millisecond * ConfigCheckInterval)
	}
}

// shard迁移的handle
// 负责其他组向该组请求shard迁移时作相关处理
func (kv *ShardKV) MigrateShard(args *MigrateArgs, reply *MigrateReply) {
	// 只有leader才能回复包含shard的数据
	if _, isLeader := kv.rf.GetState(); !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()

	DPrintf("DEBUG: Group[%d]ShardKV[%d] MigrateShard request: shard=%d, configNum=%d, myConfig=%d",
		kv.gid, kv.me, args.ShardNum, args.ConfigNum, kv.curConfig.Num)

	// 如果本身的config不如请求者的config新，则需要等到自己的更新后才能给他迁移shard
	// 出现的情况为kv.curConfig到args.Config之间不涉及当前两个组的shard迁移，因此请求方的config可以自增到前面去
	if kv.curConfig.Num < args.ConfigNum {
		reply.Err = ErrNotReady
		return
	}
	// 如果本身的config比对方更加新，正常情况不会出现
	// 因为如果请求方请求比较旧的config的shard迁移未完成，被请求方对应的shard状态将会一直是waitGive，这会阻止被请求方的config继续更新
	// 出现的情况：很久之前此shard迁移请求被发出但是由于网络原因到达得太慢，实际上请求方已经在后续的循环中获得了shard数据（否则被请求方的config不会继续更新）
	if kv.curConfig.Num > args.ConfigNum {
		return // 实际上请求方已经获得了数据，并没有等这个reply，直接return
	}

	// 只有双方的config.Num相等的时候才能将shard数据回复给请求者
	kvMap := make(map[string]string)
	if kv.kvDB[args.ShardNum] != nil {
		kvMap = deepCopyMap(kv.kvDB[args.ShardNum])
		DPrintf("DEBUG: Group[%d]ShardKV[%d] MigrateShard sending %d keys for shard %d",
			kv.gid, kv.me, len(kvMap), args.ShardNum)
		// 打印实际数据用于调试
		for k, v := range kvMap {
			DPrintf("DEBUG: MigrateShard key=%s, value=%s (len=%d)", k, v, len(v))
		}
	}
	reply.ShardData = kvMap

	// 同时把自己的session也发给对方，防止shard迁移后重复执行某些指令造成报错
	sessions := deepCopySession(kv.sessions)
	reply.SessionData = sessions
	reply.Err = OK

	DPrintf("DEBUG: Group[%d]ShardKV[%d] MigrateShard success: shard=%d, keys=%d",
		kv.gid, kv.me, args.ShardNum, len(kvMap))
}

func deepCopyMap(originMap map[string]string) map[string]string {
	newMap := map[string]string{}
	for k, v := range originMap {
		newMap[k] = v
	}
	return newMap
}

func deepCopySession(originSession map[int64]Session) map[int64]Session {
	newSession := map[int64]Session{}
	for k, v := range originSession {
		newSession[k] = v
	}
	return newSession
}

func (kv *ShardKV) checkAndFinishGiveShard() {
	for !kv.killed() {
		// 老规矩先检查是否是leader
		if _, isLeader := kv.rf.GetState(); !isLeader {
			time.Sleep(time.Millisecond * ConfigCheckInterval)
			continue
		}
		var waitGiveShards []int // waitGive状态的shard序号
		kv.mu.Lock()
		for shard, state := range kv.ownedShards {
			if state == WaitGive {
				waitGiveShards = append(waitGiveShards, shard)
			}
		}
		// 获取shard新所有者的信息要根据当前的config（因为当前处于config变更阶段，config版本是更新了的，只是没有完成变化）
		curConfig := kv.curConfig
		kv.mu.Unlock()
		// 同样使用waitGroup来保证一轮所有需要确认收到的shard都确认一遍再开始下次循环
		var wg sync.WaitGroup

		for _, shard := range waitGiveShards {
			wg.Add(1)
			curGid := curConfig.Shards[shard]
			curServers := curConfig.Groups[curGid]

			// 向现任shard所有者的组发送shard确认收到RPC，依次询问每一个成员直到找到leader
			go func(servers []string, configNum int, shardNum int) {
				defer wg.Done()
				for si := 0; si < len(servers); si++ {
					srv := kv.make_end(servers[si])
					args := AckArgs{
						ConfigNum: configNum,
						ShardNum:  shardNum,
					}
					reply := AckReply{}
					ok := srv.Call("ShardKV.AckReceiveShard", &args, &reply)
					if !ok || (ok && reply.Err == ErrWrongLeader) {
						// DPrintf("Group[%d]ShardKV[%d] request server[%v] for ack[shard %v] failed or [ErrWrongLeader]!\n",
						// 	kv.gid, kv.me, servers[si], shardNum)
						continue // 继续向该组下一个server再次请求
					}
					if ok && reply.Err == ErrNotReady {
						// DPrintf("Group[%d]ShardKV[%d] request server[%v] for ack[shard %v] but [ErrNotReady]!\n",
						// 	kv.gid, kv.me, servers[si], shardNum)
						break // 等待下次循环再来询问
					}
					if ok && reply.Err == OK && !reply.Receive {
						// 如果对方还没有收到或还没应用shard数据
						// DPrintf("Group[%d]ShardKV[%d] request server[%v] for ack[shard %v] but [not received shard]!\n",
						// 	kv.gid, kv.me, servers[si], shardNum)
						break
					}
					if ok && reply.Err == OK && reply.Receive {
						// 生成一个GiveShard command下方到raft在自己的集群中共识，通知自己的组员可以更新shard state = NoExist
						// 保证一个group的所有servers在操作序列的同一点完成shard迁移确认
						giveShardOp := Op{
							OpType:   GiveShard,
							CfgNum:   configNum,
							ShardNum: shardNum,
						}
						kv.rf.Start(giveShardOp)
						break
					}
				}
			}(curServers, curConfig.Num, shard)
		}
		wg.Wait() // 阻塞等待所有协程完成
		time.Sleep(time.Millisecond * ConfigCheckInterval)
	}
}

// shard迁移成功的确认请求handler
// 负责处理前任group发来的shard确认收到请求
func (kv *ShardKV) AckReceiveShard(args *AckArgs, reply *AckReply) {
	// 只有leader才能回复包含shard的数据
	if _, isLeader := kv.rf.GetState(); !isLeader {
		reply.Err = ErrWrongLeader
		return
	}

	kv.mu.Lock()
	defer kv.mu.Unlock()

	// 如果出现自己的config不如请求者的新，则等到自己更新后才能给他迁移成功与否的回复
	if kv.curConfig.Num < args.ConfigNum {
		reply.Err = ErrNotReady
		return
	}
	// 如果本身的config比请求方的config更新，说明了接收方已经在之前的config成功拿到了shard并成功继续更新配置
	if kv.curConfig.Num > args.ConfigNum {
		reply.Err = OK
		reply.Receive = true
		return
	}
	// 如果相等，则根据自己的shard状态回复
	if kv.ownedShards[args.ShardNum] == Exist {
		reply.Err = OK
		reply.Receive = true // 回复自己已经收到并且应用了shard数据
		return
	}

	reply.Err = OK
	reply.Receive = false // 自己还没有将shard数据共识后应用，让对方下次再来询问
	return
}

// leader定期检查当前term是否有日志，如果没有就加入一条空日志以便之前的term的日志可以提交
func (kv *ShardKV) addEmptyLog() {
	for !kv.killed() {
		// 只有leader才进行检查
		if _, isLeader := kv.rf.GetState(); !isLeader {
			time.Sleep(time.Millisecond * ConfigCheckInterval)
			continue
		}

		if !kv.rf.CheckCurrentTermLog() {
			// false表示没有当前term的日志条目
			emptyOp := Op{
				OpType: EmptyOp,
			}
			kv.rf.Start(emptyOp)
		}
		time.Sleep(time.Millisecond * ConfigCheckInterval)
	}
}

// servers[] contains the ports of the servers in this group.
//
// me is the index of the current server in servers[].
//
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
//
// the k/v server should snapshot when Raft's saved state exceeds
// maxraftstate bytes, in order to allow Raft to garbage-collect its
// log. if maxraftstate is -1, you don't need to snapshot.
//
// gid is this group's GID, for interacting with the shardmaster.
//
// pass masters[] to shardmaster.MakeClerk() so you can send
// RPCs to the shardmaster.
//
// make_end(servername) turns a server name from a
// Config.Groups[gid][i] into a labrpc.ClientEnd on which you can
// send RPCs. You'll need this to send RPCs to other groups.
//
// look at client.go for examples of how to use masters[]
// and make_end() to send RPCs to the group owning a specific shard.
//
// StartServer() must return quickly, so it should start goroutines
// for any long-running work.
// 初始化一个shardKV，本质是group中的一个server
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int,
	gid int, masters []*labrpc.ClientEnd, make_end func(string) *labrpc.ClientEnd) *ShardKV {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(ShardKV)
	kv.me = me
	kv.maxraftstate = maxraftstate
	kv.make_end = make_end
	kv.gid = gid // 每个group中的servers是不会变的
	kv.masters = masters

	// Your initialization code here.
	var mutex sync.Mutex
	kv.mu = mutex
	kv.dead = 0 // 0代表该server还存活，1代表它被kill了

	// Use something like this to talk to the shardmaster:
	// kv.mck = shardmaster.MakeClerk(kv.masters)
	// 将masters传给shardmater.MakeClerk()以便向shardmaster发送rpc
	kv.mck = shardmaster.MakeClerk(kv.masters)
	// 外层map的key为shard序号，value为该shard内部的键值对
	kv.kvDB = make(map[int]map[string]string)
	// 由于外层map的key最多有NShards个且已知，因此此处先初始化10个
	for i := 0; i < shardmaster.NShards; i++ {
		kv.kvDB[i] = make(map[string]string)
	}

	kv.applyCh = make(chan raft.ApplyMsg)
	// 一个group中的ShardKV servers下层构成一个raft集群
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)
	kv.sessions = make(map[int64]Session)
	kv.notifyMapCh = make(map[int]chan Reply) // 此处内部的channel也需要初始化，在该client第一次对kvserver发起请求的handler中初始化
	kv.logLastApplied = 0
	kv.passiveSnapshotBefore = false
	kv.preConfig = shardmaster.Config{Num: 0} // 明确设置初始状态
	kv.curConfig = shardmaster.Config{Num: 0}
	// 添加初始化时间戳或标识
	for i := 0; i < shardmaster.NShards; i++ {
		kv.ownedShards[i] = NoExist // 明确设置初始状态
	}
	// var shardArr [shardmaster.NShards]ShardState
	// kv.ownedShards = shardArr

	// 开一个go程来apply日志或快照
	go kv.applyMessage()

	// 开一个go程来定期检查log是否太大了需要快照
	go kv.checkSnapshotNeed()

	// 开goroutine定期轮询shardmaster以获得最新的配置
	go kv.getLastestConfig()

	// 开go程定期检查是否有shard需要从其他的group获取
	go kv.checkAndGetShard()

	// 开go程定期检查是否有shard迁移走已经完成
	go kv.checkAndFinishGiveShard()

	//	开go程定期检查raft层当前term是否有日志，如果没有就提交一条空日志帮助之前term的日志提交，防止活锁
	go kv.addEmptyLog()

	return kv
}
