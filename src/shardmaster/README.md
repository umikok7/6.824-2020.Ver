- [ShardMaster](#ShardMaster)
- [架构分析](#1-架构分析)
- [关键函数分析](#2-关键函数分析)
- [关键操作流程分析](#3-关键操作流程)


本项目参考了 [博客](https://blog.csdn.net/qq_43460956/article/details/134885751) 的实现。




# ShardMaster

## 1. 架构分析
shardMaster是一个服务配置的集群，其核心的职责有：
- 管理shard到group的映射关系
- 维护group的成员信息
- 处理配置变更的请求（包含Join/Leave/Move/Query）
- 确保所有节点对配置的认知一致

```shell
ShardMaster 集群（通常 3-5 个节点）
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ ShardMaster │  │ ShardMaster │  │ ShardMaster │
│      1      │  │      2      │  │      3      │
│   (Leader)  │  │ (Follower)  │  │ (Follower)  │
└─────────────┘  └─────────────┘  └─────────────┘
        │                │                │
        └────────────────┼────────────────┘
                    Raft 共识层
```

在这其中，集群的一致性就通过与Raft层进行交互得到保证。

## 2. 关键函数分析

### 2.1 负载均衡算法
- **目标**：各组负责的shard数量最多相差1
- **策略**：最小移动原则，只调整必要的shard
- **确定性**：通过排序保证相同输入产生相同输出

**实现流程**：
- 第一阶段：实现`shardCnt`中完成对未迁移shard的情况下新groups中的各group暂时负责的shard数量记录

```go
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
```

- 第二阶段： 用规则保证了尽可能均匀地分配shard、移动尽可能少的shard且结果shards数组唯一

```go
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
```


- 第三阶段： 通过两次的`delta`操作，第一次将超载组的shard移除（标记为0），第二次从标记为0的shard中分配给该组

```go
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
```


经过以上的过程实现负载均衡

### 2.2 关键设计模式-异步通道管理
在各个op操作中的最后包含一段代码：
```go
go sm.closeNotifyCh(index)  // 异步关闭避免死锁
```
**原因**：
- 避免RPC方法与apply方法的锁竞争
- 确保客户端快速响应
- 防止通道发送方阻塞导致的死锁

### 2.3 深拷贝防止数据竞争
在各个实际的op操作中可能存在如下代码：
```go
newGroups := deepCopyGroups(lastConfig.Groups)
```

**必要性**：防止新旧配置引用同一底层数据结构

## 3. 关键操作流程
此处以Join操作为例子：
### 3.1 Join操作完整流程

1. 客户端发起Join请求

	```go
	ok := srv.Call("ShardMaster.Join", args, &reply)
	```

2. ShardMaster接收并处理RPC(以下实现均在`Join`中)
	- 过滤重复的请求 
	```go
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
		}
	```

	- 构造Op对象，提交给Raft层
	```go
	index, _, isLeader := sm.rf.Start(joinOp) // 将Join操作传给Raft层进行共识
	```

	- 等待执行的结果
	```go
		// 创建通知通道，等待操作执行完成
		notifyCh := sm.createNotifyCh(index)

		select {
		case res := <-notifyCh:
			reply.Err = res.Err
		case <-time.After(RespondTimeout * time.Millisecond):
			reply.Err = ErrTimeout
		}

		go sm.closeNotifyCh(index)  // 异步清理通道，避免死锁
	```

3. Apply层执行操作

	- Raft达成共识，通过applyCh通知
	```go
	func (sm *ShardMaster) applyConfigChange() {
		for !sm.killed() {
			applyMsg := <-sm.applyCh  // 从 Raft 接收已提交的操作
			...
	```


4. 具体的Join操作：`executeJoin`
	- 添加新组
	- 负载均衡

5. 保存session，通知客户端
	具体实现都在`applyConfigChange`中
	```go
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
	```

	```go
	// 如果任期和leader身份没有变，则向对应client的notifhMapCh发送reply通知对应的handle回复client
	if _, existCh := sm.notifyMapCh[applyMsg.CommandIndex]; existCh {
		if currentTerm, isLeader := sm.rf.GetState(); isLeader && applyMsg.CommandTerm == currentTerm {
			sm.notifyMapCh[applyMsg.CommandIndex] <- reply
		}
	}
	```
