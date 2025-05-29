# Lab2：Raft

## 介绍

这是一个系列实验中的第一个，在这些实验中将构建一个具有容错性的键值存储系统。在这个实验中将实现一个复制（replicated）状态机协议-Raft。下一个实验将在Raft之上构建一个键值服务。然后，将服务“分片”到多个复制状态机上，以获得更高的性能。

**复制服务通过在多个副本服务器上存储完整的状态（即数据）来实现容错。**即使在一些服务器发生了故障（崩溃或网络中断），复制可以允许服务继续运行。**挑战在于，故障可能导致副本持有不同的数据副本**。

**Raft将客户端请求组织成一个序列，称为日志，并确保所有副本服务器都看到相同的日志。**每个副本按照日志顺序执行客户端请求，将它们应用到服务状态的本地副本上。由于所有存活着的副本看到相同的日志内容，它们都会以相同的顺序执行相同的请求，从而继续保持相同的服务状态。如果一个服务器发生故障但后来恢复了，Raft会负责使其日志保持最新。**只要大多数服务器存活并能够相互通信，Raft就会继续运行。**如果没有这样的大多数，Raft将无法取得进展，但一旦大多数服务器可以再次通信，它将从上次停止的地方继续。

在本次实验中，将实现一个作为Go对象类型的Raft协议及其相关方法，该模块旨在用于更大服务中的一个组件。一组Raft实例通过RPC互相通信以维护复制日志。你的Raft接口将支持一个无限序列的编号命令，也称为日志条目。这些条目将以索引号进行编号。具有特定索引的日志条目最终会被提交。在那时，你的Raft应将日志条目发送给更大的服务以执行。

