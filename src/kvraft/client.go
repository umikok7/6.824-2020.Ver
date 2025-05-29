package kvraft

import (
	"crypto/rand"
	"math/big"
	mathrand "math/rand"

	"../labrpc"
)

type Clerk struct {
	servers []*labrpc.ClientEnd
	// You will have to modify this struct.

	// 可能发生的情况是：如果这个leader在commit log后crash了，
	// 但是还没响应给client，client就会重发这条command给新的leader，这样就会导致这个op执行两次。
	seqId    int // 唯一的标识序列号避免某op被执行两次
	leaderId int // 用于确定哪个服务器是leader，下一次直接发给该服务器避免在每个RPC中等待搜寻leader（hint中有提示）
	clientId int64
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	// You'll have to add code here.
	ck.clientId = nrand()                        // 随机生成clientId
	ck.leaderId = mathrand.Intn(len(ck.servers)) // 直接用库随机生成一个开头的LeaderId
	return ck
}

// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.Get", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
func (ck *Clerk) Get(key string) string {

	// You will have to modify this function.
	ck.seqId++
	args := GetArgs{
		Key:      key,
		SeqId:    ck.seqId,
		ClientId: ck.clientId,
	}
	serverId := ck.leaderId //可以理解为一种客户端缓存机制，直接读取上一次成功的服务器
	// 这样客户端就不需要每次都盲目地从第一个服务器开始尝试，而是可以直接联系最有可能是leader的服务器，从而提高效率。
	// 服务器可能不可用的原因，可以结合lab2，Raft的机制去理解，因为规定只有leader服务器才能处理客户端请求，所以简单的轮询可能会找到follower服务器导致不可用。

	for {
		reply := GetReply{}
		ok := ck.servers[serverId].Call("KVServer.Get", &args, &reply)
		if ok {
			if reply.Err == ErrNoKey {
				ck.leaderId = serverId
				return ""
			} else if reply.Err == OK {
				ck.leaderId = serverId // 标记一下，便于下一次请求的时候迅速找到可用的服务器
				return reply.Value
			} else if reply.Err == ErrWrongLeader {
				// 出现错误了，采用轮询的方式进行节点的切换
				serverId = (serverId + 1) % len(ck.servers)
				continue
			}
		}
		// 节点发生crash等原因，也要进行节点的切换
		serverId = (serverId + 1) % len(ck.servers)
	}
}

// shared by Put and Append.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.PutAppend", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
func (ck *Clerk) PutAppend(key string, value string, op string) {
	// You will have to modify this function.
	// my code
	ck.seqId++
	serverId := ck.leaderId
	args := PutAppendArgs{
		Key:      key,
		Value:    value,
		Op:       op,
		ClientId: ck.clientId,
		SeqId:    ck.seqId,
	}
	for {
		reply := PutAppendReply{}
		ok := ck.servers[serverId].Call("KVServer.PutAppend", &args, &reply)
		if ok {
			if reply.Err == OK {
				ck.leaderId = serverId
				return
			} else if reply.Err == ErrWrongLeader {
				serverId = (serverId + 1) % len(ck.servers)
				continue
			}
		}
		serverId = (serverId + 1) % len(ck.servers)
	}
}

func (ck *Clerk) Put(key string, value string) {
	ck.PutAppend(key, value, "Put")
}
func (ck *Clerk) Append(key string, value string) {
	ck.PutAppend(key, value, "Append")
}
