package shardmaster

import (
	"sync"
	"sync/atomic"
	"time"

	"../labgob"
	"../labrpc"
	"../raft"
)

// OpType类型
const (
	Join  = "Join"
	Leave = "Leave"
	Move  = "Move"
	Query = "Query"
)

const RespondTimeout = 500 // sharedmaster回复client的超时时间

type ShardMaster struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	// Your data here.
	sessions    map[int64]Session
	notifyMapCh map[int]chan Reply // shardmaster执行完请求操作后通过chan通知对应的handler方法回复client,key为日志的index
	configs     []Config           // indexed by config num
}

// session跟踪为client处理的上一次请求序列号，以及相关响应
// 如果shardmaster接收到序列号已经执行过请求则立即响应，而不需要重新执行请求
type Session struct {
	LastSeqNum int    // sharedmaster为该client处理的上一个请求的序列号
	Optype     string // 上一次处理的操作请求类型
	Response   Reply  // 对应的响应
}

// 响应的统一结构，用于shardmaster在session中保存最新请求的响应
type Reply struct {
	Err    Err
	Config Config // 仅Query请求时有效
}

// 四种操作的结构体并集，通过OpType区分，因此其中部分字段仅在特定操作中有效
type Op struct {
	// Your data here.
	ClientId int64
	SeqNum   int
	OpType   string
	Servers  map[int][]string // new GID -> servers mappings, // 仅在Join操作有效
	GIDs     []int            // 仅在Leave操作有用
	Shared   int              // 仅在Move操作有效
	GID      int              // 仅在Move操作有效
	CfgNum   int              // desired config number， 仅在Query操作有效
}

func (sm *ShardMaster) Join(args *JoinArgs, reply *JoinReply) {
	// Your code here.
	sm.mu.Lock()
	// shardmaster过滤重复的请求
	if args.SeqNum < sm.sessions[args.ClientId].LastSeqNum {
		// 标志该请求client已经收到正确的回复了，只是这个重发请求到shardmaster太晚
		sm.mu.Unlock()
		return
	} else if args.SeqNum == sm.sessions[args.ClientId].LastSeqNum {
		// 可将session记录的之前执行过该请求的结果直接返回，避免一个op执行多次
		reply.Err = sm.sessions[args.ClientId].Response.Err
		sm.mu.Unlock()
		return
	} else {
		// 如果是为处理过的新请求
		sm.mu.Unlock()
		joinOp := Op{
			ClientId: args.ClientId,
			SeqNum:   args.SeqNum,
			OpType:   Join,
			Servers:  args.Servers,
		}

		index, _, isLeader := sm.rf.Start(joinOp) // 将Join操作传给Raft层进行共识
		if !isLeader {
			reply.WrongLeader = true
			return
		}
		notifyCh := sm.createNotifyCh(index)

		// 等待请求执行完的回复并开始超时计时
		select {
		case res := <-notifyCh:
			reply.Err = res.Err
		case <-time.After(RespondTimeout * time.Millisecond):
			reply.Err = ErrTimeout
		}

		go sm.closeNotifyCh(index) // 关闭并删除此channel，下一个请求到来时重新初始化一个channel
	}

}

func (sm *ShardMaster) Leave(args *LeaveArgs, reply *LeaveReply) {
	// Your code here.
	sm.mu.Lock()
	// shardmaster过滤重复的请求
	if args.SeqNum < sm.sessions[args.ClientId].LastSeqNum {
		// 标志该请求client已经收到正确的回复了，只是这个重发请求到shardmaster太晚
		sm.mu.Unlock()
		return
	} else if args.SeqNum == sm.sessions[args.ClientId].LastSeqNum {
		// 可将session记录的之前执行过该请求的结果直接返回，避免一个op执行多次
		reply.Err = sm.sessions[args.ClientId].Response.Err
		sm.mu.Unlock()
		return
	} else {
		// 如果是为处理过的新请求
		sm.mu.Unlock()
		leaveOp := Op{
			ClientId: args.ClientId,
			SeqNum:   args.SeqNum,
			OpType:   Leave,
			GIDs:     args.GIDs, // 要离开的组的GID列表
		}

		index, _, isLeader := sm.rf.Start(leaveOp) // 将Op操作传给Raft层进行共识
		if !isLeader {
			reply.WrongLeader = true
			return
		}
		notifyCh := sm.createNotifyCh(index)

		// 等待请求执行完的回复并开始超时计时
		select {
		case res := <-notifyCh:
			reply.Err = res.Err
		case <-time.After(RespondTimeout * time.Millisecond):
			reply.Err = ErrTimeout
		}

		go sm.closeNotifyCh(index) // 关闭并删除此channel，下一个请求到来时重新初始化一个channel
	}
}