应遵循扩展[Raft论文](http://nil.csail.mit.edu/6.824/2022/papers/raft-extended.pdf)中的设计，**特别是关注图2的内容**。**需要实现论文中的大部分内容，包括保存持久状态和在节点失败后重新读取这些状态。但不需要实现集群成员变更**（第6节）。

这份[指南](https://thesquareplanet.com/blog/students-guide-to-raft/)有用，还有关于[并发锁定](http://nil.csail.mit.edu/6.824/2022/labs/raft-locking.txt)和[结构](http://nil.csail.mit.edu/6.824/2022/labs/raft-structure.txt)的建议。为了获得更广泛的视角，可以参考Paxos、Chubby、Paxos Made Live、Spanner、Zookeeper、Harp、Viewstamped Replication和[Bolosky](http://static.usenix.org/event/nsdi11/tech/full_papers/Bolosky.pdf)等文献。（注意：学生指南编写于数年前，特别是第2D部分已经发生变化。在盲目遵循之前，请确保你理解某个特定实现策略的合理性。）

本次实验中最具挑战性的部分可能不是实现解决方案，而是调试。为了应对这一挑战，可能需要花时间思考如何让实现更易于调试。可以参考[指导页面](http://nil.csail.mit.edu/6.824/2022/labs/guidance.html)和这篇关于[有效使用打印语句](https://blog.josejg.com/debugging-pretty/)的博客文章。

课程还提供了一个[Raft交互图](http://nil.csail.mit.edu/6.824/2022/notes/raft_diagram.pdf)，可以帮助理清Raft代码如何与其上层交互。

![img](https://saz0h92ypo.feishu.cn/space/api/box/stream/download/asynccode/?code=YWVmZjA4NzYzY2M2MTE1NjM1NWVkMjMzMTNkNjZiZDdfRVRZQ0FTMGdzN0pOMXlIRElub2tOY0tCMEQ1blRTMHpfVG9rZW46SlRkQmJKbzZ6b3RYYWZ4YVFUWWM3cUlpblRkXzE3MjAyNjg2NTA6MTcyMDI3MjI1MF9WNA)

本次实验分为四部分。

## 开始

如果已经完成了实验1，那么应该已经有实验源代码的副本。如果没有，可以在[实验1的说明](http://nil.csail.mit.edu/6.824/2022/labs/lab-mr.html)中找到通过git获取源代码的指示。

框架代码为 `src/raft/raft.go`。课程还提供了一组测试，应该使用这些测试来推动实现工作，课程也将使用这些测试来评分实验。**测试文件位于** **`src/raft/test_test.go`****。**

当评分提交时，会在不使用 `-race` 标志的情况下运行测试。然而，个人应该确保代码没有竞态条件，因为竞态条件可能导致测试失败。因此，**强烈建议在开发解决方案时也使用** **`-race`** **标志来运行测试。**

要开始运行，请执行以下命令。不要忘记使用 `git pull` 获取最新的代码。

```Bash
$ cd ~/6.824
$ git pull
...
$ cd src/raft
$ go test
Test (2A): initial election ...
--- FAIL: TestInitialElection2A (5.04s)
        config.go:326: expected one leader, got none
Test (2A): election after network failure ...
--- FAIL: TestReElection2A (5.03s)
        config.go:326: expected one leader, got none
...
$
```

## 代码实现

**在** **`raft/raft.go`** **文件中添加代码以实现 Raft。**在该文件中，可以找到框架代码，以及发送和接收 RPC 的示例。

实现必须支持以下接口，测试程序和（最终）键/值服务器将使用这个接口。可以在 `raft.go` 的注释中找到更多详细信息。

```Go
// create a new Raft server instance:
rf := Make(peers, me, persister, applyCh)

// start agreement on a new log entry:
rf.Start(command interface{}) (index, term, isleader)

// ask a Raft for its current term, and whether it thinks it is leader
rf.GetState() (term, isLeader)

// each time a new entry is committed to the log, each Raft peer
// should send an ApplyMsg to the service (or tester).
type ApplyMsg
```

**服务调用** **`Make(peers, me, ...)`** **来创建一个 Raft 节点。****`peers`** **参数是一个包含所有 Raft 节点（包括本节点）网络标识符的数组，用于** **RPC** **通信。****`me`** **参数是本节点在** **`peers`** **数组中的索引。****`Start(command)`** **要求 Raft 开始处理，将命令追加到复制日志。****`Start()`** **应该立即返回，而不等待日志追加完成（异步）。服务期望你的实现为每个新提交的日志条目发送一个** **`ApplyMsg`** **到** **`Make()`** **的** **`applyCh`** **通道参数。**

`raft.go` 包含发送 RPC（`sendRequestVote()`）和处理接收的 RPC（`RequestVote()`）的示例代码。 Raft 节点应该使用 `labrpc` Go 包（源代码在 `src/labrpc`）交换 RPC请求。测试程序可以指示 `labrpc` 延迟、重新排序和丢弃 RPC，以模拟各种网络故障。虽然可以暂时修改 `labrpc`，但确保所实现的 Raft 能与原始的 `labrpc` 协同工作，因为课程会使用原始的 `labrpc` 来测试和评分实验。所实现的 Raft 实例必须仅通过 RPC 交互；例如，**不允许它们使用共享的 Go 变量或文件进行通信。**

后续的实验将基于这个实验，因此使用足够的时间编写稳健的代码非常重要。

先了解一下Raft协议的基本概念和工作原理。Raft是一种用于管理复制日志的一致性算法，通过将日志条目复制到多个服务器上以实现容错。Raft通过选举机制来选出一个领导者（Leader），由领导者负责处理所有客户端请求并将日志条目复制到所有从节点（Follower）上。

### Raft协议的主要组成部分

1. **领导者选举（Leader Election）**：
    - 当集群启动时，所有节点都是Follower。
    - 如果Follower在选举超时时间内没有收到领导者的心跳消息，它会转换为候选人（Candidate）。
    - 候选人向其他所有节点发送请求投票（RequestVote）消息以请求选票。
    - 其他节点收到请求投票消息时，会根据自身状态和请求者的日志状态来决定是否投票给该候选人。

2. **日志复制（Log Replication）**：
    - 领导者接收客户端的请求并将其作为新的日志条目添加到自身的日志中。
    - 领导者将新的日志条目复制到所有的Follower中。
    - 当日志条目在多数节点上被复制并应用时，领导者会通知所有Follower该日志条目已提交。

3. **安全性（Safety）**：
    - Raft保证所有已提交的日志条目在所有节点上的顺序和内容都是一致的。
    - 如果一个新的领导者被选出，它的日志必须包含所有已提交的日志条目。

### 代码解释

代码中的`RequestVoteReply`结构体用于处理RequestVote RPC请求的响应。具体解释如下：

```go
// RequestVoteReply 是 RequestVoteHandler RPC 响应的结构体。
// 字段名称必须以大写字母开头，以便在序列化时正确处理。
type RequestVoteReply struct {
    // Term 是当前任期，用于候选人更新自身的任期。
    Term int // currentTerm, for candidate to update itself

    // VoteGranted 表示是否投票给了候选人，true 表示候选人获得了投票。
    VoteGranted bool // true means candidate received vote
}
```

- **Term**：这是当前节点的任期。每次进行新的选举时，任期会增加。如果一个候选人在请求投票时发现自己任期落后于请求的Term，它会更新自己的Term并转变为Follower状态。
  
- **VoteGranted**：这是一个布尔值，表示当前节点是否同意投票给请求的候选人。如果当前节点的日志比请求节点的新或当前节点已经投过票，它会拒绝投票请求。

这段代码是Raft协议中的一部分，负责在选举过程中处理投票请求的响应。通过这种方式，Raft协议能够保证在一个集群中最多只有一个领导者，并且所有的Follower都在跟随这个领导者的日志状态，从而实现一致性和容错。

### Raft协议的相关知识

- **选举超时（Election Timeout）**：这是Follower等待心跳消息的最大时间。如果在此时间内没有收到领导者的心跳消息，Follower会变成候选人。
- **心跳超时（Heartbeat Timeout）**：领导者定期发送心跳消息给Follower，以防止它们发起选举。
- **投票请求（RequestVote RPC）**：候选人用来请求其他节点投票给自己的RPC。
- **日志条目（Log Entry）**：客户端请求在日志中的表示。每个日志条目都有一个唯一的索引和任期号。
- **提交（Commit）**：当日志条目在多数节点上被复制后，领导者会认为该条目已经提交，并将其应用到状态机。



Raft 协议中的 term 是一个关键概念，用于帮助维护系统的一致性并处理不同节点之间的协调和领导者选举。下面是对 Raft 协议中 term 的详细解释：

### 什么是 Term

- **Term** 是一个单调递增的整数，用于标识 Raft 系统中的不同时间段。
- 每个 term 开始于一次领导者选举，可能会产生一个新的领导者，也可能不会（在没有领导者选出的情况下，term 会继续增加）。

### Term 的作用

1. **领导者选举**：term 用于区分不同的领导者选举周期。每次选举都会增加当前的 term，确保在新的选举周期中，所有节点都能明确区分当前的选举状态。

2. **日志一致性**：term 还用于日志条目的版本控制。每个日志条目都包含其创建时的 term 信息。这样，当节点之间进行日志同步时，可以根据 term 来决定哪一个日志条目是最新的，从而保持日志的一致性。

3. **防止旧领导者作乱**：当一个节点发现自己的 term 落后于其他节点时，会自动更新自己的 term 并切换到 Follower 状态。这可以防止旧领导者继续做出决定，确保系统不会被过时的信息干扰。

### Term 的操作

- **初始值**：每个节点启动时，term 初始化为 0。
- **更新 term**：节点在以下情况下更新自己的 term：
  - 收到来自其他节点的 RPC 请求或响应中包含的更高 term。
  - 进入新的选举周期，term 自增。
- **持久化 term**：每次更新 term 时，节点会将新的 term 持久化到存储中，以防节点重启后 term 信息丢失。

### Term 在 Raft 中的具体使用

- **领导者选举**：在发起选举时，候选人会增加自己的 term 并向其他节点发送请求投票（RequestVote）消息。如果一个节点收到的投票请求的 term 大于自己的 term，它会更新自己的 term 并投票给这个候选人。
- **日志复制**：领导者在发送追加日志条目（AppendEntries）消息时，会附带当前的 term。跟随者节点在收到消息后，如果发现消息中的 term 大于自己的 term，会更新自己的 term 并接受日志条目。

### 代码示例

以下是一个简单的代码示例，展示了 term 在 Raft 中的更新和使用：

```go
// RequestVoteArgs example RequestVoteHandler RPC arguments structure.
type RequestVoteArgs struct {
	Term         int // 候选人的任期
	CandidateId  int // 请求投票的候选人ID
	LastLogIndex int // 候选人最后日志条目的索引
	LastLogTerm  int // 候选人最后日志条目的任期
}

// RequestVoteReply example RequestVoteHandler RPC reply structure.
type RequestVoteReply struct {
	Term        int  // 当前节点的任期
	VoteGranted bool // 是否投票给候选人
}

// RequestVoteHandler example RequestVoteHandler RPC handler.
func (rf *Raft) RequestVoteHandler(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// 设置返回的任期
	reply.Term = rf.currentTerm

	// 如果候选人的任期大于当前任期，更新当前任期并转换为Follower
	if args.Term > rf.currentTerm {
		rf.ConvertToFollower(args.Term)
		rf.VoteMsgChan <- struct{}{}
	}

	// 如果候选人的任期小于当前任期，拒绝投票
	if args.Term < rf.currentTerm {
		return
	}

	// 判断候选人的日志是否至少和当前节点的日志一样新
	lastLogTerm := rf.getLastLogTerm()
	lastLogIndex := rf.getLastLogIndex()

    //在 Raft 协议中，判断一个候选人的日志是否至少和当前节点的日志一样新需要比较两个条件：
	//
	//候选人的最后日志条目的任期号（LastLogTerm）。
	//候选人的最后日志条目的索引号（LastLogIndex）。
	//具体的判断逻辑是：
	//
	//如果候选人的最后日志条目的任期号大于当前节点的最后日志条目的任期号，则候选人的日志更新。
	//如果候选人的最后日志条目的任期号等于当前节点的最后日志条目的任期号，则比较它们的日志条目索引号，候选人的日志条目索引号大，则候选人的日志更新。
	if (args.LastLogTerm > lastLogTerm) || (args.LastLogTerm == lastLogTerm && args.LastLogIndex >= lastLogIndex) {
		// 如果当前节点是Follower并且尚未投票或投票给了该候选人，则投票给该候选人
		if rf.role == Follower && (rf.votedFor == noVoted || rf.votedFor == args.CandidateId) {
			rf.votedFor = args.CandidateId
			reply.VoteGranted = true
			rf.VoteMsgChan <- struct{}{}
		}
	}
}

// 获取当前节点最后一个日志条目的任期号
func (rf *Raft) getLastLogTerm() int {
	if len(rf.log) == 0 {
		return 0
	}
	return rf.log[len(rf.log)-1].Term
}

// 获取当前节点最后一个日志条目的索引号
func (rf *Raft) getLastLogIndex() int {
	return len(rf.log)
}
```

通过上面的代码示例，可以看到 term 是如何在领导者选举和日志复制过程中发挥作用的。





## Part 2A: [leader election](http://nil.csail.mit.edu/6.824/2022/labs/guidance.html)（中等难度）

实现Raft领导者选举和心跳机制（仅包含没有日志条目的 AppendEntries RPCs）。第2A部分的目标是选出一个单一的领导者，如果没有故障发生，领导者应保持其领导地位；如果旧的领导者失败或 to/from 旧领导者的消息包丢失，则由新的领导者接替。运行 `go test -run 2A` 来测试你的第2A部分代码。

#### 提示

- 不能直接运行Raft实现，而应通过测试程序运行，即 `go test -run 2A`。**运行****`go test`****命令时，Go测试工具会查找并运行当前目录及其子目录中所有以** **`_test.go`** **结尾的文件中的测试函数。**
- 按照论文中的图2进行操作。此时需要关注发送和接收RequestVote RPC、与选举相关的服务器规则以及与领导者选举相关的状态。
- 将图2中领导者选举的状态添加到 `raft.go` 文件中的Raft结构中。同时需要定义一个结构体以保存每个日志条目的信息。
- 填写 `RequestVoteArgs` 和 `RequestVoteReply` 结构体。修改 `Make()` 函数以创建一个后台协程，在一段时间内没有从其他节点接收到消息时，通过发送RequestVote RPC来定期启动领导者选举。这样，如果已有领导者存在，节点将知道谁是领导者，或者节点本身将成为领导者。实现 `RequestVote()` RPC处理程序，以便服务器之间可以相互投票选出另一个领导者。
- 为了实现心跳机制，定义一个 `AppendEntries` RPC结构体（尽管可能不需要所有参数），并让领导者定期发送心跳消息。编写一个 `AppendEntries` RPC处理方法，以重置选举超时时间，这样在已有领导者被选出时，其他服务器不会竞选领导者。
- 确保不同节点的选举超时不会总是同时触发，否则所有节点将只为自己投票，导致无人当选为领导者。（原因：所有节点会在同一时间成为候选者，并向其他节点发送RequestVote RPC。这将导致每个节点只为自己投票，最终导致选举冲突，无法选出领导者。）
- 测试程序要求**领导者发送心跳****RPC****的频率不超过每秒10次**。
- 测试程序要求在旧领导者失效后五秒内选出新领导者（如果大多数节点仍能通信）。然而，选举可能需要多轮投票以应对票数平分的情况（例如，数据包丢失或候选者选择了相同的随机退避时间）。需要选择足够短的选举超时时间（及心跳间隔），以确保选举在不到五秒内完成，即使需要多轮。
- 论文第5.2节提到的选举超时时间在150到300毫秒之间。这一范围仅在领导者发送心跳频率远高于每150毫秒一次时才合理。由于测试程序限制心跳频率为每秒10次，选举超时需要比论文的150到300毫秒更长，但不能太长，以避免在五秒内未能选出领导者。
- 可以使用Go的 `rand` （[链接](https://pkg.go.dev/math/rand)）包。
- 需要编写代码以定期或在延迟一段时间后执行操作。最简单的方法是在一个循环中调用 `time.Sleep()` 的协程（参见 `Make()` 创建的 `ticker()` 协程）。不要使用Go的 `time.Timer` 或 `time.Ticker`，因为它们难以正确地使用。
- 参考[指导页面](http://nil.csail.mit.edu/6.824/2022/labs/guidance.html)中的开发和调试代码的提示。如果代码难以通过测试，请再次阅读论文的图2；领导者选举的完整逻辑分布在图的多个部分。
- **不要忘记实现** **`GetState()`**。
- 测试程序在永久关闭实例时会调用 `rf.Kill()`。可以通过 `rf.killed()` 检查 `Kill()` 是否已被调用。建议在所有循环中执行此检查，以避免已关闭的Raft实例打印混乱的消息。
- Go RPC只发送字段名以大写字母开头的结构体字段。子结构体也必须有大写字母开头的字段名（例如，数组中的日志记录字段）。`labgob` 包会对此发出警告，请不要忽略这些警告。

确保在提交第 2A 部分之前通过 2A 测试，这样你会看到类似这样的输出：

```Bash
$ go test -run 2A
Test (2A): initial election ...
  ... Passed --   3.5  3   58   16840    0
Test (2A): election after network failure ...
  ... Passed --   5.4  3  118   25269    0
Test (2A): multiple elections ...
  ... Passed --   7.3  7  624  138014    0
PASS
ok          6.824/raft        16.265s
$
```

每一行“Passed”包含五个数字；这些数字分别是测试所用的时间（以秒为单位）、Raft 节点的数量、测试期间发送的 RPC 数量、RPC 消息的总字节数以及 Raft 报告已提交的日志条目的数量。每个人的数字可能会与这里显示的不同。可以忽略这些数字，但它们可能有助于检查自己的实现发送的 RPC 数量是否合理。对于所有的第 2、3 和 4 部分，如果所有测试（使用 `go test`）耗时超过 600 秒，或者任何单个测试耗时超过 120 秒，评分脚本将判定你的解决方案不合格。

在评分时，将运行没有 `-race` 标志的测试，但应该确保代码在带有 `-race` 标志的情况下也能稳定地通过测试。

#### 错误：

1. 测试时间超时

```Bash
panic: test timed out after 10m0s
running tests:
        TestBatch2A (9m29s)

goroutine 99721 [running]:
testing.(*M).startAlarm.func1()
        /usr/local/go/src/testing/testing.go:2259 +0x3b9
created by time.goFunc
        /usr/local/go/src/time/sleep.go:176 +0x2d
```

解决：

```Bash
go test -run 2A -timeout 60m
```

#### 实现细节

1. ##### 结构体

```Go
// ApplyMsg
// 当每个Raft节点意识到连续的日志条目被提交时，该节点应该通过传递给 Make() 的 applyCh 向同一服务器上的服务（或测试器）发送一个ApplyMsg。
// 将 CommandValid 设置为true以表明 ApplyMsg 包含一个新提交的日志条目。
type ApplyMsg struct {
   CommandValid bool        // 表示Command字段是否包含一个新提交的日志条目
   Command      interface{} // 包含新提交的日志条目
   CommandIndex int         // 新提交日志条目的索引
}

// Raft
// 一个实现单个 Raft 节点的 Go 对象示例
type Raft struct {
   mu        sync.Mutex          // 保护该节点状态的共享访问的锁 Lock to protect shared access to this peer's state
   peers     []*labrpc.ClientEnd // 所有节点的 RPC 端点 RPC end points of all peers
   persister *Persister          // 保存节点持久化状态的对象 Object to hold this peer's persisted state
   me        int                 // 当前节点在 peers 数组中的索引 this peer's index into peers[]
   dead      int32               // 通过 Kill() 设置，用于标记节点是否已停止 set by Kill()

   // 2A
   role              RoleType                // 记录节点目前状态
   currentTerm       int                     // 节点当前任期
   votedFor          int                     // follower把票投给了哪个candidate
   voteCount         int                     // 记录所获选票的个数
   appendEntriesChan chan AppendEntriesReply // 心跳channel
   LeaderMsgChan     chan struct{}           // 当选Leader时发送
   VoteMsgChan       chan struct{}           // 收到选举信号时重置一下计时器，不然会出现覆盖term后计时器超时又突然自增。
}

// AppendEntriesArgs 是附加日志条目（包括心跳）的 RPC 请求参数结构。
type AppendEntriesArgs struct {
   Term         int // 领导者的当前任期
   LeaderId     int // 领导者的 ID
   PrevLogIndex int // 新日志条目之前的日志条目的索引
   PrevLogTerm  int // 新日志条目之前的日志条目的任期
   LeaderCommit int // 领导者的 commitIndex
}

// AppendEntriesReply 是附加日志条目（包括心跳）的 RPC 响应结构。
type AppendEntriesReply struct {
   Term    int  // 当前任期，用于领导者更新自己的任期
   Success bool // 如果跟随者包含了匹配的日志条目且日志条目已成功存储，则为 true
}

// RequestVoteArgs 是 RequestVote RPC 请求的参数结构体。
// 字段名称必须以大写字母开头，以便在序列化时正确处理。
type RequestVoteArgs struct {
   Term         int // 候选人的当前任期
   CandidateId  int // 请求投票的候选人的 ID
   LastLogIndex int // 候选人最后一个日志条目的索引(5.4节)
   LastLogTerm  int // 候选人最后一个日志条目的任期(5.4节)
}

// RequestVoteReply 是 RequestVote RPC 响应的结构体。
// 字段名称必须以大写字母开头，以便在序列化时正确处理。
type RequestVoteReply struct {
   Term        int  // 当前任期，用于候选人更新自身的任期
   VoteGranted bool // 表示是否投票给了候选人，true 表示候选人获得了投票。
}
```

1. ##### GetState()获取节点任期和角色

```Go
// GetState 返回当前的任期和该服务器是否认为自己是领导者。
// 该方法用于外部查询 Raft 服务器的当前状态。
// 返回值：
//   - int：当前的任期（term）。
//   - bool：该服务器是否认为自己是领导者（isleader）。
func (rf *Raft) GetState() (int, bool) {
   var term int
   var isleader bool
   // Your code here (2A).
   term = rf.currentTerm
   isleader = rf.role == Leader
   return term, isleader
}
```

1. ##### sendAllRequestVote()发起投票

```Go
// sendAllRequestVote 向所有其他 Raft 节点发送请求投票的 RPC。
// 该函数首先构建 RequestVoteArgs，然后向所有其他节点并行发送请求投票的 RPC。
func (rf *Raft) sendAllRequestVote() {
   rf.mu.Lock()
   defer rf.mu.Unlock()

   // 构建请求投票的参数
   args := &RequestVoteArgs{
      Term:         rf.currentTerm, // 当前任期
      CandidateId:  rf.me,          // 候选人 ID
      LastLogIndex: 0,              // 候选人最后一个日志条目的索引（暂时设置为 0）
      LastLogTerm:  0,              // 候选人最后一个日志条目的任期（暂时设置为 0）
   }

   // 向所有其他节点发送请求投票的 RPC
   for i := range rf.peers {
      // 对于非当前节点的其他节点发送，并且本身为候选者状态
      if i != rf.me && rf.role == Candidate {
         // 并行发送请求投票的 RPC
         go func(id int) {
            // 接收返回参数
            ret := &RequestVoteReply{
               Term:        0,
               VoteGranted: false,
            }
            rf.sendRequestVote(id, args, ret)
         }(i)
      }
   }
}
```

1. ##### RequestVote() 处理RequestVote RPC请求

```Go
// RequestVote RPC handler.
// 处理RequestVote RPC请求的处理函数
//任期比较：
// 如果请求者的任期大于当前节点的任期，说明请求者的信息更新，当前节点需要更新其任期并转换为Follower角色。
// 如果请求者的任期小于当前节点的任期，则当前节点拒绝投票，因为其任期更大，更“新”。
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
   // Your code here (2A, 2B).
   // 锁定当前Raft实例，以保证并发安全。
   rf.mu.Lock()
   defer rf.mu.Unlock()

   // 设置返回的任期，投票默认拒绝 返回 false
   reply.Term = rf.currentTerm

   // 如果请求者的任期大于当前节点的任期，则更新当前节点的任期并转换为Follower角色。
   // 这里不加return 因为一个candidate一轮只发送一次选举。Follower收到了修改自己任期即可。后面可以继续参与投票。
   if args.Term > rf.currentTerm {
      // 当前节点的任期比请求节点（候选者）的任期小，则转为追随者
      // 将当前节点转换为Follower角色，并更新任期为请求者的任期。
      rf.ConvertToFollower(args.Term)
      // 向VoteMsgChan通道发送消息，通知其他部分有投票事件发生。
      rf.VoteMsgChan <- struct{}{}
   }

   // 如果请求者的任期小于当前节点的任期，或者在同一任期内已经投给了其他候选人，则直接拒绝投票并返回当前任期与投票结果。
   if args.Term < rf.currentTerm || (args.Term == rf.currentTerm && rf.votedFor != -1 && rf.votedFor != args.CandidateId) {
      reply.Term, reply.VoteGranted = rf.currentTerm, false
      return
   }

   // 如果当前节点是Follower，并且当前节点尚未投票或已投票给该候选人，
   // todo: 并且候选人的日志至少和当前节点的日志一样新，则同意投票。(2B的实现中还需要加入对日志的比较（根据论文的5.4节）)
   if rf.role == Follower && (rf.votedFor == noVoted || rf.votedFor == args.CandidateId) {
      // 更新投票给的候选人ID。
      rf.votedFor = args.CandidateId
      // 同意投票。
      reply.VoteGranted = true
      reply.Term = args.Term
      // 向VoteMsgChan通道发送消息，通知其他部分有投票事件发生。
      rf.VoteMsgChan <- struct{}{}
   }
}
```

1. ##### sendRequestVote()发送投票请求

```Go
// sendRequestVote 发送 RequestVote RPC 给服务器的示例代码。
// server 是目标服务器在 rf.peers[] 中的索引。
// args 中包含 RPC 参数。
// *reply 中填充 RPC 回复，因此调用者应传递 &reply。
// 传递给 Call() 的 args 和 reply 类型必须与处理函数中声明的参数类型相同
// （包括它们是否是指针）。
//
// labrpc 包模拟了一个丢包网络，其中服务器可能无法访问，
// 请求和回复可能会丢失。Call() 发送一个请求并等待回复。
// 如果在超时间隔内收到回复，Call() 返回 true；否则返回 false。
// 因此 Call() 可能不会立即返回。
// 返回 false 可能是由服务器宕机、服务器无法访问、请求丢失或回复丢失引起的。
//
// Call() 保证会返回（可能会有延迟），*除非*服务器端的处理函数没有返回。
// 因此，不需要在 Call() 周围实现你自己的超时机制。
//
// 查看 ../labrpc/labrpc.go 中的注释以了解更多详情。
//
// 如果你在让 RPC 工作时遇到困难，请检查你是否将传递给 RPC 的结构体中的所有字段名都大写，
// 并且调用者传递的是 reply 结构体的地址（&），而不是结构体本身。
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
   // server下标节点调用RequestVote RPC处理程序
   ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
   // 发送失败直接返回
   if !ok {
      return false
   }

   // 如果该节点已经不是候选者或者该节点请求时的任期与当前任期不一致，直接返回 true，无须继续拉票
   if rf.role != Candidate || args.Term != rf.currentTerm {
      // 已经成为 Leader 或者重置为候选者或跟随者了
      return true
   }

   // 如果收到的回复中的任期比当前节点的任期大，遇到了任期比自己大的节点，转换为跟随者 follower
   if reply.Term > rf.currentTerm {
      rf.ConvertToFollower(reply.Term)
      rf.VoteMsgChan <- struct{}{}
      return true
   }

   // 如果投票被授予，并且当前节点仍是候选者，增加投票计数
   if reply.VoteGranted && rf.role == Candidate {
      rf.voteCount++
      // 如果获得超过半数的票数，并且仍是候选者，转换为领导者
      if 2*rf.voteCount > len(rf.peers) && rf.role == Candidate {
         rf.ConvertToLeader()
         // 超半数票 直接当选，当选为领导者后，通知 LeaderMsgChan
         rf.LeaderMsgChan <- struct{}{}
      }
   }
   return true
}
```

1. ##### Killed()检查goroutine是否已经死亡

```Go
// Kill 测试器在每次测试后不会停止 Raft 创建的 goroutine，
// 但它确实会调用 Kill() 方法。你的代码可以使用 killed() 来
// 检查是否已调用 Kill()。使用 atomic 避免了对锁的需求。
//
// 问题在于长时间运行的 goroutine 会使用内存并可能消耗 CPU 时间，
// 这可能导致后续测试失败并生成令人困惑的调试输出。
// 任何具有长时间运行循环的 goroutine 都应该调用 killed() 以检查它是否应该停止。
// Kill 将当前 Raft 实例标记为已终止。
func (rf *Raft) Kill() {
   // 使用 atomic.StoreInt32(&rf.dead, 1) 设置 rf.dead 的值为 1。
   atomic.StoreInt32(&rf.dead, 1)
   // Your code here, if desired.
}

// killed 检查当前 Raft 实例是否已被终止。
// 使用 atomic.LoadInt32(&rf.dead) 来获取 rf.dead 的值。
// 如果值为 1，则表示该实例已被终止。
func (rf *Raft) killed() bool {
   z := atomic.LoadInt32(&rf.dead)
   return z == 1
}
```

1. ##### ticker()启动选举

**通过 TestManyElections2A() 测试的关键点在于候选者收到更大任期的****leader****的心跳信息或者日志复制信息后，需要转为****follower****。**

```Go
// ticker 协程在近期没有收到心跳的情况下启动新的选举。
func (rf *Raft) ticker() {
   // dead置为1后为true，则退出运行
   for rf.killed() == false {

      // Your code here to check if a leader election should
      // be started and to randomize sleeping time using
      // time.Sleep().
      // 在这里添加代码以检查是否应该启动领导者选举
      // 并使用 time.Sleep() 随机化休眠时间。
      switch rf.role {
      case Candidate:
         go rf.sendAllRequestVote()
         select {
         case <-rf.VoteMsgChan:
            continue
         case resp := <-rf.appendEntriesChan:
            if resp.Term >= rf.currentTerm {
               // 关键点：候选者收到更大任期的leader的心跳信息或者日志复制信息后，需要转为follower
               rf.ConvertToFollower(resp.Term)
               continue
            }
         case <-time.After(electionTimeout + time.Duration(rand.Int31()%300)*time.Millisecond):
            // 选举超时 重置选举状态
            rf.ConvertToCandidate()
            continue
         case <-rf.LeaderMsgChan:
         }
      case Leader:
         // Leader 定期发送心跳和同步日志
         rf.SendAllAppendEntries()
         select {
         case resp := <-rf.appendEntriesChan:
            // 处理跟随者的响应，如发现更高的任期则转为Follower
            if resp.Term > rf.currentTerm {
               rf.ConvertToFollower(resp.Term)
               continue
            }
         case <-time.After(HeartBeatInterval):
            // 超时后继续发送心跳
            continue
         }
         // 等待心跳间隔时间
         //time.Sleep(HeartBeatInterval)
      case Follower:
         // 如果是跟随者，等待不同的事件发生
         select {
         case <-rf.VoteMsgChan:
            // 收到投票消息，继续等待
            continue
         case resp := <-rf.appendEntriesChan:
            // 收到附加日志条目消息，继续等待
            if resp.Term > rf.currentTerm {
               rf.ConvertToFollower(resp.Term)
               continue
            }
         case <-time.After(appendEntriesTimeout + time.Duration(rand.Int31()%300)*time.Millisecond):
            // 附加日志条目超时，转换为候选人，发起选举
            // 增加扰动避免多个Candidate同时进入选举
            rf.ConvertToCandidate()
         }
      }
   }
}
```

1. ##### Make() 创建服务

```Go
// Make 创建服务或测试者想要创建的一个 Raft 服务器。
// 所有 Raft 服务器的端口（包括这个）都在 peers[] 数组中。
// 这个服务器的端口是 peers[me]。所有服务器的 peers[] 数组顺序相同。
// persister 是这个服务器保存其持久状态的地方，并且最初持有最近保存的状态（如果有的话）。
// applyCh 是一个通道，测试者或服务期望 Raft 在此通道上发送 ApplyMsg 消息。
// Make() 必须快速返回，因此它应该为任何长期运行的工作启动协程。
func Make(peers []*labrpc.ClientEnd, me int, persister *Persister, applyCh chan ApplyMsg) *Raft {
   rf := &Raft{
      mu:        sync.Mutex{}, // 互斥锁，保护共享访问这个节点的状态
      peers:     peers,        // 所有节点的 RPC 端点
      persister: persister,    // 持久化对象，用于保存这个节点的持久状态
      me:        me,           // 这个节点在 peers[] 数组中的索引
      dead:      0,            // 用于标记节点是否已终止
   }

   // 2A
   rf.role = Follower                                   // 初始状态为 Follower
   rf.currentTerm = 0                                   // 当前任期
   rf.votedFor = noVoted                                // 当前任期内投票的候选人 ID
   rf.voteCount = 0                                     // 当前选举中的投票计数
   rf.appendEntriesChan = make(chan AppendEntriesReply) // 用于心跳信号的通道
   rf.LeaderMsgChan = make(chan struct{}, chanLen)      // 用于领导者选举信号的通道
   rf.VoteMsgChan = make(chan struct{}, chanLen)        // 用于投票信号的通道

   // initialize from state persisted before a crash
   // 从崩溃前保存的状态进行初始化
   rf.readPersist(persister.ReadRaftState())

   // start ticker goroutine to start elections
   // 启动 ticker 协程开始选举
   go rf.ticker()

   return rf
}
```

1. ##### SendAllAppendEntries()发送心跳或者复制日志

```Go
// SendAllAppendEntries 由Leader向其他所有节点调用来复制日志条目;也用作heartbeat
// 用于领导者向其他所有节点发送附加日志条目（或心跳）请求。在领导者周期性地发送心跳或需要复制日志条目到所有节点时使用
func (rf *Raft) SendAllAppendEntries() {
   rf.mu.Lock()
   defer rf.mu.Unlock()

   for server := range rf.peers {
      // 对于每个不是当前节点的节点，leader 启动一个新的 goroutine 来发送 AppendEntries 请求
      if server != rf.me && rf.role == Leader {
         go func(id int) {
            args := &AppendEntriesArgs{
               Term:         rf.currentTerm,
               LeaderId:     rf.me,
               PrevLogIndex: 0,
               PrevLogTerm:  0,
               LeaderCommit: 0,
            }
            reply := &AppendEntriesReply{
               Term:    0,
               Success: false,
            }
            rf.SendAppendEntries(id, args, reply)
         }(server)
      }
   }
}
```

1. ##### SendAppendEntries() 

```Go
// SendAppendEntries 向指定的节点发送 AppendEntries RPC 请求。
// 发送具体的 AppendEntries 请求，并处理响应
func (rf *Raft) SendAppendEntries(id int, args *AppendEntriesArgs, reply *AppendEntriesReply) {
   // 调用指定节点的 AppendEntriesHandler 方法，并传递请求和响应结构
   rf.peers[id].Call("Raft.AppendEntriesHandler", args, reply)

   // 如果当前节点不再是领导者，则直接返回
   if rf.role != Leader {
      return
   }

   // 如果响应中的任期大于当前任期，当前节点会转换为跟随者
   if reply.Term > rf.currentTerm {
      rf.ConvertToFollower(reply.Term)
      return
   }
}
```

1. ##### AppendEntriesHandler()处理心跳或日志信息

```Go
// AppendEntriesHandler 由Leader向每个其余节点发送
// 处理来自领导者的 AppendEntries RPC 请求
// 处理接收到的 AppendEntries 请求，包括心跳和日志条目的复制
func (rf *Raft) AppendEntriesHandler(args *AppendEntriesArgs, reply *AppendEntriesReply) {
   // 传一个空结构体表示接收到了Leader的请求。
   // 初始化响应的任期为当前任期
   reply.Term = rf.currentTerm

   // 收到Leader更高的任期时，更新自己的任期，转为 leader 的追随者
   if rf.currentTerm < args.Term {
      rf.ConvertToFollower(args.Term)
      return
   }

   // 向 appendEntriesChan 发送一个空结构体，以表示接收到了领导者的请求
   //rf.appendEntriesChan <- struct {}{}
   // 发送心跳或日志条目后
   rf.appendEntriesChan <- AppendEntriesReply{Term: rf.currentTerm, Success: true}

}
```



## Part 2B: log (困难)

实现领导者和跟随者的代码以附加新的日志条目，以便通过 `go test -run 2B` 测试。

#### 提示：

- 首要目标应该是通过 `TestBasicAgree2B()`。首先实现 `Start()` 方法，然后编写代码，通过 `AppendEntries` RPC 发送和接收新的日志条目，按照论文中的图 2 实现。在每个节点上将每个新提交的条目发送到 `applyCh`。
- 需要实现选举限制（论文的第 5.4.1 节）。
- 在早期的 Lab 2B 测试中，可能无法达成一致的一个原因是，即使领导者还在，仍然进行反复选举。检查选举计时器管理中的错误，或者检查在赢得选举后有没有立即发送心跳。
- 你的代码可能会有循环去反复检查某些事件。不要让这些循环在没有暂停的情况下连续执行，因为这会减慢你的实现速度，导致测试失败。使用 Go 的[条件变量](https://pkg.go.dev/sync#Cond)，或者在每次循环迭代中插入 `time.Sleep(10 * time.Millisecond)`。
- 为了将来更好地进行实验，请编写（或重写）干净且清晰的代码。可以参考[指导页面](http://nil.csail.mit.edu/6.824/2022/labs/guidance.html)，获取开发和调试代码的提示。
- 如果测试失败，查看 `config.go` 和 `test_test.go` 中的测试代码，以更好地理解测试的内容。`config.go` 还展示了测试者如何使用 Raft API。

如果你的代码运行得太慢，你的代码可能在之后的实验的测试中无法通过。可以使用 time 命令检查解决方案使用了多少实时时间和CPU时间。以下是典型的输出：

```Bash
$ time go test -run 2B
Test (2B): basic agreement ...
  ... Passed --   0.9  3   16    4572    3
Test (2B): RPC byte count ...
  ... Passed --   1.7  3   48  114536   11
Test (2B): agreement after follower reconnects ...
  ... Passed --   3.6  3   78   22131    7
Test (2B): no agreement if too many followers disconnect ...
  ... Passed --   3.8  5  172   40935    3
Test (2B): concurrent Start()s ...
  ... Passed --   1.1  3   24    7379    6
Test (2B): rejoin of partitioned leader ...
  ... Passed --   5.1  3  152   37021    4
Test (2B): leader backs up quickly over incorrect follower logs ...
  ... Passed --  17.2  5 2080 1587388  102
Test (2B): RPC counts aren't too high ...
  ... Passed --   2.2  3   60   20119   12
PASS
ok          6.824/raft        35.557s

real        0m35.899s
user        0m2.556s
sys        0m1.458s
$
```

“ok 6.824/raft 35.557s”意味着Go测量到2B测试所需的时间为实际时间（挂钟）的35.557秒。“user 0m2.556s”意味着代码消耗了2.556秒的CPU时间，即实际执行指令所花费的时间（而不是等待或睡眠）。如果你的解决方案在2B测试中使用的实时时间远远超过一分钟，或者CPU时间远远超过5秒，以后可能会遇到问题。请查找睡眠或等待RPC超时所花费的时间、在没有睡眠或等待条件或通道消息的情况下运行的循环，或者发送的大量RPC。

#### 实现：

##### 1、commitIndex 和 lastApplied

在Raft分布式一致性算法中，commitIndex 和 lastApplied 是两个非常重要的状态变量，它们分别追踪不同的进度指标，但都与日志的提交和应用紧密相关。下面是这两个变量关系的简要说明：

- commitIndex: 此变量记录了**已知被集群中大多数节点复制的最高日志条目的索引**。换句话说，它是Raft**领导者可以安全地认为已经被持久化并且可以被所有追随者接受的日志条目的最高索引**。当领导者收到足够多的追随者对某个日志条目的确认（通过心跳或AppendEntries RPC响应），它就会更新commitIndex。这个值的更新是领导者决定哪些日志可以被提交（即应用到状态机）的基础。
- lastApplied: 此变量**记录了状态机实际应用的最高日志条目的索引**。这意味着，**索引小于或等于lastApplied的日志条目，其包含的命令已经被执行并且影响到了服务的状态**。lastApplied在Raft节点作为追随者或领导者时都会更新，**每当有新的日志条目被安全地提交（即commitIndex前进），Raft节点就会逐步将这些已提交的日志应用到状态机上，同时更新lastApplied**。

两者之间的关系：

lastApplied总是小于等于commitIndex。这是因为只有当一个日志条目被提交（即其索引大于等于commitIndex），它才能被应用到状态机上，从而更新lastApplied。

**在正常操作下，每当有新的日志条目被提交（即commitIndex增加），Raft节点随后会逐步将这些已提交的日志应用到状态机，导致lastApplied逐渐追赶commitIndex。但在某些情况下，比如刚成为领导者或处理日志应用的延迟，lastApplied可能会暂时落后于commitIndex。**

维护这两个变量的正确性是确保系统一致性和容错性的关键。commitIndex确保了安全性，即不会丢失已提交的日志；而lastApplied确保了系统状态的一致更新，即所有已提交的更改最终都会反映在服务状态中。

##### 2、nextIndex 和 matchIndex

在Raft分布式一致性算法中，nextIndex 和 matchIndex 是两个核心状态变量，它们维护在每个Raft节点（尤其是领导者节点）中，用以管理与其它节点（跟随者节点）间日志复制的过程。这两个索引反映了领导者如何高效且正确地推进日志同步，确保整个集群的日志一致性。下面是它们之间关系的详细说明：

**nextIndex:**

定义: 对于每一个跟随者，nextIndex[i] 存储的是领导者计划发送给跟随者i的下一个日志条目的索引。初始化时，它通常被设置为领导者当前日志的最后一条日志的索引加1，意味着领导者将从跟随者尚未有的第一条日志开始发送。

作用: 该变量控制了领导者向特定跟随者发送日志的起点，是解决日志不一致性和优化日志复制效率的关键。当跟随者的日志与领导者不一致时，领导者会根据跟随者的反馈适当减少nextIndex值，以找到两方日志中共同的部分，从而避免日志冲突。

**matchIndex:**

定义: 同样针对每个跟随者，matchIndex[i] 记录了领导者已知的、在跟随者i上已成功复制的最高日志条目的索引。初始化时为0，随着日志复制过程的进行，这个值会单调递增。

作用: 它反映了跟随者与领导者日志同步的最远进度。领导者使用matchIndex来决定何时可以安全地提升commitIndex，即当大多数跟随者的matchIndex满足条件时，领导者可以提交日志。此外，matchIndex也用于领导者计算多数派，以决定哪些日志条目可以被提交。

两者之间的关系:

协调复制: 在日志复制过程中，nextIndex指示发送的起始点，而matchIndex记录了已确认的终点。领导者通过比较跟随者的回复（如AppendEntries RPC的响应）来动态调整nextIndex，以解决日志不一致问题。一旦跟随者确认了一组日志条目，领导者就会更新该跟随者的matchIndex，表示这些日志已被安全复制。

同步进展: 当nextIndex与matchIndex之间的差距缩小，意味着跟随者正在逐步赶上领导者，日志同步在进行中。反之，如果差距增大或不变，可能表示存在日志不一致或其他通信问题。

安全性保证: 通过这两个索引的协同工作，Raft确保了日志的一致性，防止了旧日志覆盖新日志的情况，同时也保证了提交的日志在大多数节点上都是持久化的，这是Raft算法实现数据一致性的基础。

##### 3、ConvertToLeader时重置nextIndex

在Raft算法中，当一个节点转换为领导者（Leader）角色时，确实会将其nextIndex数组中的所有项初始化为其本地日志的最后一条日志的索引加1。这样做基于以下几点考虑和机制设计：

初始化策略：这是一种保守的初始化策略。虽然看起来直接将nextIndex设置为领导者日志的末尾可能导致与一些跟随者（Follower）的日志不匹配，但这是为了快速探测到不一致并作出相应调整。如果跟随者的日志实际上没有达到领导者日志的长度，后续的AppendEntries请求会因为日志不匹配而失败（因为跟随者的日志索引处的日志任期号与领导者尝试发送的日志任期号不一致），领导者会相应地减小nextIndex值，直到找到匹配点。

探测不一致：通过这样的初始化，领导者能够快速发现并解决日志不一致的问题。当领导者首次尝试发送日志时，如果跟随者的日志不包含领导者尝试发送的日志条目，跟随者会回复一个拒绝消息，告知领导者其日志的最后一条条目的任期号。领导者收到这类反馈后，会相应降低对该跟随者的nextIndex，然后尝试发送一个较旧的日志条目，直至找到两个节点日志中匹配的部分。

效率与一致性权衡：这种做法在一定程度上牺牲了初次同步的效率，因为可能会导致跟随者频繁拒绝领导者的消息，但从长期看，它能快速收敛到一致状态，并确保日志的一致性。相比从一个较低的索引开始逐步试探，直接设置较高的nextIndex可以更快地在大多数情况下找到正确的匹配点，尤其是在跟随者日志相对落后不多的情况下。

安全性：尽管直接将nextIndex设置为最大值可能导致短期内的RPC失败，但Raft算法的设计确保了即使在这种情况下，也不会破坏系统的安全性。领导者始终只会在其当前任期的日志条目上进行投票，而且只有当大多数节点确认了日志条目，才会提交日志，这保证了日志的一致性和正确性。

综上所述，虽然这种初始化方式可能会在转换为领导者后立即遇到一些日志不匹配的错误，但它是一种快速探测并解决日志不一致性的策略，且在Raft算法的框架内，通过后续的调整和重试机制，能够确保集群最终达到一致状态。

##### 4、处理日志复制信息 AppendEntriesHandler

```Go
func (rf *Raft) AppendEntriesHandler(args *AppendEntriesArgs, reply *AppendEntriesReply) {
   rf.mu.Lock()
   defer rf.mu.Unlock()

   // 传一个带有当前任期的结构体表示接收到了Leader的请求。
   // 初始化响应的任期为当前任期
   reply.Term = rf.currentTerm

   // 老Leader重连后Follower不接受旧信号
   if rf.currentTerm > args.Term {
      return
   }

   // 收到Leader更高的任期时，更新自己的任期，转为 leader 的追随者
   // 或者如果当前节点是候选者，则更新自己的任期，转为追随者
   if rf.currentTerm < args.Term || (rf.currentTerm == args.Term && rf.role == Candidate) {
      rf.ConvertToFollower(args.Term)
   }

   // 向 appendEntriesChan 发送一个空结构体，以表示接收到了领导者的请求
   //rf.appendEntriesChan <- struct {}{}
   // 发送心跳重置计时器或日志条目后
   rf.appendEntriesChan <- AppendEntriesReply{Term: rf.currentTerm, Success: true}
   if args.PrevLogIndex >= len(rf.logs) {
      return
   }
   lastLog := rf.logs[args.PrevLogIndex]
   // 最后的日志对不上 因此需要让Leader对该节点的nextIndex - 1
   if args.PrevLogTerm != lastLog.Term {
      return
   }
   reply.Success = true
   //领导者尝试让跟随者追加的日志条目范围完全落在跟随者已知的已提交日志区间内，那就不需要再复制了
   if args.PrevLogIndex+len(args.Entries) <= rf.commitIndex {
      return
   }
   // 在PrevLogIndex处开始复制一份日志
   rf.logs = append(rf.logs[:args.PrevLogIndex+1], args.Entries...)
   // 更新commitIndex
   rf.commitIndex = min(len(rf.logs)-1, args.LeaderCommit)
}
```

**（1）**

```Go
if args.PrevLogIndex >= len(rf.logs) {
      return
   }
```

在Raft算法中处理AppendEntries RPC请求的逻辑里，它用来检查跟随者（Follower）接收到的来自领导者（Leader）的心跳或日志复制请求中的前一个日志条目索引（PrevLogIndex）是否合理。具体分析如下：

**参数解释:**

args.PrevLogIndex: 旧的日志条目的索引，是上次同步的最后一条日志的索引。这是领导者在发送AppendEntries请求时携带的参数，表示跟随者应该在其日志中查找的前一个日志条目的索引，以便进行日志匹配和连续性检查。领导者期望在这个索引位置的日志条目与它正尝试追加的日志条目之前是连续的。

len(rf.logs): 表示当前跟随者（处理这个RPC请求的节点）日志的条目数量。如果跟随者的日志为空，则这个值为0。

**检查目的:**

该检查是为了确保领导者请求的前一个日志条目索引没有超过跟随者当前日志的实际长度。如果args.PrevLogIndex大于跟随者日志的长度，这意味着领导者认为跟随者应该有一个比实际更长的日志，这通常是因为领导者和跟随者之间的日志出现了不一致，或者跟随者落后很多且领导者的信息过时。

**返回处理:**

当检测到args.PrevLogIndex > len(rf.logs)时，跟随者直接返回，不进一步处理这次AppendEntries请求。这是因为在这种情况下，继续处理没有意义：按照领导者提供的索引，跟随者找不到匹配的日志条目来验证接下来的日志条目是否可以正确追加。此时，跟随者通常会在响应中设置success = false，并可能通过ConflictIndex或ConflictTerm等字段告知领导者具体的不匹配信息。

**后续动作:**

领导者收到这样的失败响应后，会根据跟随者的反馈调整其nextIndex值，然后重试发送AppendEntries请求，从一个更早的索引开始，以解决日志不一致的问题。这样，通过一系列的尝试与调整，Raft算法能够最终确保集群间日志的一致性。

因此，这一检查是Raft算法中处理日志不一致和确保日志复制正确性的重要环节，有助于维护分布式系统中数据的一致性和完整性。

**（2）**在Raft算法中，领导者（Leader）通过AppendEntries RPC向跟随者（Follower）发送心跳或日志条目时，会携带LeaderCommit字段，指示领导者已知的提交索引，即集群中大多数节点已经持久化的日志条目的最高索引。跟随者接收到这个信息后，会更新其本地的commitIndex，以反映集群的最新提交状态，而这个更新操作通常采用如下形式：

```Go
rf.commitIndex = min(len(rf.logs) - 1, args.LeaderCommit)
```

这里使用min()函数取最小值的原因在于确保两个关键条件得到满足：

不超出本地日志范围：len(rf.logs) - 1代表跟随者当前日志的最后一个索引。通过比较，确保commitIndex不会超过跟随者实际拥有的日志条目范围。这是因为跟随者不能提交不存在于其日志中的条目，即使领导者认为那些条目已被集群大多数成员提交。这是对日志完整性的保护，避免了“未来日志条目”的错误提交。

保持一致性：args.LeaderCommit是领导者告知的已提交日志的最高索引。跟随者需要确保自己的commitIndex至少达到这个值，以保证整个集群的一致性。如果跟随者的日志足够新，能够包含领导者所提交的所有日志条目，那么跟随者的commitIndex就应该更新为args.LeaderCommit，以反映集群的最新一致状态。

综合以上两点，min()函数在这里的作用是平衡了跟随者日志的实际情况与集群的提交进度，既避免了因领导者信息超前而导致的日志不匹配问题，又确保了跟随者能够尽可能地跟上集群的提交状态，保证了Raft算法的一致性原则。

**（3）**

```Go
if args.PrevLogIndex + len(args.Entries) <= rf.commitIndex {
        return
}
```

这段代码出现在Raft算法中跟随者（Follower）节点处理来自领导者（Leader）的AppendEntries RPC请求的逻辑中。它的目的是为了确保领导者尝试提交的日志条目区间不会与跟随者当前已知的提交日志索引冲突或逆序。具体分析如下：

参数解释:

args.PrevLogIndex: 这是领导者在AppendEntries请求中携带的参数，表示跟随者日志中紧邻新日志条目之前的一个条目的索引。领导者期望跟随者在此索引位置的日志条目之后追加新的条目。

len(args.Entries): 表示领导者尝试追加到跟随者日志中的日志条目数量。

rf.commitIndex: 跟随者当前已知的提交日志的最高索引，即跟随者已经持久化并被集群中大多数节点确认的日志条目。

逻辑解释:

当args.PrevLogIndex + len(args.Entries) <= rf.commitIndex时，意味着领导者尝试让跟随者追加的日志条目范围完全落在跟随者已知的已提交日志区间内。这在正常情况下不应该发生，因为领导者应当总是尝试追加在其已知提交日志之后的新日志，或者是修复不一致的日志。

为何返回:

如果上述条件成立，这可能意味着一种逻辑错误或日志的严重不一致。为了避免潜在的混乱和数据损坏，跟随者选择直接拒绝此次AppendEntries请求，不进行任何日志追加操作。这可以视为一种防御性编程措施，旨在维护日志的一致性和集群状态的正确性。

潜在原因:

这种情况可能是由于网络延迟、消息乱序、或是领导者状态机的逻辑错误导致的。例如，领导者可能基于过时的信息发出了这个请求，或者领导者和跟随者之间存在严重的日志不一致，需要通过更复杂的日志匹配和修复流程来解决。

##### 5、发起投票函数sendAllRequestVote

```Go
// sendAllRequestVote 向所有其他 Raft 节点发送请求投票的 RPC。
// 该函数首先构建 RequestVoteArgs，然后向所有其他节点并行发送请求投票的 RPC。
func (rf *Raft) sendAllRequestVote() {
   rf.mu.Lock()
   defer rf.mu.Unlock()

   // 取出自己的已知的已提交的最后一条日志条目。不能直接取最后一条，因为commitIndex是大多数节点
   // 都已经提交了的，是安全的索引。
   // 看解释
   lastLog := rf.logs[rf.commitIndex]
   // 构建请求投票的参数
   args := &RequestVoteArgs{
      Term:         rf.currentTerm, // 当前任期
      CandidateId:  rf.me,          // 候选人 ID
      LastLogIndex: lastLog.Index,  // 候选人记录的最后一个已提交的日志条目的索引
      LastLogTerm:  lastLog.Term,   // 候选人记录的最后一个已提交的日志条目的任期
   }

   // 向所有其他节点发送请求投票的 RPC
   for i := range rf.peers {
      // 对于非当前节点的其他节点发送，并且本身为候选者状态
      if i != rf.me && rf.role == Candidate {
         // 并行发送请求投票的 RPC
         go func(id int) {
            // 接收返回参数
            ret := &RequestVoteReply{
               Term:        0,
               VoteGranted: false,
            }
            rf.sendRequestVote(id, args, ret)
         }(i)
      }
   }
}
```

在Raft算法中，当一个节点转变为候选人（Candidate）并发起投票请求时，它向其他节点发送请求投票（RequestVote）的RPC，这个请求包含了候选人的信息以及它最后已知的提交日志条目的索引和任期号。候选人选择在投票请求中包含已提交的最后一条日志而不是其日志的最后一条，原因在于以下几个方面：

确保安全性：Raft强调安全性优先于可用性。使用已提交的日志条目作为竞选依据，可以确保如果该候选人赢得选举并成为领导者，它所提议的日志至少包含了集群中大多数节点已经同意的内容。这样，即使候选人的日志末尾有未提交的条目，也不会因为这部分日志的不确定性而影响集群状态的一致性。

促进日志匹配：在Raft中，日志匹配是确保数据一致性的关键。候选者提供已提交日志而非最新日志，可以帮助解决日志不一致问题。如果候选者使用其未提交的日志作为竞选依据，可能会导致选举出一个包含其他节点未见过的日志条目的领导者，进而引发更多的日志冲突和需要解决的不一致问题。

简化日志匹配逻辑：使用已提交日志简化了跟随者判断是否投票给候选者的过程。跟随者只需比较自己已知的提交日志的任期和索引与候选者提供的信息，如果候选者的日志至少一样新（即任期相同或更大，且索引不小于跟随者已知的最大已提交索引），跟随者就更倾向于投票给它。这样可以减少因日志不一致导致的投票拒绝，加速选举过程。

避免潜在的分叉：如果候选者使用自己未提交的日志作为竞选标准，当选后可能会尝试提交这部分日志，而这些日志可能并未在集群中大多数节点上达成一致，从而造成日志分叉，影响数据一致性。使用已提交日志作为基准，可以减少这种情况的发生。

综上所述，候选者在发起投票时选择包含其已知的已提交的最后一条日志条目而非其日志的最后一条，是出于增强系统安全性、促进日志匹配、简化决策逻辑以及避免日志分叉的考虑，这些都是为了确保Raft算法在分布式环境下的稳定性和一致性。

结果：

这里多运行了两个测试，但是总体时间还是在60秒以上。

```Go
PS E:\GoLangProgram\code\xun-go\com\xun\Mit-6.824-xun\src\raft> go test -run 2B
Test (2B): basic agreement ...
  ... Passed --   2.1  3   36    9986    3
Test (2B): RPC byte count ...
  ... Passed --   6.1  3  110  131280   11
Test (2B): test progressive failure of followers ...
  ... Passed --   6.1  3  120   29196    3
Test (2B): test failure of leaders ...
  ... Passed --   7.6  3  230   54502    3
Test (2B): agreement after follower reconnects ...
  ... Passed --   8.7  3  158   42723    8
Test (2B): no agreement if too many followers disconnect ...
  ... Passed --   4.6  5  188   44898    3
Test (2B): concurrent Start()s ...
  ... Passed --   1.2  3   20    5486    6
Test (2B): rejoin of partitioned leader ...
  ... Passed --   8.2  3  222   56958    4
Test (2B): leader backs up quickly over incorrect follower logs ...
  ... Passed --  67.9  5 4828 4299403  107
Test (2B): RPC counts aren't too high ...
  ... Passed --   2.7  3   46   13068   12
PASS
ok      6.824/raft      115.223s
```

![img](D:\Pictures\Camera Roll\lab2b.png)

此时lab2A还是成功的：

![img](D:\Pictures\Camera Roll\lab2a.png)

## Part 2C: persistence（持久化） (困难)

### 实验要求

如果基于 Raft 的服务器重新启动，它**应该从之前中断的地方恢复服务**。这需要 Raft 保持持久状态，以便在重启后仍然存在。论文中的图 2 提到了哪些状态应该是持久的。

一个实际的实现会在每次状态改变时将 Raft 的持久状态写入磁盘，并在重启后从磁盘读取状态。你的实现不会使用磁盘；相反，它将**从一个 `Persister` 对象（参见 persister.go）保存和恢复持久状态**。**调用 `Raft.Make()` 的函数会提供一个 `Persister`，该 `Persister` 初始保存了最近一次持久化的 Raft 状态**（如果有的话）。**Raft 应该从该 `Persister` 初始化其状态，并在每次状态变化时使用它来保存持久状态。使用 `Persister` 的 `ReadRaftState()` 和 `SaveRaftState()` 方法**。

完成 `raft.go` 中的 **`persist()` 和 `readPersist()`** 函数，添加代码以保存和恢复持久状态。需要将状态编码（或“序列化”）为字节数组，以便传递给 `Persister`。使用 `labgob` 编码器；参见 `persist()` 和 `readPersist()` 中的注释。`labgob` 类似于 Go 的 gob 编码器，但如果你**试图编码具有小写字段名称的结构，它会打印错误消息**。在你的实现中改变持久状态的点插入对 `persist()` 的调用。一旦你完成了这些，并且如果你的实现的其余部分是正确的，你应该通过所有的 2C 测试。

### 提示

2C 测试比 2A 或 2B 更苛刻，失败可能是由你在 2A 或 2B 代码中的问题引起的。

你可能需要一种优化，将 `nextIndex` 一次备份多个条目。查看 [Raft 论文](http://nil.csail.mit.edu/6.824/2022/papers/raft-extended.pdf)的第 7 页底部和第 8 页顶部（以灰线标记）。论文对于细节描述得比较模糊，你需要填补这些空白，或许可以借助 6.824 课程的 Raft 讲义。

你的代码应该通过所有的 2C 测试（如下所示），以及 2A 和 2B 测试。

```bash
$ go test -run 2C
Test (2C): basic persistence ...
  ... Passed --   5.0  3   86   22849    6
Test (2C): more persistence ...
  ... Passed --  17.6  5  952  218854   16
Test (2C): partitioned leader and one follower crash, leader restarts ...
  ... Passed --   2.0  3   34    8937    4
Test (2C): Figure 8 ...
  ... Passed --  31.2  5  580  130675   32
Test (2C): unreliable agreement ...
  ... Passed --   1.7  5 1044  366392  246
Test (2C): Figure 8 (unreliable) ...
  ... Passed --  33.6  5 10700 33695245  308
Test (2C): churn ...
  ... Passed --  16.1  5 8864 44771259 1544
Test (2C): unreliable churn ...
  ... Passed --  16.5  5 4220 6414632  906
PASS
ok  	6.824/raft	123.564s
$
```

在提交代码之前，可以多次运行测试并确保每次运行都打印 PASS 。这样可以确保你的代码在各种情况下都是稳定和可靠的，并且能够通过所有的测试用例。

```bash
$ for i in {0..10}; do go test; done
```

### 实验结果

![](D:\Pictures\Camera Roll\lab2c.jpg)

### 实现细节

#### 1、ConflictTerm 和 ConflictIndex 进行回退优化

```go
// AppendEntriesReply 是附加日志条目（包括心跳）的 RPC 响应结构。回退优化
type AppendEntriesReply struct {
	Term          int  // 当前任期，用于领导者更新自己的任期
	Success       bool // 如果跟随者包含了匹配的日志条目且日志条目已成功存储，则为 true
	ConflictTerm  int  //在跟随者日志中与领导者发送的日志条目发生冲突的那条日志的任期号
	ConflictIndex int  //在跟随者日志中发生冲突的具体条目的索引。索引是日志条目在日志文件中的索引
}
```

在Raft一致性算法中，AppendEntries RPC（远程过程调用）用于领导者向跟随者复制日志条目。当跟随者收到AppendEntries请求时，它会检查请求中的日志条目是否与它自己日志中的条目冲突。如果发现冲突，跟随者会通过AppendEntriesReply结构体中的ConflictTerm和ConflictIndex字段来通知领导者，这两个字段的作用如下：

- ConflictTerm: 这个字段表示在跟随者日志中与领导者发送的日志条目发生冲突的那条日志的任期号。任期号是Raft算法中用于确定日志条目有效性的关键标识，较高的任期号意味着日志条目更“新”。通过报告冲突的任期号，领导者可以了解到跟随者日志中某个特定任期的条目与自己的不一致，这有助于领导者采取后续行动来解决冲突。
- ConflictIndex: 这个字段表示在跟随者日志中发生冲突的具体条目的索引。索引是日志条目在日志文件中的位置标识。通过报告冲突的索引，领导者可以定位到日志中的具体位置，从而知道从哪个位置开始日志条目需要被替换或删除。

**当领导者收到带有非零ConflictTerm和ConflictIndex的回复时，它会根据这些信息调整其日志复制策略。例如，领导者可能需要回退到冲突索引之前的日志条目，并重新发送从那个点开始的全部日志条目，以确保日志的一致性。领导者也可能需要检查自己的日志条目，以确保它所持有的日志是最新的，并且与集群中大多数节点的日志相匹配。**

ConflictTerm和ConflictIndex字段在Raft算法中用于在日志复制过程中识别和解决日志不一致的问题，确保所有节点的日志最终达到一致状态。

#### 2、怎么进行回退

在Raft一致性算法中，当领导者接收到跟随者发送的AppendEntriesReply，其中包含ConflictTerm和ConflictIndex时，领导者需要采取适当的措施来解决日志不一致的问题。这里使用的先搜索任期（ConflictTerm）再使用冲突索引（ConflictIndex）的策略，是为了确保日志条目的正确性和一致性。以下是具体的解释：

1. 搜索任期（ConflictTerm）的重要性:
   - 识别日志冲突的来源：ConflictTerm指出了跟随者日志中与领导者日志发生冲突的任期号。这帮助领导者定位到具体的任期，从而了解冲突发生的背景。
   - 确保日志条目的权威性：**在Raft中，更高的任期号通常意味着日志条目更“新”和更权威**。通过查找与ConflictTerm匹配的任期，领导者可以确保它正在处理的是最新的、有效的日志条目。
2. 使用冲突索引（ConflictIndex）的时机:
   - 细化冲突的位置：一旦领导者确定了冲突的任期，它需要进一步定位到日志中的具体位置。ConflictIndex提供了冲突条目的确切索引，帮助领导者了解从哪个点开始日志不一致。
   - 处理日志不一致：在找到冲突任期的条目后，领导者会根据该任期条目的索引来决定如何调整nextIndex，从而决定从哪个位置开始重新发送日志条目，以解决不一致问题。
3. 策略的逻辑:
   - 如果领导者找到了ConflictTerm对应的任期条目，这意味着跟随者日志中的冲突条目是在领导者日志中的某条任期条目之后添加的。在这种情况下，领导者应该将nextIndex设置为该任期最后一个条目的下一个索引，这样在下次AppendEntries请求中，领导者只会发送冲突点之后的日志条目。
   - 如果领导者没有找到ConflictTerm对应的任期条目，这可能意味着跟随者日志中的冲突条目是领导者日志中没有的。此时，领导者应将nextIndex设置为ConflictIndex，这意味着领导者将从ConflictIndex开始重新发送日志条目，以覆盖或修正跟随者日志中的冲突部分。

总结来说，先搜索任期再使用冲突索引的策略，是为了确保领导者能够准确地识别和处理日志不一致的问题，同时最小化日志复制的开销，提高日志复制的效率和准确性。这种策略体现了Raft算法在处理分布式系统中日志复制和一致性问题时的精巧设计。

#### 3、持久化函数

```go
// persist
// 将Raft的持久状态保存到稳定存储中，这使得在崩溃和重启后能够恢复这些状态。
// 参考论文图2以了解哪些状态应该被持久化。
// 函数负责将Raft的几个关键状态持久化存储，确保系统在遭遇故障重启后能够恢复到最近一次的已知安全状态。
// 持久化内容包括当前任期号（currentTerm）、已投票给的候选者（votedFor）以及日志条目（logs）。
func (rf *Raft) persist() {
	// 初始化一个字节缓冲区用于序列化数据
	w := new(bytes.Buffer)
	// 创建一个labgob编码器，用于将Go对象编码为字节流
	e := labgob.NewEncoder(w)
	// 需要保存的内容，使用编码器将关键状态信息序列化到缓冲区
	// 如果编码过程中出现错误，则通过log.Fatal终止程序，防止数据损坏或不完整状态的保存
	if e.Encode(rf.currentTerm) != nil || e.Encode(rf.votedFor) != nil || e.Encode(rf.logs) != nil {
		log.Fatal("Errors occur when encode the data!")
	}
	// 序列化完成后，从缓冲区获取字节切片准备存储
	data := w.Bytes()
	// 调用persister的SaveRaftState方法，将序列化后的数据保存到稳定的存储介质中
	rf.persister.SaveRaftState(data)
}

// readPersist
// restore previously persisted state.
// 恢复先前持久化保存的状态。
// 这个函数在启动时调用，确保从稳定存储中加载之前保存的Raft状态。
func (rf *Raft) readPersist(data []byte) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if data == nil || len(data) < 1 { // bootstrap without any state?
		// 如果没有数据或数据长度不足，可能是初次启动，无需恢复状态
		return
	}
	// 初始化字节缓冲区以读取数据
	r := bytes.NewBuffer(data)
	// 创建一个labgob解码器，用于将字节流转换回Go对象
	d := labgob.NewDecoder(r)

	// 定义变量以接收解码后的状态信息
	var currentTerm int
	var votedFor int
	var logs []LogEntries

	// 按编码的顺序使用解码器从字节流中读取并还原状态信息
	// 如果解码过程中出现错误，通过log.Fatal终止程序，防止使用损坏或不完整的状态数据
	if d.Decode(&currentTerm) != nil || d.Decode(&votedFor) != nil || d.Decode(&logs) != nil {
		log.Fatal("Errors occur when decode the data!")
	} else {
		// 解码成功后，将读取的状态信息赋值给Raft实例的对应字段
		rf.currentTerm = currentTerm
		rf.votedFor = votedFor
		rf.logs = logs
	}
}
```

#### 4、ticker函数

```go
// The ticker go routine starts a new election if this peer hasn't received
// heartsbeats recently.
// ticker 协程在近期没有收到心跳的情况下启动新的选举。
func (rf *Raft) ticker() {
	// dead置为1后为true，则退出运行
	for rf.killed() == false {
		//fmt.Println(rf.me, rf.role, rf.currentTerm, rf.logs, rf.votedFor, rf.voteCount)
		// Your code here to check if a leader election should
		// be started and to randomize sleeping time using
		// time.Sleep().
		// 在这里添加代码以检查是否应该启动领导者选举
		// 并使用 time.Sleep() 随机化休眠时间。
		switch rf.role {
		case Candidate:
			select {
			case <-rf.VoteMsgChan:
				// 不需要continue跳过当前循环
			case resp := <-rf.appendEntriesChan:
				if resp.Term >= rf.currentTerm {
					// 关键点：候选者收到更大任期的leader的心跳信息或者日志复制信息后，需要转为follower
					rf.ConvertToFollower(resp.Term)
					continue
				}
			case <-time.After(electionTimeout + time.Duration(rand.Int31()%300)*time.Millisecond):
				// 选举超时 重置选举状态
				if rf.role == Candidate {
					rf.ConvertToCandidate()
					// 发起投票请求
					go rf.sendAllRequestVote()
				}
			case <-rf.LeaderMsgChan:
			}
		case Leader:
			// Leader 定期发送心跳和同步日志
			rf.SendAllAppendEntries()
			// 更新commitIndex对子节点中超过半数复制的日志进行提交
			go rf.checkCommitIndex()
			select {
			case resp := <-rf.appendEntriesChan:
				// 处理跟随者的响应，如发现更高的任期则转为Follower
				if resp.Term > rf.currentTerm {
					rf.ConvertToFollower(resp.Term)
					continue
				}
			case <-time.After(heartBeatInterval):
				// 超时后继续发送心跳
				continue
			}
		case Follower:
			// 如果是跟随者，等待不同的事件发生
			select {
			case <-rf.VoteMsgChan:
				// 收到投票消息，继续等待

			case resp := <-rf.appendEntriesChan:
				// 收到附加日志条目消息，继续等待
				if resp.Term > rf.currentTerm {
					rf.ConvertToFollower(resp.Term)
					continue
				}
			case <-time.After(appendEntriesTimeout + time.Duration(rand.Int31()%300)*time.Millisecond):
				// 附加日志条目超时，转换为候选人，发起选举
				// 增加扰动避免多个Candidate同时进入选举
				rf.ConvertToCandidate()
				// 发起投票请求
				go rf.sendAllRequestVote()
			}
		}
	}
}
```

#### 5、SendAppendEntries 函数发送 AppendEntries RPC 请求

```go
// SendAppendEntries 向指定的节点发送 AppendEntries RPC 请求。
// 发送具体的 AppendEntries 请求，并处理响应
func (rf *Raft) SendAppendEntries(id int, args *AppendEntriesArgs, reply *AppendEntriesReply) {
	// 调用指定节点的 AppendEntriesHandler 方法，并传递请求和响应结构
	ok := rf.peers[id].Call("Raft.AppendEntriesHandler", args, reply)
	// 发送失败直接返回即可。
	if !ok {
		return
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	// 函数退出前，持久化状态
	defer rf.persist()

	// 阻止过时RPC：leader 发送的请求的任期与当前任期不同，则直接返回
	if args.Term != rf.currentTerm {
		return
	}

	// 如果响应中的任期大于当前任期，当前节点会转换为跟随者
	if reply.Term > rf.currentTerm {
		rf.ConvertToFollower(reply.Term)
	}

	// 如果当前节点不再是领导者，则直接返回
	if rf.role != Leader {
		return
	}

	// If AppendEntries fails because of log inconsistency:
	// decrement nextIndex and retry (§5.3)
	// 当接收到AppendEntries RPC的响应失败，意味着跟随者上的日志与领导者尝试追加的日志条目之间存在不一致。
	// 为了解决这种不一致并重新尝试AppendEntries过程：
	// 减小(nextIndex)指向该跟随者（id标识）的日志条目索引，
	// 这是为了找到一个双方都有相同前缀的日志位置，从而恢复一致性。
	// 回退优化：
	// 在收到一个冲突响应后，领导者首先应该搜索其日志中任期为 conflictTerm 的条目。
	// 如果领导者在其日志中找到此任期的一个条目，则应该设置 nextIndex 为其日志中此任期的最后一个条目的索引的下一个。
	// 如果领导者没有找到此任期的条目，则应该设置 nextIndex = conflictIndex。
	if !reply.Success {
		if reply.ConflictTerm == -1 {
			rf.nextIndex[id] = reply.ConflictIndex
		} else {
			flag := true
			for j := args.PrevLogIndex; j >= 0; j-- {
				// 找到冲突任期的最后一条日志（冲突任期号为跟随者日志中最后一条条目的任期号）
				if rf.logs[j].Term == reply.ConflictTerm {
					rf.nextIndex[id] = j + 1
					flag = false
					break
				} else if rf.logs[j].Term < reply.ConflictTerm {
					break
				}
			}
			if flag {
				// 如果没有找到冲突任期，则设置nextIndex为冲突索引
				rf.nextIndex[id] = reply.ConflictIndex
			}
		}

	} else {
		// 同步成功：在分布式系统中，可能存在消息延迟、乱序或重试的情况。如果领导者在这次成功响应处理之前，已经尝试过发送更靠后的日志条目（即已经增大了nextIndex[id]），
		// 直接使用args.PrevLogIndex+len(args.Entries)+1可能会导致nextIndex[id]被错误地回退到一个小于已知可安全发送的日志索引值。
		// 取最大值还确保了同步的效率。如果由于某种原因（例如，领导者在等待此响应的同时又发送了一个包含更多日志条目的AppendEntries请求），直接设置nextIndex[id]为
		// args.PrevLogIndex+len(args.Entries)+1可能会忽略掉领导者已经做出的更优的尝试。
		rf.nextIndex[id] = max(args.PrevLogIndex+len(args.Entries)+1, rf.nextIndex[id])
		// 更新（已匹配索引）matchIndex[id]为最新的已知被复制到跟随者的日志索引，
		// PrevLogIndex + 新追加的日志条目数量
		// 这有助于领导者跟踪每个跟随者已复制日志的最大索引。
		rf.matchIndex[id] = max(args.PrevLogIndex+len(args.Entries), rf.matchIndex[id])
	}
}
```

#### 6、AppendEntriesHandler 函数 处理 AppendEntries 请求

```go
// AppendEntriesHandler 由Leader向每个其余节点发送
// 处理来自领导者的 AppendEntries RPC 请求
// 处理接收到的 AppendEntries 请求，包括心跳和日志条目的复制
// 这是除Leader以外其余节点的处理逻辑
// 在Raft中，领导者通过强迫追随者的日志复制自己的日志来处理不一致。
// 这意味着跟随者日志中的冲突条目将被来自领导者日志的条目覆盖。
// 第5.4节将说明，如果加上另外一个限制，这样做是安全的。
func (rf *Raft) AppendEntriesHandler(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// 函数退出前，持久化状态
	defer rf.persist()
	defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} before processing AppendEntriesRequest %v and reply AppendEntriesResponse %v", rf.me, rf.role, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.logs[0], rf.logs[len(rf.logs)-1], args, reply)

	// 传一个带有当前任期的结构体表示接收到了Leader的请求。
	// 初始化响应的任期为当前任期
	reply.Term = rf.currentTerm

	// 老Leader重连后Follower不接受旧信号
	if rf.currentTerm > args.Term {
		return
	}

	// 收到Leader更高的任期时，更新自己的任期，转为 leader 的追随者
	// 或者如果当前节点是候选者，则更新自己的任期，转为追随者
	if rf.currentTerm < args.Term || (rf.currentTerm == args.Term && rf.role == Candidate) {
		rf.ConvertToFollower(args.Term)
	}

	// 向 appendEntriesChan 发送一个空结构体，以表示接收到了领导者的请求
	//rf.appendEntriesChan <- struct {}{}
	// 发送心跳重置计时器或日志条目后
	rf.appendEntriesChan <- AppendEntriesReply{Term: rf.currentTerm, Success: true}
	// 如果追随者的日志中没有 preLogIndex，它应该返回 conflictIndex = len(log) 和 conflictTerm = None。
	if args.PrevLogIndex >= len(rf.logs) {
		// 该检查是为了确保领导者请求的前一个日志条目索引没有超过跟随者当前日志的实际长度。如果args.PrevLogIndex大于跟随者日志的长度，
		// 这意味着领导者认为跟随者应该有一个比实际更长的日志，这通常是因为领导者和跟随者之间的日志出现了不一致，或者跟随者落后很多且领导者的信息过时。
		// 领导者收到这样的失败响应后，会根据跟随者的反馈调整其nextIndex值，然后重试发送AppendEntries请求，从一个更早的索引开始，
		// 以解决日志不一致的问题。这样，通过一系列的尝试与调整，Raft算法能够最终确保集群间日志的一致性。
		reply.ConflictTerm = -1
		reply.ConflictIndex = len(rf.logs)
		return
	}
	lastLog := rf.logs[args.PrevLogIndex]
	// 最后的日志对不上 因此需要让Leader对该节点的nextIndex - 1
	// 优化逻辑：处理日志条目任期不匹配的情况
	// 当领导者尝试追加的日志条目与跟随者日志中的前一条条目任期不一致时，
	// 跟随者需要向领导者反馈冲突信息，以便领导者能够正确地调整日志复制策略。
	// 如果追随者的日志中有 preLogIndex，但是任期不匹配，它应该返回 conflictTerm = log[preLogIndex].Term，
	// 即返回与领导者提供的前一条日志条目任期不同的任期号。
	// 然后在它的日志中搜索任期等于 conflictTerm 的第一个条目索引。
	if args.PrevLogTerm != lastLog.Term {
		// 设置冲突任期号为跟随者日志中最后一条条目的任期号，
		// 这是因为跟随者日志的最后一条条目与领导者提供的前一条条目任期不一致。
		reply.ConflictTerm = lastLog.Term
		// 然后在它的日志中搜索任期等于 conflictTerm 的第一个条目索引。
		// 从 args.PrevLogIndex 开始向前遍历日志条目，直到找到任期不匹配的第一个条目，
		// 或者遍历到日志的起始位置。
		for j := args.PrevLogIndex; j >= 0; j-- {
			// 当找到任期不匹配的条目时，记录其下一个条目的索引作为冲突索引，
			// 这意味着从这个索引开始，日志条目需要被重新同步。
			if rf.logs[j].Term != lastLog.Term {
				reply.ConflictIndex = j + 1
				break
			}
		}
		// 完成冲突信息的收集后，返回处理结果，结束函数执行。
		return
	}
	reply.Success = true
	//领导者尝试让跟随者追加的日志条目范围完全落在跟随者已知的已提交日志区间内，那就不需要再复制了
	if args.PrevLogIndex+len(args.Entries) <= rf.commitIndex {
		return
	}
	// 在PrevLogIndex处开始复制一份日志
	// 实现了 Raft 算法中跟随者接收到领导者发送的日志条目后，根据日志条目的任期号进行日志条目的复制或替换逻辑。
	// 这里要循环判断冲突再复制 不然可能由于滞后性删除了logs
	for idx := 0; idx < len(args.Entries); idx++ {
		// 计算当前条目在跟随者日志中的目标索引位置
		curIdx := idx + args.PrevLogIndex + 1
		// 检查当前条目索引是否超出跟随者日志的长度，或者
		// 日志条目的任期号与跟随者现有日志条目中的任期号是否不一致
		if curIdx >= len(rf.logs) || rf.logs[curIdx].Term != args.Entries[idx].Term {
			// 如果存在落后或者冲突，以当前领导者的日志为主，从当前位置开始，用领导者发送的日志条目替换或追加到跟随者日志中
			// 这里使用切片操作，保留原有日志的前半部分，然后追加领导者发送的日志条目
			rf.logs = append(rf.logs[:curIdx], args.Entries[idx:]...)
			// 替换或追加完毕后，跳出循环
			break
		}
	}
	// 更新commitIndex
	if args.LeaderCommit > rf.commitIndex {
		rf.commitIndex = min(len(rf.logs)-1, args.LeaderCommit)
	}
}
```





## PART 2D：日志压缩 log compaction（困难）

### 介绍

当前的情况是，重新启动的服务器会重放完整的 Raft 日志以恢复其状态。然而，**对于一个长时间运行的服务来说，永远记住完整的 Raft 日志是不现实的。**相反，你需要修改 Raft 以配合**定期持久存储其状态“快照”的服务**，此时 **Raft 会丢弃快照之前的日志条目。这样可以减少持久化数据的数量并加快重启速度**。然而，现在可能会出现一种情况，即**某个 follower 落后太多，导致 leader 丢弃了它需要追赶的日志条目；此时，leader 必须发送一个快照以及从快照开始的日志**。[扩展版 Raft 论文](http://nil.csail.mit.edu/6.824/2022/papers/raft-extended.pdf)的第7部分概述了这个方案；你需要设计细节。

你可能会发现参考 [Raft 交互图](http://nil.csail.mit.edu/6.824/2022/notes/raft_diagram.pdf)以了解复制服务和 Raft 之间的通信方式会很有帮助。

你的 Raft 必须提供以下功能，该功能可以由服务调用以传递其状态的序列化快照：

```go
Snapshot(index int, snapshot []byte)
```

在 Lab 2D 中，**测试程序会定期调用 `Snapshot()`**。在 Lab 3 中，你将编写一个调用 `Snapshot()` 的键/值服务器；该快照将包含完整的键/值对表。服务层会在每个 peer 上调用 `Snapshot()`（不仅仅是在 leader 上）。

**`index` 参数指示的是反映在快照中的最高日志条目。Raft 应该丢弃该点之前的日志条目。**你需要修改你的 **Raft 代码，以便只存储日志的尾部。**

你需要实现论文中讨论的 `InstallSnapshot` RPC，允许 Raft leader 告诉落后的 Raft peer 用快照替换其状态。你可能需要仔细考虑 `InstallSnapshot` 如何与图2中的状态和规则进行交互。

**当 follower 的 Raft 代码收到 `InstallSnapshot` RPC 时，可以使用 `applyCh` 将快照发送到服务层中的 `ApplyMsg`。`ApplyMsg` 结构定义已经包含了你需要的字段（以及测试程序所期望的字段）。注意，这些快照只会推进服务的状态，不会使其倒退。**

如果服务器崩溃，它必须从持久化的数据重新启动。你的 **Raft 应该同时持久化 Raft 状态和相应的快照**。**使用 `persister.SaveStateAndSnapshot()`，该函数接受 Raft 状态和相应快照的单独参数。如果没有快照，则将快照参数传递为 nil。**

当服务器重新启动时，应用层会读取持久化的快照并恢复其保存的状态。

之前，该实验建议你实现一个名为 **`CondInstallSnapshot`** 的函数，以避免需要协调 `applyCh` 上发送的快照和日志条目。这个遗留的 API 接口仍然存在，但不建议你实现它：相反，我们建议你**简单地让它返回 true**。

**实现 `Snapshot()` 和 `InstallSnapshot` RPC，以及支持这些功能所需的 Raft 修改（例如，关于修剪日志的操作）**。当解决方案可以通过 2D 测试（以及之前所有的 Lab 2 测试）时该实验就算是完成了。

### 提示

- 一个好的起点是先修改你的代码，使其**能够仅存储从某个索引X开始的日志部分**。最初，你可以将X设置为零并运行2B/2C测试。然后，让`Snapshot(index)`丢弃索引之前的日志，并将X设置为该索引。如果一切顺利，你现在应该能通过第一个2D测试。

- 不能在Go切片中存储日志，并将Go切片索引与Raft日志索引用于相互替换；而是需要以一种方式索引切片，以考虑丢弃的日志部分。

- 接下来：如果领导者没有需要同步给跟随者的最新日志条目，则发送`InstallSnapshot` RPC。

- 在单个`InstallSnapshot` RPC中发送整个快照。不要实现图13的分段机制来拆分快照。

- **Raft必须以允许Go垃圾收集器释放和重用内存的方式丢弃旧日志条目；这要求没有可达引用（指针）指向丢弃的日志条目。**
- 即使日志被修剪了，你的实现仍然需要正确发送在`AppendEntries` RPC中的新条目之前的条目的任期和索引；这可能需要保存和引用最新快照的`lastIncludedTerm/lastIncludedIndex`（考虑这些是否应该持久化）。

- 在不使用-race选项的情况下，运行完整的Lab 2测试（2A+2B+2C+2D）需要消耗的合理时间是6分钟的真实时间和1分钟的CPU时间。使用-race选项运行时，大约需要10分钟的真实时间和2分钟的CPU时间。

你的代码应通过所有2D测试（如下所示），以及2A、2B和2C测试。

```bash
$ go test -run 2D
Test (2D): snapshots basic ...
  ... Passed --  11.6  3  176   61716  192
Test (2D): install snapshots (disconnect) ...
  ... Passed --  64.2  3  878  320610  336
Test (2D): install snapshots (disconnect+unreliable) ...
  ... Passed --  81.1  3 1059  375850  341
Test (2D): install snapshots (crash) ...
  ... Passed --  53.5  3  601  256638  339
Test (2D): install snapshots (unreliable+crash) ...
  ... Passed --  63.5  3  687  288294  336
Test (2D): crash and restart all servers ...
  ... Passed --  19.5  3  268   81352   58
PASS
ok      6.824/raft      293.456s
```

当评估你的提交时，是运行没有 -race 标志的测试，但也应该确保代码在有 -race 标志的情况下也能一致地通过测试。

### 2D 实验结果

![](D:\Pictures\Camera Roll\lab2d.jpg)



### Lab2 测试结果

![lab2-1](D:\Pictures\Camera Roll\lab2-1.jpg)

![lab2-2](D:\Pictures\Camera Roll\lab2-2.jpg)

**至此，整个实验二的Raft实验都完成了，可以调整 constant.go 中的时间大小探索适合自己电脑的超时时间或者时间间隔。**



### 实现细节

#### 1、快照请求和响应结构体

```go
// InstallSnapshotArgs 定义了在Raft协议中安装快照所需参数的结构。
type InstallSnapshotArgs struct {
	Term              int    // 是发送快照的领导者当前的任期。
	LeaderId          int    // 是领导者的标识符，它帮助跟随者将客户端请求重定向到当前的领导者。
	LastIncludedIndex int    // 是快照中包含的最后一个日志索引。这个索引及其之前的所有日志条目都将被快照替换。
	LastIncludedTerm  int    // LastIncludedIndex 所属的任期。它确保快照包含最新的信息。
	Data              []byte // Data 是表示快照数据的原始字节切片。快照以分块的方式发送，从指定的偏移量开始。
}

// InstallSnapshotReply 定义了跟随者对快照安装请求的响应结构。
type InstallSnapshotReply struct {
	// Term 是跟随者当前的任期。领导者接收到此响应后会检查该任期，以确认自己是否仍然是当前的领导者。
	Term int
}
```

#### 2、applyLog方法

```go
// applyLog 方法负责将已知提交的日志条目应用到状态机中。
// 这个方法检查是否有新的日志条目可以被应用，并将它们发送到applyChan，
// 以便状态机能够处理这些条目并更新其状态。
func (rf *Raft) applyLog() {
    rf.mu.Lock()
    // 获取快照包含的最后一条日志的索引，即当前日志的起始点
    snapShotIndex := rf.getFirstLog().Index

    // 检查是否有新的提交日志需要应用
    // 如果commitIndex不大于lastApplied，说明没有新的日志需要应用
    if rf.commitIndex <= rf.lastApplied {
        rf.mu.Unlock()
        return
    }

    // 创建一个新的日志条目切片，用于存储需要应用的日志
    copyLogs := make([]LogEntries, rf.commitIndex-rf.lastApplied)

    // 复制需要应用的日志条目到copyLogs中
    copy(copyLogs, rf.logs[rf.lastApplied-snapShotIndex+1:rf.commitIndex-snapShotIndex+1])

    // 更新lastApplied，记录最新已应用的日志条目的索引
    rf.lastApplied = rf.commitIndex
    rf.mu.Unlock()

    // 遍历从lastApplied+1到commitIndex的所有日志条目
    for _, logEntity := range copyLogs {
        // 通过applyChan发送ApplyMsg，通知状态机应用新的日志条目
        rf.applyChan <- ApplyMsg{
            CommandValid: true,              // 标记为有效的新提交日志条目
            Command:      logEntity.Command, // 新提交的日志条目的具体命令
            CommandIndex: logEntity.Index,   // 新提交日志条目的索引
        }
    }
}
```

#### 3、config.go中的applier方法接收applyChan中的日志

```go
// applier 是一个处理来自applyCh通道消息的函数，它确保接收到的消息与日志的内容匹配。
// 这个函数在一个独立的goroutine中运行，不断从applyCh读取ApplyMsg消息，
// 并验证这些消息的正确性。
//
// 参数:
//   i - 当前节点的ID，用于日志输出和错误报告。
//   applyCh - 一个通道，Raft节点通过它发送需要应用的状态机命令。
func (cfg *config) applier(i int, applyCh chan ApplyMsg) {
    // 无限循环，持续从applyCh读取消息
    for m := range applyCh {
        // 如果ApplyMsg的CommandValid字段为false，跳过这条消息
        // 这通常意味着消息中不包含有效的命令
        if m.CommandValid == false {
            // 忽略非命令类型的ApplyMsg
        } else {
            // 加锁以保护对日志和状态的访问
            cfg.mu.Lock()
            defer cfg.mu.Unlock()

            // 检查日志条目与接收到的ApplyMsg是否匹配
            err_msg, prevok := cfg.checkLogs(i, m)

            // 如果日志条目的索引大于1且检查失败，构造错误消息
            if m.CommandIndex > 1 && prevok == false {
                err_msg = fmt.Sprintf("server %v apply out of order %v", i, m.CommandIndex)
            }

            // 如果存在错误消息，打印致命错误并记录错误信息
            if err_msg != "" {
                log.Fatalf("apply error: %v", err_msg)
                cfg.applyErr[i] = err_msg

                // 继续读取消息，防止Raft阻塞并持有锁...
                // 这有助于确保Raft的正常运行，即使遇到错误
            }
        }
    }
}
```

在这个函数中，applier不断地从applyCh通道读取ApplyMsg消息。如果CommandValid字段为false，则直接跳过该消息，因为它不包含有效的命令。对于包含有效命令的消息，applier会检查这些消息与日志内容是否匹配，以确保状态机的更新顺序正确。如果发现错误，例如日志条目与消息不匹配，applier会记录错误并打印一个致命错误，但会继续读取消息，以防止Raft阻塞并持有锁，保证系统的健壮性和连续性。

```go
// checkLogs 检查ApplyMsg中的命令是否与日志中已有的命令匹配。
// 这个函数确保每个日志条目的值在整个集群中是一致的。
//
// 参数:
//   i - 当前节点的ID，用于日志输出和错误报告。
//   m - ApplyMsg类型的消息，包含需要检查的命令。
//
// 返回:
//   err_msg - 如果检测到不匹配的命令，返回错误信息；否则返回空字符串。
//   prevok - 布尔值，表示前一个日志条目是否存在。
func (cfg *config) checkLogs(i int, m ApplyMsg) (string, bool) {
	var err_msg string
	v := m.Command

	// 遍历所有节点的日志
	for j := 0; j < len(cfg.logs); j++ {
		// 检查当前节点的日志条目是否已存在，且值与ApplyMsg中的值不同
		if old, ok := cfg.logs[j][m.CommandIndex]; ok && old != v {
			log.Printf("%v: log %v; server %v\n", i, cfg.logs[i], cfg.logs[j])

			// 构造错误信息，表示另一个节点已经为这个条目提交了不同的值
			err_msg = fmt.Sprintf("commit index=%v server=%v %v != server=%v %v",
				m.CommandIndex, i, m.Command, j, old)
		}
	}

	// 检查前一个日志条目是否存在
	_, prevok := cfg.logs[i][m.CommandIndex-1]

	// 更新当前节点的日志条目
	cfg.logs[i][m.CommandIndex] = v

	// 如果当前日志条目的索引大于已知的最大索引，更新最大索引
	if m.CommandIndex > cfg.maxIndex {
		cfg.maxIndex = m.CommandIndex
	}

	return err_msg, prevok
}

```

这个函数的主要目的是确保日志的一致性，即所有节点的日志在相同索引处的值应该相同。它通过比较ApplyMsg中的命令与所有节点日志中相应位置的命令来实现这一点。如果发现不匹配的情况，函数会生成一个错误信息并返回。此外，函数还会更新当前节点的日志，并维护一个最大索引值，用于跟踪日志的最新状态。

#### 4、checkCommitIndex 方法

```go
// checkCommitIndex 方法让领导者节点根据Raft算法的规则检查并更新commitIndex。
// (§5.3, §5.4).
func (rf *Raft) checkCommitIndex() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	snapShotIndex := rf.getFirstLog().Index // 获取快照包含的最后一条日志的索引，即当前日志的起始点
	// 从最后一条日志开始遍历，倒序遍历日志条目，快照保存的日志不需要检查
	for idx := len(rf.logs) - 1; idx >= rf.commitIndex-snapShotIndex; idx-- {
		// figure8简化版本
		// Leader 不能直接提交不属于Leader任期的日志条目
		if rf.logs[idx].Term < rf.currentTerm || rf.role != Leader {
			return
		}
		count := 1
		// 遍历每个服务器的matchIndex，如果大于等于当前遍历的日志索引，则表明该服务器已经成功复制日志，计数加1
		for i := range rf.matchIndex {
			// 需要加上快照的最后一条索引
			if i != rf.me && rf.matchIndex[i] >= idx+snapShotIndex {
				count++
			}
		}
		// 如果计数大于半数，则更新commitIndex为当前遍历的日志索引，允许更多的日志条目被状态机应用。
		if count > len(rf.peers)/2 {
			if rf.role == Leader {
				// 需要加上快照的最后一条索引
				rf.commitIndex = idx + snapShotIndex
			}
			break
		}
	}
}
```

#### 5、Snapshot 方法创建一个快照

```go
// Snapshot 接收服务层的通知，表明已经创建了一个包含截至index的所有信息的快照。
// 这意味着服务层不再需要通过（包括）该index的日志。Raft应尽可能地修剪其日志。
// 参数:
//   index - 快照所包含的最后日志的索引。
//   snapshot - 快照数据。
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.trimIndex(index)
	// 保存状态和日志
	rf.persister.SaveStateAndSnapshot(rf.EncoderState(), snapshot)
}

// trimIndex 修剪日志，移除快照所包含的所有日志条目。
// 参数:
//   index - 快照所包含的最后日志的索引。
func (rf *Raft) trimIndex(index int) {
	// 获取第一个日志条目的索引
	snapShotIndex := rf.getFirstLog().Index
	// 如果日志的起始索引已经等于或大于要修剪的索引，则无需修剪
	if snapShotIndex >= index {
		return
	}

	// rf.logs[0]保留快照的lastLog，快照中包含的最后一条日志也会被保留下来，而不会被修剪掉
	// 释放大切片内存
	rf.logs = append([]LogEntries{}, rf.logs[index-snapShotIndex:]...)
	rf.logs[0].Command = nil
}
```

#### 6、SendAllAppendEntries 向所有节点发送心跳或日志

```go
// SendAllAppendEntries 由Leader向其他所有节点调用来复制日志条目;也用作heartbeat
// 用于领导者向其他所有节点发送附加日志条目（或心跳）请求。在领导者周期性地发送心跳或需要复制日志条目到所有节点时使用
func (rf *Raft) SendAllAppendEntries() {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer DPrintf("Term %v Leader %v Send all AppendEntries Log : %v", rf.currentTerm, rf.me, rf.logs)

	for server := range rf.peers {
		// 对于每个不是当前节点的节点，leader 启动一个新的 goroutine 来发送 AppendEntries 请求
		if server != rf.me && rf.role == Leader {
			// 向follower发送nextIndex最新log日志
			// 如果无需更新 则发送心跳即可。
			// 为了效率 可以一次发送多份
			// 这一段要在锁内处理，防止越界。
			nextId := rf.nextIndex[server] // 获取Leader对于第id个服务器的下一个要发送的日志id
			firstLog := rf.getFirstLog()   // 获取Leader的第一条日志（快照保存的最后一条日志）

			// 此时发送AppendEntries信号，让节点复制日志，否则则直接发送快照文件，让子节点复制
			if nextId > firstLog.Index { // 判断是否需要发送日志条目还是快照
				// 减去快照的最后一条日志索引，得到相对于快照的在当前日志切片中的下一条需要同步的日志索引
				nextId -= firstLog.Index
				lastLog := rf.logs[nextId-1] // 获取上次发送给follower的最后一条日志条目
				// 创建一个新切片，用于存放需要同步给follower的尚未同步的日志条目
				logs := make([]LogEntries, len(rf.logs)-nextId)
				copy(logs, rf.logs[nextId:]) // 拷贝尚未同步的日志

				// 构建AppendEntriesArgs结构体，准备发送日志条目
				args := &AppendEntriesArgs{
					Term:         rf.currentTerm,
					LeaderId:     rf.me,
					PrevLogIndex: lastLog.Index,  // 上次同步的最后一条日志的索引
					PrevLogTerm:  lastLog.Term,   // 上次同步的最后一条日志的任期
					LeaderCommit: rf.commitIndex, // 已知被提交的最高日志条目索引
					Entries:      logs,           // 要附加的需要同步的日志条目
				}
				// 异步发送AppendEntries请求给follower
				go func(id int, args *AppendEntriesArgs) {
					reply := &AppendEntriesReply{}
					rf.SendAppendEntries(id, args, reply)
				}(server, args)
			} else {
				// 下一条要发送的日志索引小于快照的最后一条日志索引，说明需要全部日志都通过快照保存
				// 构建InstallSnapshotArgs结构体，准备直接发送新建的快照
				args := &InstallSnapshotArgs{
					Term:              rf.currentTerm,
					LeaderId:          rf.me,
					LastIncludedIndex: firstLog.Index, // 快照保存的最后一条日志的索引
					LastIncludedTerm:  firstLog.Term,  // 快照保存的最后一条日志的任期
					Data:              rf.persister.ReadSnapshot(),
				}

				// 异步发送InstallSnapshot请求给follower
				go func(id int, args *InstallSnapshotArgs) {
					reply := &InstallSnapshotReply{}
					rf.SendInstallSnapshotRpc(id, args, reply)
				}(server, args)
			}
		}
	}
}
```

#### 7、SendAppendEntries 方法发送 AppendEntries RPC 请求

```go
// SendAppendEntries 向指定的节点发送 AppendEntries RPC 请求。
// 发送具体的 AppendEntries 请求，并处理响应
func (rf *Raft) SendAppendEntries(id int, args *AppendEntriesArgs, reply *AppendEntriesReply) {
	// 调用指定节点的 AppendEntriesHandler 方法，并传递请求和响应结构
	ok := rf.peers[id].Call("Raft.AppendEntriesHandler", args, reply)
	// 发送失败直接返回即可。
	if !ok {
		return
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	// 函数退出前，持久化状态
	defer rf.persist()

	// 阻止过时RPC：leader 发送的请求的任期与当前任期不同，则直接返回
	if args.Term != rf.currentTerm {
		return
	}

	// 如果响应中的任期大于当前任期，当前节点会转换为跟随者
	if reply.Term > rf.currentTerm {
		rf.ConvertToFollower(reply.Term)
	}

	// 如果当前节点不再是领导者，则直接返回
	if rf.role != Leader {
		return
	}

	// If AppendEntries fails because of log inconsistency:
	// decrement nextIndex and retry (§5.3)
	// 当接收到AppendEntries RPC的响应失败，意味着跟随者上的日志与领导者尝试追加的日志条目之间存在不一致。
	// 为了解决这种不一致并重新尝试AppendEntries过程：
	// 减小(nextIndex)指向该跟随者（id标识）的日志条目索引，
	// 这是为了找到一个双方都有相同前缀的日志位置，从而恢复一致性。
	// 回退优化：
	// 在收到一个冲突响应后，领导者首先应该搜索其日志中任期为 conflictTerm 的条目。
	// 如果领导者在其日志中找到此任期的一个条目，则应该设置 nextIndex 为其日志中此任期的最后一个条目的索引的下一个。
	// 如果领导者没有找到此任期的条目，则应该设置 nextIndex = conflictIndex。
	if !reply.Success {
		if reply.ConflictTerm == -1 {
			rf.nextIndex[id] = reply.ConflictIndex
		} else {
			// 2D更新，注意日志相对于上个快照的下标偏移
			snapLastIndex := rf.getFirstLog().Index
			flag := true
			for j := len(rf.logs) - 1; j >= 0; j-- {
				// 找到冲突任期的最后一条日志（冲突任期号为跟随者日志中最后一条条目的任期号）
				if rf.logs[j].Term == reply.ConflictTerm {
					rf.nextIndex[id] = j + 1 + snapLastIndex // 加上快照的偏移量
					flag = false
					break
				} else if rf.logs[j].Term < reply.ConflictTerm {
					break
				}
			}
			if flag {
				// 如果没有找到冲突任期，则设置nextIndex为冲突索引
				rf.nextIndex[id] = reply.ConflictIndex
			}
		}

	} else {
		// 同步成功：在分布式系统中，可能存在消息延迟、乱序或重试的情况。如果领导者在这次成功响应处理之前，已经尝试过发送更靠后的日志条目（即已经增大了nextIndex[id]），
		// 直接使用args.PrevLogIndex+len(args.Entries)+1可能会导致nextIndex[id]被错误地回退到一个小于已知可安全发送的日志索引值。
		// 取最大值还确保了同步的效率。如果由于某种原因（例如，领导者在等待此响应的同时又发送了一个包含更多日志条目的AppendEntries请求），直接设置nextIndex[id]为
		// args.PrevLogIndex+len(args.Entries)+1可能会忽略掉领导者已经做出的更优的尝试。
		rf.nextIndex[id] = max(args.PrevLogIndex+len(args.Entries)+1, rf.nextIndex[id])
		// 更新（已匹配索引）matchIndex[id]为最新的已知被复制到跟随者的日志索引，
		// PrevLogIndex + 新追加的日志条目数量
		// 这有助于领导者跟踪每个跟随者已复制日志的最大索引。
		rf.matchIndex[id] = max(args.PrevLogIndex+len(args.Entries), rf.matchIndex[id])
	}
}
```

#### 8、AppendEntriesHandler方法--AppendEntries 请求的处理函数

```go
// AppendEntriesHandler 由Leader向每个其余节点发送
// 处理来自领导者的 AppendEntries RPC 请求
// 处理接收到的 AppendEntries 请求，包括心跳和日志条目的复制
// 这是除Leader以外其余节点的处理逻辑
// 在Raft中，领导者通过强迫追随者的日志复制自己的日志来处理不一致。
// 这意味着跟随者日志中的冲突条目将被来自领导者日志的条目覆盖。
// 第5.4节将说明，如果加上另外一个限制，这样做是安全的。
func (rf *Raft) AppendEntriesHandler(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// 函数退出前，持久化状态
	defer rf.persist()
	defer DPrintf("{Node %v}'s state is {state %v,term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} before processing AppendEntriesRequest %v and reply AppendEntriesResponse %v", rf.me, rf.role, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.logs[0], rf.logs[len(rf.logs)-1], args, reply)
	defer DPrintf("{Node %v}'s state is {state %v,Term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} before processing AppendEntriesRequest %v and reply AppendEntriesResponse %v", rf.me, rf.role, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), args, reply)
	defer DPrintf("{Node %v}Logs %v", rf.me, rf.logs)

	// 传一个带有当前任期的结构体表示接收到了Leader的请求。
	// 初始化响应的任期为当前任期
	reply.Term = rf.currentTerm

	// 老Leader重连后Follower不接受旧信号
	if rf.currentTerm > args.Term {
		return
	}

	// 收到Leader更高的任期时，更新自己的任期，转为 leader 的追随者
	// 或者如果当前节点是候选者，则更新自己的任期，转为追随者
	if rf.currentTerm < args.Term || (rf.currentTerm == args.Term && rf.role == Candidate) {
		rf.ConvertToFollower(args.Term)
	}

	// 向 appendEntriesChan 发送一个空结构体，以表示接收到了领导者的请求
	//rf.appendEntriesChan <- struct {}{}
	// 发送心跳重置计时器或日志条目后
	rf.appendEntriesChan <- AppendEntriesReply{Term: rf.currentTerm, Success: true}
	// 如果追随者的日志中没有 preLogIndex，它应该返回 conflictIndex = len(log) 和 conflictTerm = None。
	if args.PrevLogIndex >= rf.getLogLen() {
		// 该检查是为了确保领导者请求的前一个日志条目索引没有超过跟随者当前日志的实际长度。如果args.PrevLogIndex大于跟随者日志的长度，
		// 这意味着领导者认为跟随者应该有一个比实际更长的日志，这通常是因为领导者和跟随者之间的日志出现了不一致，或者跟随者落后很多且领导者的信息过时。
		// 领导者收到这样的失败响应后，会根据跟随者的反馈调整其nextIndex值，然后重试发送AppendEntries请求，从一个更早的索引开始，
		// 以解决日志不一致的问题。这样，通过一系列的尝试与调整，Raft算法能够最终确保集群间日志的一致性。
		reply.ConflictTerm = -1
		reply.ConflictIndex = rf.getLogLen()
		return
	}
	// 2D更新，注意日志的下标偏移
	snapLastIndex := rf.getFirstLog().Index

	lastLog := rf.logs[args.PrevLogIndex-snapLastIndex] // 减去快照的索引偏移量
	// 优化逻辑：处理日志条目任期不匹配的情况
	// 当领导者尝试追加的日志条目与跟随者日志中的前一条条目任期不一致时，
	// 跟随者需要向领导者反馈冲突信息，以便领导者能够正确地调整日志复制策略。
	// 如果追随者的日志中有 preLogIndex，但是任期不匹配，它应该返回 conflictTerm = log[preLogIndex].Term，
	// 即返回与领导者提供的前一条日志条目任期不同的任期号。
	// 然后在它的日志中搜索任期等于 conflictTerm 的第一个条目索引。
	if args.PrevLogTerm != lastLog.Term {
		// 设置冲突任期号为跟随者日志中最后一条条目的任期号，
		// 这是因为跟随者日志的最后一条条目与领导者提供的前一条条目任期不一致。
		reply.ConflictTerm = lastLog.Term
		// 然后在它的日志中搜索任期等于 conflictTerm 的第一个条目索引。
		// 从 args.PrevLogIndex 开始向前遍历日志条目，直到找到任期不匹配的第一个条目，
		// 或者遍历到上个快照的最后一条日志的位置。
		for j := args.PrevLogIndex; j >= snapLastIndex; j-- {
			// 当找到任期不匹配的条目时，记录其下一个条目的索引作为冲突索引，
			// 这意味着从这个索引开始，日志条目需要被重新同步。
			if rf.logs[j-snapLastIndex].Term != lastLog.Term {
				reply.ConflictIndex = j + 1
				break
			}
		}
		// 完成冲突信息的收集后，返回处理结果，结束函数执行。
		return
	}
	reply.Success = true

	// 在PrevLogIndex处开始复制一份日志
	// 实现了 Raft 算法中跟随者接收到领导者发送的日志条目后，根据日志条目的任期号进行日志条目的复制或替换逻辑。
	// 这里要循环判断冲突再复制 不然可能由于滞后性删除了logs
	for idx := 0; idx < len(args.Entries); idx++ {
		// 计算当前条目在跟随者日志中的目标索引位置
		curIdx := idx + args.PrevLogIndex + 1 - snapLastIndex // 减去快照的索引偏移量
		// 检查当前条目索引是否超出跟随者日志的长度，或者
		// 日志条目的任期号与跟随者现有日志条目中的任期号是否不一致
		if curIdx >= len(rf.logs) || rf.logs[curIdx].Term != args.Entries[idx].Term {
			// 如果存在落后或者冲突，以当前领导者的日志为主，从当前位置开始，用领导者发送的日志条目替换或追加到跟随者日志中
			// 这里使用切片操作，保留原有日志的前半部分，然后追加领导者发送的日志条目
			rf.logs = append(rf.logs[:curIdx], args.Entries[idx:]...)
			// 替换或追加完毕后，跳出循环
			break
		}
	}
	// 更新commitIndex
	if args.LeaderCommit > rf.commitIndex {
		rf.commitIndex = min(rf.getLogLen(), args.LeaderCommit)
	}
}
```

#### 9、SendInstallSnapshotRpc 向指定节点发送安装快照的RPC请求

```go
// SendInstallSnapshotRpc 向指定节点发送安装快照的RPC请求。
// 这个方法是用来在Raft协议中传输快照数据的一部分，用于快速恢复节点状态，而不是从头开始复制日志。
// 参数:
//   id - 目标节点的ID，表示接收RPC请求的节点。
//   args - 安装快照的参数，包含快照的相关信息，如快照的偏移量、数据和是否完成传输等。
//   reply - RPC请求的回复，用于接收目标节点的响应信息，如是否成功接收快照等。
func (rf *Raft) SendInstallSnapshotRpc(id int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	ok := rf.peers[id].Call("Raft.InstallSnapshotHandler", args, reply)
	if !ok {
		return
	}
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if args.Term != rf.currentTerm {
		// 领导者的任期不匹配，忽略该响应
		return
	}

	if reply.Term > rf.currentTerm {
		// 跟随者任期大于领导者任期，领导者转换为追随者
		rf.ConvertToFollower(reply.Term)
	}

	snapShotIndex := rf.getFirstLog().Index
	// RPC任期不匹配、或者退位、或者快照下标对不上直接返回即可
	// 再检查一次，如果当前节点不是领导者，则忽略响应
	if rf.currentTerm != args.Term || rf.role != Leader || args.LastIncludedIndex != snapShotIndex {
		return
	}

	rf.nextIndex[id] = max(rf.nextIndex[id], args.LastIncludedIndex+1)
	rf.matchIndex[id] = max(rf.matchIndex[id], args.LastIncludedIndex)

	// 持久化状态以及快照
	rf.persister.SaveStateAndSnapshot(rf.EncoderState(), args.Data)
}
```

#### 10、InstallSnapshotHandler 处理接收到的安装快照请求

```go
// InstallSnapshotHandler 处理接收到的安装快照请求。
// 当一个节点接收到其他节点发送的快照时，此方法会被调用以更新其状态。
// 参数:
//   args - 安装快照请求的参数，包含快照数据、快照的最后包含的索引和任期等信息。
//   reply - 安装快照请求的回复，用于向发送方反馈处理结果。
func (rf *Raft) InstallSnapshotHandler(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer DPrintf("{Node %v}'s state is {state %v,Term %v,commitIndex %v,lastApplied %v,firstLog %v,lastLog %v} before processing InstallSnapshotRequest %v and reply InstallSnapshotResponse %v", rf.me, rf.role, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), args, reply)

	reply.Term = rf.currentTerm
	// 如果当前节点的任期大于请求的任期，则忽略请求
	if rf.currentTerm > args.Term {
		return
	}
	if rf.currentTerm < args.Term || (rf.currentTerm == args.Term && rf.role == Candidate) {
		// 如果当前节点的任期小于请求的任期，或者任期相等（收到了leader的请求）则转换为追随者
		rf.ConvertToFollower(args.Term)
	}

	// 发送AppendEntriesReply，确认快照接收成功
	rf.appendEntriesChan <- AppendEntriesReply{
		Term:    rf.currentTerm,
		Success: true,
	}
	// 如果请求中的快照最后包含的索引小于等于当前节点的已提交索引，则无需处理
	if args.LastIncludedIndex <= rf.commitIndex {
		return
	}

	// 全盘接收快照文件
	// 更新节点的commitIndex和lastApplied
	rf.commitIndex = args.LastIncludedIndex
	rf.lastApplied = args.LastIncludedIndex
	// 根据快照信息更新日志
	if rf.getLastLog().Index <= args.LastIncludedIndex {
		// 如果所有旧的日志都已被快照覆盖，创建新的日志条目
		rf.logs = []LogEntries{{
			Command: nil,
			Term:    args.LastIncludedTerm,
			Index:   args.LastIncludedIndex,
		}}
	} else {
		// 否则，保留快照之后的日志条目，并更新第一个日志条目的信息
		snapIndex := rf.getFirstLog().Index
		newLogs := make([]LogEntries, rf.getLastLog().Index-args.LastIncludedIndex+1)
		copy(newLogs, rf.logs[args.LastIncludedIndex-snapIndex:])
		rf.logs = newLogs
		rf.logs[0].Command = nil
		rf.logs[0].Term = args.LastIncludedTerm
		rf.logs[0].Index = args.LastIncludedIndex
	}
	// 持久化状态和快照
	rf.persister.SaveStateAndSnapshot(rf.EncoderState(), args.Data)
	// 异步发送ApplyMsg，通知应用层处理快照
	go func() {
		rf.applyChan <- ApplyMsg{
			SnapshotValid: true,
			Snapshot:      args.Data,
			SnapshotIndex: args.LastIncludedIndex,
			SnapshotTerm:  args.LastIncludedTerm,
		}
	}()
}
```

