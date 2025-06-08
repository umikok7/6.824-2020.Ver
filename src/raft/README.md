
- [启动raft-server](#1-启动raft-server)
- [raft选举以及心跳机制](#2-raft选举以及心跳机制)
- [raft的主动以及被动快照](#3-raft的主动以及被动快照)



本项目参考了 [博客](https://blog.csdn.net/qq_43460956/article/details/134869695?spm=1001.2014.3001.5502) 的实现。

Raft可视化模拟可参考[Raft可视化模拟](https://raft.github.io)

# Lab2全流程梳理

## 1. 启动Raft Server
启用一个raft server是通过`Make()`函数实现的，在Make()函数中初始化变量、从crash中恢复、启用超时检测、apply日志的go程，最后返回一个Raft指针。

```go
func Make(peers []*labrpc.ClientEnd, me int,persister *Persister, applyCh chan ApplyMsg) *Raft {

    ...

    rf.timer = time.NewTimer(time.Duration(getRandMS(300, 500)) * time.Millisecond)

    ...
    // 起一个goroutine循环处理超时
	go rf.HandleTimeout()

	// 起一个goroutine循环检查是否有需要应用到状态机日志
	go rf.applier()

     ...
    }
```

## 2. Raft选举以及心跳机制

### 2.1 超时检测
通过`HandleTimeout`实现

```go
// 处理超时的协程，会一直循环检测超时
func (rf *Raft) HandleTimeout() {
	for {
		select {
		case <-rf.timer.C: // 当timer超时后会向C(Timer内置的channel)中发送当前时间，此时case的逻辑就会执行，从而实现超时处理
			if rf.killed() { // 如果rf被kill了就不继续检测了
				return
			}
			rf.mu.Lock()
			nowState := rf.state // 记录下状态，以免switch访问rf.state时发送DATA RACE
			rf.mu.Unlock()

			switch nowState { // 根据当前的角色来判断属于哪种超时情况，执行对应的逻辑
			case Follower: // 如果是follower，则超时是因为一段时间没接收到leader的心跳或candidate的投票请求
				// 竞选前重置计时器（选举超时时间）
				rf.timer.Stop()
				rf.timer.Reset(time.Duration(getRandMS(300, 500)) * time.Millisecond)

				go rf.RunForElection() // follower宣布参加竞选
			case Candidate: // 如果是candidate，则超时是因为出现平票等造成上一任期竞选失败
				// 重置计时器
				// 对于刚竞选失败的candidate，这个计时是竞选失败等待超时设定
				// 对于已经等待完竞选失败等待超时设定的candidate，这个计时是选举超时设定
				rf.timer.Stop()
				rf.timer.Reset(time.Duration(getRandMS(300, 500)) * time.Millisecond)

				rf.mu.Lock()
				if rf.ready { // candidate等待完竞选等待超时时间准备好再次参加竞选
					rf.mu.Unlock()
					go rf.RunForElection()
				} else {
					rf.ready = true // candidate等这次超时后就可以再次参选
					rf.mu.Unlock()
				}
			case Leader: // 成为leader就不需要超时计时了，直至故障或发现自己的term过时
				return
			}

		}
	}
}
```

此处主要需要对`timer`进行理解，在`Make()`中已经初始化了这个闹钟，当初始化的随机在300-500ms之间的时间一到，就会向`timer.C`发送消息，这时候就会进入接下来的逻辑。

此外需要注意的是在`candidate`或者`follower`的情况下都会重新启用定时器，并且也是随机的时间，这样能够避免羊群效应，不会存在同时发起选举导致投票分散、无人当选的情况。

最后需要注意`ready`这个字段的含义，他用于区分candidate是竞选失败还是选举超时，通俗来说 为true表示的是竞选失败，可以马上参选，为false表示的是选举超时，需要等待一段时间才能重新参选；（超时了说明网络不稳定，所以需要等一段时间再重新参选）

### 2.2 开始选举
`func (rf *Raft) RunForElection()`来实现这部分的逻辑，此处主要需要了解go语言中的条件变量的使用

```go
func (rf *Raft) RunForElection(){
    ...
    // 利用协程并行地发送请求投票RPC，参考lec5的code示例vote-count-4.go
		go func(idx int) {
			rf.mu.Lock()
			args := RequestVoteArgs{
				Term:         sameTerm, // 此处用之前记录的currentTerm副本
				CandidatedId: rf.me,
				LastLogIndex: rf.log[len(rf.log)-1].Index,
				LastLogTerm:  rf.log[len(rf.log)-1].Term,
			}
			rf.mu.Unlock()
			reply := RequestVoteReply{}

			// 注意传的是args和reply的地址而不是结构体本身！
			ok := rf.sendRequestVote(idx, &args, &reply) // candidate向 server i 发送请求投票RPC
			if !ok {
				DPrintf("Candidate %d call server %d for RequestVote failed!\n", rf.me, idx)
			}

			// 如果candidate任期比其他server的小，则candidate更新自己的任期并转为follower，并跟随此server
			rf.mu.Lock()

			// 处理RPC回复之前先判断，如果自己不再是Candidate了则直接返回
			// 防止任期混淆（当收到旧任期的RPC回复，比较当前任期和原始RPC中发送的任期，如果两者不同，则放弃回复并返回）
			if rf.state != Candidate || rf.currentTerm != args.Term {
				rf.mu.Unlock()
				return
			}

			if rf.currentTerm < reply.Term {
				rf.votedFor = -1            // 当term发生变化时，需要重置votedFor
				rf.state = Follower         // 变回Follower
				rf.currentTerm = reply.Term // 更新自己的term为较新的值
				rf.persist()
				rf.mu.Unlock()

				rf.timer.Stop()
				rf.timer.Reset(time.Duration(getRandMS(300, 500)) * time.Millisecond)
				return // 这里只是退出了协程.
			}
			rf.mu.Unlock()

			vote := reply.VoteGranted // 查看是否收到选票。如果RPC发送失败，reply中的投票仍是默认值，相当于没收到投票
			voteMu.Lock()             // 访问votes和finished前加锁
			if vote {
				DPrintf("Candidate %d got a vote from server %d!\n", rf.me, idx)
				votes++
			}
			finished++
			voteMu.Unlock()
			cond.Broadcast() // // Broadcast 会清空队列，唤醒全部的等待中的 goroutine
		}(i) // 因为i随着for循环在变，因此将它作为参数传进去
	}

	sumNum := len(rf.peers)     // 集群中总共的server数
	majorityNum := sumNum/2 + 1 // 满足大多数至少需要的server数量

	voteMu.Lock() // 调用 Wait 方法的时候一定要持有锁
	// 检查是否满足“获得大多数选票”的条件
	for votes < majorityNum && finished != sumNum { // 投票数尚不够，继续等待剩余server的投票（要么投票数量够了，要么已经全部投完了，否则一直循环）
		cond.Wait() // 调用该方法的 goroutine 会被放到 Cond 的等待队列中并阻塞，直到被 Signal 或者 Broadcast 方法唤醒

		// 当candidate收到比自己term大的rpc回复时它就回到follower，此时直接结束自己的竞选
		// 或者该candidate得不到多数票但又由于有server崩溃而得不到sumNum张选票而一直等待，此时只有当有leader出现并发送心跳让该candidate变回follower跳出循环
		// 所以每次都要检测该candidate是否仍然在竞选，如果它已经退选，就不用一直等待选票了
		rf.mu.Lock()
		stillCandidate := (rf.state == Candidate)
		rf.mu.Unlock()
		if !stillCandidate {
			voteMu.Unlock() // 如果提前退出，不要忘了把这个锁释放掉
			return
		}
	}

	if votes >= majorityNum { // 满足条件，直接当选leader
		rf.Convert2Leader() // 成为leader就不需要超时计时了，直至故障或发现自己的term过时
	} else { // 收到所有回复但选票仍不够的情况，即竞选失败
		DPrintf("Candidate %d failed in the election and continued to wait...\n", rf.me)
	}
	voteMu.Unlock()
}
```

通过条件变量的使用来避免**轮询**的方式去查看投票数量是否正确，这样兼具了效率以及实时性：只有收到了回复的时候才会唤起候选人去检查是否满足成为leader的条件


### 2.3 发起投票请求,以及对应handler函数

在`RunForElection`中会发起投票请求

```go
ok := rf.sendRequestVote(idx, &args, &reply) // candidate向 server i 发送请求投票RPC
```

随后进行处理：`RequestVote`，在此之中voter会判断是否给candidate进行投票


### 2.4 当选leader

在candidate收到足够的赞成票后会当选leader，之后它就需要担负leader的职责开始自己的工作

```go
func (rf *Raft) Convert2Leader(){
    ...
	// leader上任时初始化nextIndex以及matchIndex
	rf.nextIndex = make([]int, len(rf.peers))
	for i := 0; i < len(rf.nextIndex); i++ {
		rf.nextIndex[i] = rf.log[len(rf.log)-1].Index + 1 // 初始化为leader的最后一个日志条目index+1
	}
	rf.matchIndex = make([]int, len(rf.peers))
	for i := 0; i < len(rf.matchIndex); i++ {
		rf.matchIndex[i] = 0 // 初始化为0
	}
    ...
}
```

在此之中有两个很重要的参数需要理解：

- `nextIndex`的值一开始被设置为日志的最高index，随着AppendEntries RPC返回不匹配而逐渐减小，逼近日志匹配的位置。

- `matchIndex`开始时认为没有日志项匹配，只有在AppendEntries RPC匹配上了才能更新对应值。这样做是为了数据安全：只有当某个日志项被成功复制到了多数派，leader才能更新commitIndex为日志项对应的index。而判断日志项被成功复制到了大多数server就是根据matchIndex的各个值。

### 2.5 发送心跳信息

`LeaderAppendEntries()`会进行leader的心跳信息（日志信息）的发送，会有两个地方进行心跳信息的发送，一个是在：`Start()`函数内部

```go
// 接受客户端的command，并且应用raft算法
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	term, isLeader = rf.GetState() // 直接调用GetState()来获取该server的当前任期以及是否为leader

	rf.mu.Lock()
	defer rf.mu.Unlock()

	index = rf.log[len(rf.log)-1].Index + 1 // index：如果这条日志最后被提交，那么它将在日志中的索引（注意索引0处的占位元素也算在切片len里面）

	if isLeader == false { // 如果这个Server不是leader则直接返回false，不继续执行后面的追加日志
		return term, term, false
	}

	// 如果是leader则准备开始向其他server复制日志
	newLog := LogEntry{ // 将指令形成一个新的日志条目
		Command: command,
		Term:    term,
		Index:   index,
	}
	rf.log = append(rf.log, newLog) // leader首先将新日志条目追加到自己的日志中
	rf.persist()
	DPrintf("[Start]Client sends a new commad(%v) to Leader %d!\n", command, rf.me)
	// 客户端发来新的command，复制日志到各server，调用LeaderAppendEntries()
	// 如果追加失败（网络问题或日志不一致被拒绝），则重复发送由携带日志条目的周期性的心跳包来完成
	go rf.LeaderAppendEntries() // 由新日志触发AppendEntries RPC的发送
	return index, term, true
}

```

- 只有该server认为自己是leader时才会为该指令生成日志条目并追加到自己的日志末尾，然后向其他server共识
- 追加完新日志条目后直接用一个协程来调用`LeaderAppendEntries()`来发送AppendEntries RPC同步日志。

此外就是在成为leader后的`Convert2Leader`函数中会发送心跳；

#### 2.5.1 LeaderAppendEntries的诸多难点

`LeaderAppendEntries()`是Leader向Follower发送日志的核心函数，主要处理两种情况：

	1. 周期性心跳包（空日志，维持Leader地位）
	2. 日志复制（携带新的command）


1. **更新Leader自己的索引**
   ```go
   rf.matchIndex[rf.me] = rf.log[len(rf.log)-1].Index // Leader也算"大多数"的一员
   rf.nextIndex[rf.me] = rf.matchIndex[rf.me] + 1
   ```
   **关键理解：** Leader在计算"大多数节点已复制"时必须包含自己

2. **并行发送RPC**
   ```go
   for i, _ := range rf.peers {
       if i == rf.me { continue }
       go func(idx int) {
           // 向每个Follower发送AppendEntries RPC
       }(i)
   }
   ```
   **关键理解：** 并行发送提高效率，不会因为单个节点问题影响整体

3. **处理三种关键情况：**

   **情况A：需要发送快照**
   ```go
   if nextIdx <= rf.lastIncludedIndex {
       go rf.LeaderSendSnapshot(idx, rf.persister.ReadSnapshot())
       return
   }
   ```
   **何时发生：** Follower太落后，需要的日志已被快照替代，这种情况下直接发送快照

   **情况B：日志追加失败（快速回退优化）**
   ```go
   if reply.Success == false {
       // 不是一个一个回退，而是快速跳转到可能匹配的位置
       possibleNextIdx := reply.ConflictIndex // 简化理解
       rf.nextIndex[idx] = possibleNextIdx
   }
   ```
   **核心思想：** 避免一个一个试，直接跳到冲突位置

   **情况C：日志追加成功**
   ```go
   if reply.Success == true {
       rf.matchIndex[idx] = args.PreLogIndex + len(args.Entries)
       rf.nextIndex[idx] = rf.matchIndex[idx] + 1
       
       // 检查是否可以更新commitIndex（大多数节点已复制）
       sortMatchIndex := make([]int, len(rf.peers))
       copy(sortMatchIndex, rf.matchIndex)
       sort.Ints(sortMatchIndex)
       maxN := sortMatchIndex[(len(sortMatchIndex)-1)/2] // 中位数 = 大多数的最小值
   }
   ```
   **核心思想：** 通过排序找中位数，确定大多数节点都有的最高日志索引

**重点理解：**
- **matchIndex vs nextIndex：** matchIndex记录"已确认复制"，nextIndex记录"下次发送位置"
- **大多数共识：** 排序后取中位数，这就是大多数节点都有的日志位置
- **任期安全：** 只能提交当前任期的日志


### 2.6 follower接收AppendEntries RPC，处理心跳消息
通过`AppendEntries`来实现的这部分逻辑；

1. **任期检查和状态更新**
   ```go
   if args.Term < rf.currentTerm {
       reply.Success = false  // 拒绝过时的Leader
       return
   }
   
   if args.Term > rf.currentTerm {
       rf.currentTerm = args.Term  // 更新到更高任期
       rf.votedFor = -1
   }
   
   rf.state = Follower  // 承认Leader地位
   rf.timer.Reset(...)  // 重置选举超时
   ```
   **关键理解：** 更高任期拥有绝对权威，必须立即跟随

2. **日志一致性检查**
   ```go
   // 检查PreLogIndex位置是否匹配
   if rf.log[len(rf.log)-1].Index < args.PreLogIndex || 
      rf.log[args.PreLogIndex-rf.lastIncludedIndex].Term != args.PreLogTerm {
       
       // 不匹配，返回冲突信息帮助Leader快速定位
       reply.Success = false
       reply.ConflictIndex = xxx  // 冲突位置
       reply.ConflictTerm = xxx   // 冲突任期
       return
   }
   ```
   **关键理解：** 只有前一个日志匹配，才能安全地追加新日志

3. **日志修复和追加**
   ```go
   // 找到冲突点
   misMatchIndex := -1
   for i, entry := range args.Entries {
       if 现有日志与新日志冲突 {
           misMatchIndex = args.PreLogIndex + 1 + i
           break
       }
   }
   
   if misMatchIndex != -1 {
       // 截断冲突部分，追加新日志
       newLog := rf.log[:misMatchIndex-rf.lastIncludedIndex]
       newLog = append(newLog, 新的日志条目...)
       rf.log = newLog
   }
   ```
   **关键理解：** 发现冲突立即截断，确保与Leader一致

4. **更新提交进度**
   ```go
   if args.LeaderCommit > rf.commitIndex {
       rf.commitIndex = min(args.LeaderCommit, rf.log最后一个索引)
   }
   ```
   **关键理解：** 跟上Leader的提交进度，但不能超过自己的日志

**重点理解：**
- **心跳的作用：** 维持Leader地位 + 重置Follower超时 + 同步提交进度
- **一致性检查：** 必须先匹配前一个日志，才能安全追加
- **冲突处理：** 发现不一致立即截断，以Leader为准
- **安全边界：** 提交进度不能超过自己拥有的日志

### 2.7 通过applier应用到状态机
由`applier`函数实现；





## 3. Raft的主动以及被动快照
对于没有快照功能的程序，重启服务器回重播完整的Raft日志以恢复状态，这是不可取的，通过快照替代一部分日志，删除被快照替代的日志条目才符合长久的使用；

### 3.1 主动快照

通过应用层调用`Snapshot`函数触发**主动快照**
```go
	// leader or follower都可以主动快照
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	rf.mu.Lock()

	// 如果主动快照的index不大于rf之前的lastIncludedIndex（这次快照其实是重复或更旧的），则不应用该快照
	if index <= rf.lastIncludedIndex {
		DPrintf("Server %d refuse this positive snapshot(index=%v, rf.lastIncludedIndex=%v).\n", rf.me, index, rf.lastIncludedIndex)
		rf.mu.Unlock()
		return
	}

	DPrintf("Server %d start to positively snapshot(rf.lastIncluded=%v, snapshotIndex=%v).\n", rf.me, rf.lastIncludedIndex, index)

	// 修剪log[]，将index及以前的日志条目剪掉
	var newLog = []LogEntry{{Term: rf.log[index-rf.lastIncludedIndex].Term, Index: index}} // 裁剪后依然log索引0处用一个占位entry，不实际使用
	newLog = append(newLog, rf.log[index-rf.lastIncludedIndex+1:]...)                      // 这样可以避免原log底层数组由于有部分在被引用而无法将剪掉的部分GC（真正释放）
	rf.log = newLog
	rf.lastIncludedIndex = newLog[0].Index
	rf.lastIncludedTerm = newLog[0].Term
	// 主动快照时lastApplied、commitIndex一定在snapshotIndex之后，因此不用更新

	// 通过persister进行持久化存储
	rf.persist() // 先持久化raft state（因为rf.log，rf.lastIncludedIndex，rf.lastIncludedTerm改变了）
	state := rf.persister.ReadRaftState()
	rf.persister.SaveStateAndSnapshot(state, snapshot)

	isLeader := (rf.state == Leader)
	rf.mu.Unlock()

	// leader通过InstallSnapshot RPC将本次的SnapShot信息发送给其他Follower
	if isLeader {
		for i, _ := range rf.peers {
			if i == rf.me {
				continue
			}
			go rf.LeaderSendSnapshot(i, snapshot)
		}
	}
	return

}
```

### 3.2 被动快照

被动快照是**Follower接收Leader发来的快照**，通过InstallSnapshot RPC实现，当Follower严重落后的时候会触发被动快照：

```go
// 在LeaderAppendEntries中检查
if nextIdx <= rf.lastIncludedIndex {
    // Follower需要的日志已被快照替代
    go rf.LeaderSendSnapshot(idx, rf.persister.ReadSnapshot())
    return
}
```

### 3.3 快照发送以及处理逻辑

1. leader发送快照
	```go
	func (rf *Raft) LeaderSendSnapshot(idx int, snapshotData []byte) {
		args := InstallSnapshotArgs{
			Term:              rf.currentTerm,
			LeaderId:          rf.me,
			LastIncludedIndex: rf.lastIncludedIndex,
			LastIncludedTerm:  rf.lastIncludedTerm,
			SnapshotData:      snapshotData,
		}
		
		// 发送InstallSnapshot RPC
		ok := rf.sendSnapshot(idx, &args, &reply)
	}
	```

2. Follower处理快照
	```go
	func (rf *Raft) CondInstallSnap(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
		// 1. 基本检查
		if args.Term < rf.currentTerm {
			reply.Accept = false
			return
		}
		
		// 2. 防止主动快照冲突
		if rf.activeSnapshotting {
			reply.Accept = false  // 正在主动快照，拒绝被动快照
			return
		}
		
		// 3. 检查快照是否更新
		if args.LastIncludedIndex <= rf.lastIncludedIndex {
			reply.Accept = false  // 快照过时，拒绝
			return
		}
	}
	```

3. 应用快照
	```go
	// 截断日志并应用快照
	if snapshotIndex < rf.log[len(rf.log)-1].Index {
		// 部分日志需要保留
		if rf.log[snapshotIndex-rf.lastIncludedIndex].Term != snapshotTerm {
			// 任期冲突，截断所有后续日志
			newLog = []LogEntry{{Term: snapshotTerm, Index: snapshotIndex}}
		} else {
			// 任期匹配，保留后续日志
			newLog = []LogEntry{{Term: snapshotTerm, Index: snapshotIndex}}
			newLog = append(newLog, rf.log[snapshotIndex-rf.lastIncludedIndex+1:]...)
		}
	} else {
		// 快照包含所有日志，清空日志
		newLog = []LogEntry{{Term: snapshotTerm, Index: snapshotIndex}}
	}

	...
	// 更新状态
	rf.log = newLog
	rf.lastIncludedIndex = args.LastIncludedIndex
	rf.lastIncludedTerm = args.LastIncludedTerm
	rf.commitIndex = args.LastIncludedIndex
	rf.lastApplied = args.LastIncludedIndex
	...

	```


#### 3.4 索引转换机制
在没有快照功能时，由于log[0]占位日志的存在，日志条目的index和其在log中的下标是对应的，因此在很多地方通过index可以“直接”访问到log中对应的元素。加入快照功能后，裁剪日志的操作会破坏这种对应关系，直接用index访问会出现数组访问越界的报错。故要对程序利用index访问log的地方作出修改，进行下标变换以正常访问log。

```go
// 快照后的索引转换
logIndex := actualIndex - rf.lastIncludedIndex

// 示例：
// 快照前：log[0,1,2,3,4,5,6,7,8,9]
// 快照到index=5：lastIncludedIndex=5
// 快照后：log[5,6,7,8,9] （index=5作为占位符）
// 访问实际index=7的日志：log[7-5] = log[2]
```