func (sm *ShardMaster) Move(args *MoveArgs, reply *MoveReply) {
	// Your code here.
	sm.mu.Lock()
	// shardmaster过滤重复的请求
	if args.SeqNum < sm.sessions[args.ClientId].LastSeqNum {
		// 标志该请求client已经收到正确的回复了，只是这个重发请求到shardmaster太晚
		sm.mu.Unlock()
		return
	} else if args.SeqNum == sm.sessions[args.ClientId].LastSeqNum {
		// 可将session记录的之前执行过该请求的结果直接返回，避免一个op执行多次
		reply.Err = sm.sessions[args.ClientId].Response.Err
		sm.mu.Unlock()
		return
	} else {
		// 如果是为处理过的新请求
		sm.mu.Unlock()
		moveOp := Op{
			ClientId: args.ClientId,
			SeqNum:   args.SeqNum,
			OpType:   Move,
			GID:      args.GID,   // 要移动到的组的Gid
			Shared:   args.Shard, // 要移动的分片序号
		}

		index, _, isLeader := sm.rf.Start(moveOp) // 将Op操作传给Raft层进行共识
		if !isLeader {
			reply.WrongLeader = true
			return
		}
		notifyCh := sm.createNotifyCh(index)

		// 等待请求执行完的回复并开始超时计时
		select {
		case res := <-notifyCh:
			reply.Err = res.Err
		case <-time.After(RespondTimeout * time.Millisecond):
			reply.Err = ErrTimeout
		}

		go sm.closeNotifyCh(index) // 关闭并删除此channel，下一个请求到来时重新初始化一个channel
	}
}

func (sm *ShardMaster) Query(args *QueryArgs, reply *QueryReply) {
	// Your code here.
	// 由于Query操作仅查询不会改变配置信息，因此类似于lab3中的Get操作，运行重复的Query请求
	queryOp := Op{
		ClientId: args.ClientId,
		SeqNum:   args.SeqNum,
		OpType:   Query,
		CfgNum:   args.Num, // 想要查询的Config的序号
	}

	index, _, isLeader := sm.rf.Start(queryOp) // 将Op操作传给Raft层进行共识
	if !isLeader {
		reply.WrongLeader = true
		return
	}
	notifyCh := sm.createNotifyCh(index)

	// 等待请求执行完的回复并开始超时计时
	select {
	case res := <-notifyCh:
		reply.Err = res.Err
		reply.Config = res.Config // Query请求要返回查询到的config
	case <-time.After(RespondTimeout * time.Millisecond):
		reply.Err = ErrTimeout
	}

	go sm.closeNotifyCh(index) // 关闭并删除此channel，下一个请求到来时重新初始化一个channel
}

func (sm *ShardMaster) createNotifyCh(index int) chan Reply {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	notifyCh := make(chan Reply, 1)
	sm.notifyMapCh[index] = notifyCh
	return notifyCh
}

func (sm *ShardMaster) closeNotifyCh(index int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	_, ok := sm.notifyMapCh[index]
	if ok {
		// 如果存在这个channel，那么就将它关闭即可
		close(sm.notifyMapCh[index])
		delete(sm.notifyMapCh, index)
	}
}

// the tester calls Kill() when a ShardMaster instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
func (sm *ShardMaster) Kill() {
	atomic.StoreInt32(&sm.dead, 1)
	sm.rf.Kill()
	// Your code here, if desired.
}

func (sm *ShardMaster) killed() bool {
	z := atomic.LoadInt32(&sm.dead)
	return z == 1
}

// needed by shardkv tester
func (sm *ShardMaster) Raft() *raft.Raft {
	return sm.rf
}

// servers[] contains the ports of the set of
// servers that will cooperate via Paxos to
// form the fault-tolerant shardmaster service.
// me is the index of the current server in servers[].
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister) *ShardMaster {
	sm := new(ShardMaster)
	sm.me = me
	sm.dead = 0 // 0表示server还存活，1代表它被kill了

	sm.configs = make([]Config, 1)
	// 根据实验要求，第一个config的编号应该是0，不包含如何组，且所有shard都被分配GID零
	sm.configs[0].Groups = map[int][]string{}

	labgob.Register(Op{})
	sm.applyCh = make(chan raft.ApplyMsg)
	sm.rf = raft.Make(servers, me, persister, sm.applyCh) // 由于一个shardmaster关联一个raft server，因此此处也要初始化一个对应的raft实例

	// Your code here.
	sm.sessions = map[int64]Session{}
	sm.notifyMapCh = make(map[int]chan Reply) // 里面的channel使用之前记得初始化

	go sm.applyConfigChange() // 开一个go程用来循环检测与apply从applyCh中传来的配置相关操作指令
	return sm
}

func (sm *ShardMaster) getLastConfig() Config {
	return sm.configs[len(sm.configs)-1]
}

// ShardMaster从applyCh中取出client的config变更请求并实际执行对应操作
func (sm *ShardMaster) applyConfigChange() {
	for !sm.killed() {
		applyMsg := <-sm.applyCh

		if applyMsg.CommandValid {
			// 对于一般的日志消息
			sm.mu.Lock()
			op, ok := applyMsg.Command.(Op)
			if !ok {
				DPrintf("convert fail!\n")
			} else {
				reply := Reply{}
				sessionRec, exist := sm.sessions[op.ClientId]

				// 如果apply指令之前已经apply过并且不是Query指令则不允许重复执行，返回之前已经保存的结果
				if exist && op.OpType != Query && op.SeqNum <= sessionRec.LastSeqNum {
					reply = sm.sessions[op.ClientId].Response
				} else {
					// 对于没有执行过的指令
					switch op.OpType {
					case Join:
						reply.Err = sm.executeJoin(op)
						DPrintf("ShardMaster[%v] Join(SeqNum = %v) apply success! Groups = %v, Shards = %v\n",
							sm.me, op.SeqNum, sm.getLastConfig().Groups, sm.getLastConfig().Shards)
					case Leave:
						reply.Err = sm.executeLeave(op)
						DPrintf("ShardMaster[%v] Leave(SeqNum = %v) apply success! Groups = %v, Shards = %v\n",
							sm.me, op.SeqNum, sm.getLastConfig().Groups, sm.getLastConfig().Shards)
					case Move:
						reply.Err = sm.executeMove(op)
						DPrintf("ShardMaster[%v] Move(SeqNum = %v) apply success! Groups = %v, Shards = %v\n",
							sm.me, op.SeqNum, sm.getLastConfig().Groups, sm.getLastConfig().Shards)
					case Query:
						reply.Err, reply.Config = sm.executeQuery(op)
						DPrintf("ShardMaster[%v] Query(SeqNum = %v) apply success! Groups = %v, Shards = %v\n",
							sm.me, op.SeqNum, sm.getLastConfig().Groups, sm.getLastConfig().Shards)
					default:
						DPrintf("Unexpected OpType...\n")
					}
					// 将最近执行的Query指令外的指令执行结果放在session中以便以后重复请求，可以直接返回，目的还是为了避免重复执行
					if op.OpType != Query {
						session := Session{
							LastSeqNum: op.SeqNum,
							Optype:     op.OpType,
							Response:   reply,
						}
						sm.sessions[op.ClientId] = session
						DPrintf("ShardMaster[%d].session[%d] = %v\n", sm.me, op.ClientId, session)
					}
				}

				// 如果任期和leader身份没有变，则向对应client的notifhMapCh发送reply通知对应的handle回复client
				if _, existCh := sm.notifyMapCh[applyMsg.CommandIndex]; existCh {
					if currentTerm, isLeader := sm.rf.GetState(); isLeader && applyMsg.CommandTerm == currentTerm {
						sm.notifyMapCh[applyMsg.CommandIndex] <- reply
					}
				}
			}
			sm.mu.Unlock()
		} else {
			DPrintf("ShardMaster[%d] get an unexpected ApplyMsg!\n", sm.me)
		}
	}
}

func (sm *ShardMaster) executeJoin(op Op) Err {
	lastConfig := sm.getLastConfig()
	newConfig := Config{}
	newConfig.Num = lastConfig.Num + 1

	// 先对原先的Groups map进行深拷贝
	newGroups := deepCopyGroups(lastConfig.Groups)

	// 向原配置的Groups中增加新Groups
	// Join可能在配置中已有的组中加入servers，也可能新增整个组
	for gid, servers := range op.Servers {
		newGroups[gid] = servers
	}

	newConfig.Groups = newGroups
	// shard负载均衡
	newConfig.Shards = shardLoadBalance(newGroups, lastConfig.Shards)
	sm.configs = append(sm.configs, newConfig)
	return OK
}

func (sm *ShardMaster) executeLeave(op Op) Err {
	lastConfig := sm.getLastConfig()
	newConfig := Config{}
	newConfig.Num = lastConfig.Num + 1

	// 先对原先的Groups map进行深拷贝
	newGroups := deepCopyGroups(lastConfig.Groups)

	// 从原先配置的Groups中去除指定的Groups
	for _, gid := range op.GIDs {
		delete(newGroups, gid) // 从newGroups删除指定的键
	}

	newConfig.Groups = newGroups
	var newShards [10]int
	// 避免负载均衡的时候除0
	if len(newGroups) != 0 {
		newShards = shardLoadBalance(newGroups, lastConfig.Shards)
	}
	newConfig.Shards = newShards
	sm.configs = append(sm.configs, newConfig)
	return OK
}

func (sm *ShardMaster) executeMove(op Op) Err {
	lastConfig := sm.getLastConfig()
	newConfig := Config{}
	newConfig.Num = lastConfig.Num + 1

	// move操作Groups无变化
	newConfig.Groups = deepCopyGroups(lastConfig.Groups)

	// 强制分配某个shard给某group，无需负载均衡
	newShards := lastConfig.Shards
	newShards[op.Shared] = op.GID // 直接将该shard的gid修改为对应值
	newConfig.Shards = newShards

	sm.configs = append(sm.configs, newConfig)
	return OK
}

func (sm *ShardMaster) executeQuery(op Op) (Err, Config) {
	lastConfig := sm.getLastConfig()
	// 如果查询的配置号为-1或大于已知的最大配置号，则shardmaster应回应最新配置
	if op.CfgNum == -1 || op.CfgNum > lastConfig.Num {
		return OK, lastConfig
	}
	// 由于configs[0]占位config存在，切片下标与config.Num有对应关系
	return OK, sm.configs[op.CfgNum]
}

// 根据hint中的描述，如果要基于前一个Config创建一个新的配置，则某些字段需要创建一个新的map，为了防止数据竞争
// 因此需要使用深拷贝避免新旧map引用到了同一块底层数据
func deepCopyGroups(originG map[int][]string) map[int][]string {
	newGroups := map[int][]string{}
	for gid, servers := range originG {
		// 创建一个新的servers切片，复制原始切片的内容到新切片中
		copiedServers := make([]string, len(servers))
		copy(copiedServers, servers)
		// 将复制后的切片放入新map中
		newGroups[gid] = copiedServers
	}
	return newGroups

}

// 负载均衡，Shard尽可能均匀地分给各group，且移动尽可能少
// 平均下来每个group最后的shard数量最多相差1
// 最后每个group负责的shard的数量为avgShardNum或avgShardNum+1均可
func shardLoadBalance(groups map[int][]string, lastShards [NShards]int) [NShards]int {
	resShards := lastShards // 通过简单赋值操作实现深拷贝
	groupNum := len(groups)
	// 新增
	if groupNum == 0 {
		var emptyShards [NShards]int
		return emptyShards
	}
	shardCnt := make(map[int]int, groupNum) // gid -> 负责的shard数量,暂存未迁移的情况下新groups中各group暂时负责的shard数量

	// 统计未迁移shard情况下新groups中各group负责的shard数量
	// 阶段1:统计先有组的当前shard分配情况，同时释放已经离开的shard
	for shard, gid := range lastShards {
		if _, eixst := groups[gid]; eixst {
			// 若新配置中仍然有这个组
			shardCnt[gid]++
		} else {
			// 该组已经离开了groups
			resShards[shard] = 0 // 将该位置的shard释放
		}
	}
	// 阶段2:确保所有组都在shardCnt有记录，新组初始化为0
	// 新groups中的gid组成的slice
	gidSlice := make([]int, 0, groupNum)
	for gid, _ := range groups {
		gidSlice = append(gidSlice, gid)
		if _, exist := shardCnt[gid]; !exist {
			// 对应本次新增的组（Join情况）
			shardCnt[gid] = 0 // 暂时还没分配Shard
		}
	}
	// 最后有remainder个组负责 avgShardNum+1 个shard， 剩下组负责avgShardNum个shard
	avgShardNum := NShards / groupNum
	remainder := NShards % groupNum

	// 对gidSlice进行排序，规则为：负载大的group排在前面，若负载相同则gid小的排在前面
	// 这样可以保证最后得到的负载均衡后的shard分配情况唯一
	for i := 0; i < len(gidSlice)-1; i++ {
		for j := len(gidSlice) - 1; j > i; j-- {
			if shardCnt[gidSlice[j]] > shardCnt[gidSlice[j-1]] ||
				(shardCnt[gidSlice[j]] == shardCnt[gidSlice[j-1]] && gidSlice[j] < gidSlice[j-1]) {
				gidSlice[j-1], gidSlice[j] = gidSlice[j], gidSlice[j-1]
			}
		}
	}

	// 由于已经按照shard数从大到小排序好了，因此gidSlice的前remainder个gid就是最后要负责（avgShardNum+1）个shard的
	for i := 0; i < groupNum; i++ {
		var curTar int // 该组最终应负责的shard数
		if i < remainder {
			curTar = avgShardNum + 1
		} else {
			curTar = avgShardNum
		}
		curGid := gidSlice[i]
		delta := shardCnt[curGid] - curTar // 该gid的组要变化的量
		if delta == 0 {
			// 该组已经为最终shard数，无需再变化了
			continue
		}

		// 将超载的组先释放掉超出的shard
		if delta > 0 {
			for j := 0; j < NShards; j++ {
				if delta == 0 {
					// 释放了delta个shard就退出
					break
				}
				if resShards[j] == curGid {
					resShards[j] = 0
					delta--
				}
			}
		}
	}

	// 将resShards中待分配的shard分配给不足的group
	for i := 0; i < groupNum; i++ {
		var curTar int // 该组最终应负责的shard数
		if i < remainder {
			curTar = avgShardNum + 1
		} else {
			curTar = avgShardNum
		}
		curGid := gidSlice[i]              // delta = 0会直接continue，所以此处一定能找到缺了的那个组id
		delta := shardCnt[curGid] - curTar // 该gid的组要变化的量
		if delta == 0 {
			continue
		}
		// 将待分配的shard迁移到shard不足的组
		if delta < 0 {
			for j := 0; j < NShards; j++ {
				if delta == 0 {
					break
				}
				if resShards[j] == 0 {
					resShards[j] = curGid
					delta++
				}
			}
		}
	}
	return resShards
}
