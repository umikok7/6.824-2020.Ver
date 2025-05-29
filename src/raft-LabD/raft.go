package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"bytes"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"../labgob"
	"../labrpc"
)

// import "bytes"

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in Lab 3 you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh; at that point you can add fields to
// ApplyMsg, but set CommandValid to false for these other uses.
//

// 当每个Raft节点意识到连续的日志条目被提交时，
// 该节点应该通过传递给 Make() 的 applyCh 向同一服务器上的服务（或测试器）发送一个ApplyMsg。
// 将 CommandValid 设置为true以表明 ApplyMsg 包含一个新提交的日志条目。
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// for 2D
	SnapshotValid bool   // 表示Snapshot字段是否包含一个快照
	Snapshot      []byte // 包含快照的数据
	SnapshotTerm  int    // 快照的任期号
	SnapshotIndex int    // 快照的索引
}

// A Go object implementing a single Raft peer.
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	// 2A
	role              RoleType                // 记录当前节点的状态
	currentTerm       int                     // 节点当前任期
	voteFor           int                     // follower把票投给了哪个candidate
	voteCount         int                     // 记录获取选票的个数
	appendEntriesChan chan AppendEntriesReply // 心跳channel
	LeaderMsgChan     chan struct{}           // 当选Leader时发送
	voteMsgChan       chan struct{}           // 收到选举信号时重制一下计时器，不然会出现覆盖term后计时器超时又自增情况

	// 2B
	commitIndex int           // 记录已知被提交的最高目录条目索引，初始化为0，单调增
	lastApplied int           // 记录已经被应用于状态机的最高日志条目索引，初始化为0，单调增
	logs        []LogEntries  // 日志条目集合，每个条目包含状态机的命令及该条目被leader接收时的任期（日志首个索引为1）
	nextIndex   []int         // 针对每个服务器，记录下一个将发送给服务器的日志条目索引（初始化为领导者最后日志索引+1）
	matchIndex  []int         // 针对每个服务器，记录已知在该服务器上复制的最高日志条目索引（初始化为0，单调增）
	applyChan   chan ApplyMsg // 用于提交给客户端已经完成超过半数服务器复制成功的log处理结果

}

type LogEntries struct {
	/*
		Command字段存储日志条目关联的具体命令内容
		这是一个接口类型，允许存储任意类型的命令数据
	*/
	Command interface{}
	/*
		Term 表示该log是在哪一个term被创建或复制的
		这对于确定日志条目的新旧十分重要，是raft一致性算法的关键
	*/
	Term int
	/*
		Index用于表示该日志条目在日志中的序列号
		索引用于唯一标识每一日志，并且在日志匹配和日志压缩等操作起核心作用
	*/
	Index int
}

// 附加日志条目的RPC请求结构
type AppendEntriesArgs struct {
	Term         int
	LeaderId     int          // 领导者id
	PrevLogIdx   int          // 新日志条目之前的日志条目索引
	PrevLogTerm  int          // 新日志条目之前的日志条目任期
	LeaderCommit int          // leader’s commitIndex
	Entries      []LogEntries // 要存储的日志条目（为空表示心跳，可发多个以提高效率）
}

// 附加日志条目的RPC响应结构
type AppendEntriesReply struct {
	Term          int // 当前任期，用于更新领导者自己的任期
	Success       bool
	ConflictTerm  int // 在follower日志中与leader发送的日志条目发送冲突的那条日志的任期号
	ConflictIndex int // 在跟随者日志中发生冲突的具体条目的索引
}

// 定义了安装快照所需要的参数结构
type InstallSnapshotArgs struct {
	Term              int    // 发送快照的leader的当前的任期
	LeaderId          int    // Leader的标识符，帮助follower将客户端请求重定向到当前的leader
	LastIncludedIndex int    // 快照中包含的最后一个日志索引；该索引及其之前的所有日志条目都会被快照替代
	LastIncludedTerm  int    // LastIncludedIndex所属的任期，它确保快照包含最新的信息
	Data              []byte // 表示快照数据的原始字节切片，快照以分块的方式发送，从指定的偏移量开始
}

// 定义了follower对快照安装请求的响应结构
type InstallSnapshotReply struct {
	// Term 是当前follower的任期，leader接收到此响应后会检查该任期，以确认自己是否依旧是leader
	Term int
}

// applyLog 方法负责将已知提交的日志条目应用到状态机中。
// 这个方法检查是否有新的日志条目可以被应用，并将它们发送到applyChan，
// 以便状态机能够处理这些条目并更新其状态
func (rf *Raft) applyLog() {
	rf.mu.Lock()
	// 2D更新
	snapShotIndex := rf.getFirstLog().Index // 获取快照包含的最后一条日志的索引，即当前日志起始点
	// 检查是否有新的日志需要应用
	// 如果commitIndex不大于lastApplied，说明没有新的日志需要应用
	if rf.commitIndex <= rf.lastApplied {
		rf.mu.Unlock()
		return
	}

	copyLogs := make([]LogEntries, rf.commitIndex-rf.lastApplied)
	// 复制需要应用的日志条目到copyLogs中
	// copy(copyLogs, rf.logs[rf.lastApplied+1:rf.commitIndex+1])
	// 2D 更新
	copy(copyLogs, rf.logs[rf.lastApplied-snapShotIndex+1:rf.commitIndex-snapShotIndex+1])
	// 应用日志条目之后需要更新lastApplied
	rf.lastApplied = rf.commitIndex
	// 记得解锁，避免向通道发送消息的时候持有锁
	rf.mu.Unlock()

	// 遍历所有日志条目
	for _, logEntity := range copyLogs {
		rf.applyChan <- ApplyMsg{
			CommandValid: true,
			Command:      logEntity.Command, //新提交的日志条目
			CommandIndex: logEntity.Index,   // 新提交的日志条目索引
		}
	}

}

func (rf *Raft) doApplyWork() {
	for !rf.killed() {
		rf.applyLog() // 确保已提交的日志条目被及时用到状态机上
		time.Sleep(commitInterval)
	}
}

// 让leader节点根据Raft算法的规则检查并更新commitIndex
func (rf *Raft) checkCommitIndex() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// 2D更新
	snapShotIndex := rf.getFirstLog().Index // 获取快照包含的最后一条日志的索引，即当前日志的起始点

	// 从最后一条日志倒序遍历日志, 快照保存的日志不需要检查
	for idx := len(rf.logs) - 1; idx >= rf.commitIndex-snapShotIndex; idx-- {
		// 具体参考paper figure8，此处是简化版本
		// leader不可直接提交不属于leader任期的日志条目
		if rf.logs[idx].Term < rf.currentTerm || rf.role != Leader {
			return
		}
		count := 1 // 代表leader自己已提交
		// 遍历每一个服务器的matchIndx，如果大于等于当前遍历的日志索引，则表明该服务器已经成功复制日志，计数+1
		for i := range rf.matchIndex {
			if i != rf.me && rf.matchIndex[i] >= idx+snapShotIndex {
				count++
			}
		}
		// 如果计数大于半数，则更新commitIndex为当前遍历的日志索引，运行更多的日志条目被状态机使用
		if count > len(rf.peers)/2 {
			if rf.role == Leader {
				// 需要加上快照的最后一条索引
				rf.commitIndex = idx + snapShotIndex
			}
			break
		}
	}

}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	rf.mu.Lock()
	defer rf.mu.Unlock()
	var term int
	var isleader bool
	// Your code here (2A).
	term = rf.currentTerm
	if rf.role == Leader {
		isleader = true
	} else {
		isleader = false
	}
	return term, isleader
}

// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)

	// 2C
	// 初始化一个字节缓冲区用于序列化数据
	w := new(bytes.Buffer)
	// 创建一个labgob编码器，用于将Go对象编码为字节流
	e := labgob.NewEncoder(w)
	// 需要保存的内容，使用编码器将关键状态信息序列化到缓冲区
	// 如果编码过程中出现错误，则通过log.Fatal终止程序，防止数据损坏或不完整状态的保存
	if e.Encode(rf.currentTerm) != nil || e.Encode(rf.voteFor) != nil || e.Encode(rf.logs) != nil {
		log.Fatal("Errors occur when encode the data!")
	}
	// 序列化后，从缓冲区获取字节切片准备存储
	data := w.Bytes()
	// 调用persister的SaveRaftState方法，将序列化后的数据保存到稳定的存储介质中
	rf.persister.SaveRaftState(data)

}

// restore previously persisted state.
func (rf *Raft) readPersist(data []byte) {

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if data == nil || len(data) < 1 { // bootstrap without any state?
		// 如果没有数据或者数据长度不足，可能是初次启动无需恢复状态
		return
	}

	// 初始化字节缓冲区以读取数据
	r := bytes.NewBuffer(data)
	// 创建一个labgob解码器，将字节流转回Go对象
	d := labgob.NewDecoder(r)

	// 定义变量以接收解码后的状态信息
	var currentTerm int
	var voteFor int
	var logs []LogEntries

	// 按编码的顺序使用解码器从字节流中读取并还原状态信息
	// 如果解码过程中出现错误，通过log.Fatal终止程序，防止使用损坏或者不完整的状态信息
	if d.Decode(&currentTerm) != nil || d.Decode(&voteFor) != nil || d.Decode(&logs) != nil {
		log.Fatal("Errors occur when decode the data!")
	} else {
		// 解码成功后，将读取的状态信息赋值给Raft实例对应的字段
		rf.currentTerm = currentTerm
		rf.voteFor = voteFor
		rf.logs = logs
		// 2D 更新，读取了快照后，最后一条应用的日志是快照保存的最后一条日志条目,也就是即当前日志的起始点
		snapShotIndex := rf.getFirstLog().Index
		rf.lastApplied = snapShotIndex
	}

	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

// CondInstallSnapshot
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
// 旧接口不需要实现
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).

	return true
}

// Snapshot the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
// Snapshot 接收服务层的通知，表明已经创建了一个包含截至index的所有信息的快照。
// 这意味着服务层不再需要通过（包括）该index的日志。Raft应尽可能地修剪其日志。
// 参数:
//
//	index - 快照所包含的最后日志的索引。
//	snapshot - 快照数据。
//
// 功能： 服务层触发快照创建
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.trimIndex(index) // 修剪日志
	// 保存状态和日志
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	if e.Encode(rf.currentTerm) != nil || e.Encode(rf.voteFor) != nil || e.Encode(rf.logs) != nil {
		log.Fatal("Errors occur when encode the data!")
	}
	data := w.Bytes()
	rf.persister.SaveStateAndSnapshot(data, snapshot)
}

// trimIndex 修剪日志，移除快照所包含的所有日志条目。
// 参数:
//
//	index - 快照所包含的最后日志的索引。
func (rf *Raft) trimIndex(index int) {
	// 获取第一个日志条目的索引
	snapShotIndex := rf.getFirstLog().Index
	// 如果日志的起始索引已经大于或等于要修剪的索引，则无需修剪
	if snapShotIndex >= index {
		return
	}
	// rf.logs[0]保留快照的lastLog，快照中包含的最后一条日志也会被保存下来而不会被修剪
	// 释放大切片内存
	rf.logs = append([]LogEntries{}, rf.logs[index-snapShotIndex:]...)
	rf.logs[0].Command = nil // 第一个日志的Command置为nil，表示这是快照点
}

// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type RequestVoteArgs struct {
	// Your data here (2A, 2B).

	// 2A 2B
	Term        int // 候选人任期
	CandidateId int // 请求投票的候选人
	LastLogIdx  int // 候选人的最后一个日志输入索引
	LastLogTerm int // 候选人的最后一个日志输入任期
}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int  // 当前的任期，用于候选人更新自己的任期
	VoteGranted bool // 是否投票给了候选人， true 代表候选人收到了投票
}

// sendAllRequestVote 向其他 Raft 节点发送请求投票的 RPC
func (rf *Raft) sendAllRequestVote() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// 2B 新增参数，给其他节点在处理投票的时候比较逻辑：论文中规定term更大log更长的节点优先当选
	// 候选人选择在投票请求中包含已提交的最后一条日志而不是其日志的最后一条是出于安全性大于可用性的考量的
	//lastLog := rf.logs[rf.commitIndex]
	lastLog := rf.getLastLog()
	// 首先构建请求投票的参数
	args := &RequestVoteArgs{
		Term:        rf.currentTerm, // 当前任期
		CandidateId: rf.me,
		LastLogIdx:  lastLog.Index,
		LastLogTerm: lastLog.Term,
	}

	// 向所有其他节点发送请求投票的rpc
	for i := range rf.peers {
		// 对于非当前节点的其他节点发送，并且本身为候选者状态
		if i != rf.me && rf.role == Candidate {
			go func(id int) {
				// 接收返回参数
				ret := &RequestVoteReply{}
				// 调用rpc方法，注意！此处是在向其他节点发送RPC请求
				rf.sendRequestVote(id, args, ret)
			}(i)
		}
	}
}

// example RequestVote RPC handler.
// 处理RequestVote RPC请求的处理函数
// 重要概念：RequestVoteHandler 运行在接收投票请求的节点上，而不是发起投票的节点上
// 任期比较:
// 如果请求者的任期大于当前节点的任期，说明请求者的信息更新，当前节点需要更新其任期并转换为Follower角色。
// 如果请求者的任期小于当前节点的任期，则当前节点拒绝投票，因为其任期更大，更“新”。
func (rf *Raft) RequestVoteHandler(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	// 加锁锁定当前Raft实例，以保证并发安全
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()

	// 设置返回的任期，投票默认拒绝返回false
	reply.Term = rf.currentTerm

	// 请求节点（候选者）的任期大于当前节点的任期
	if args.Term > rf.currentTerm {
		// 将当前节点的转化为follower角色，并且更新任期为请求者的任期
		rf.ConvertToFollowerNoLock(args.Term)
		// 向通道发送消息，通知其他部分此处有投票事件发生
		rf.voteMsgChan <- struct{}{}
	}

	// // 请求节点的任期小于当前节点的任期或者 同一任期内 当前节点已经给其他人投过票了且不是投给的他，则直接拒绝投票
	// if args.Term < rf.currentTerm || (args.Term == rf.currentTerm && rf.voteFor != -1 && rf.voteFor != args.CandidateId) {
	// 	reply.Term, reply.VoteGranted = rf.currentTerm, false
	// 	return
	// }

	// 双向影响，不接受任期小于的Candidate的投票申请
	if args.Term < rf.currentTerm {
		return
	}

	// 2B
	lastLog := rf.getLastLog() // 获取了跟随者当前日志的最后一条日志条目
	// 这个条件判断是决定是否投票给候选者的依据，遵循了Raft论文中规定的规则：当候选者的日志不如跟随者的新时，跟随者不应投票给该候选者。具体来说：
	// 如果候选者的最后一条日志任期号（args.LastLogTerm）小于跟随者的最后一条日志任期号（lastLog.Term），则候选者的日志被认为整体较老。
	// 如果候选者的最后一条日志任期号等于跟随者的，但候选者的最后一条日志索引（args.LastLogIndex）小于跟随者的，这也表明候选者的日志不够新，因为同任期下索引大的日志更晚产生。
	if args.LastLogTerm < lastLog.Term || (args.LastLogTerm == lastLog.Term && args.LastLogIdx < lastLog.Index) {
		reply.Term, reply.VoteGranted = rf.currentTerm, false
		return
	}

	// 如果当前节点是Follwer，并且当前节点未投票或者已经投票给该候选人
	if rf.role == Follower && (rf.voteFor == noVoted || rf.voteFor == args.CandidateId) {
		// 更新当前节点的投票状态为：投给了候选人ID
		rf.voteFor = args.CandidateId
		// 同意投票
		reply.VoteGranted = true
		reply.Term = args.Term
		// 向通道发送消息，通知其他部分此处有投票事件发生
		rf.voteMsgChan <- struct{}{}
	}
}

// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	// server下标节点调用RequestVoteHandler函数
	// 1阶段：发起RPC请求
	ok := rf.peers[server].Call("Raft.RequestVoteHandler", args, reply)
	if !ok {
		return false
	}
	// 2阶段：处理RPC响应
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// 2C
	// 函数退出时，调用持久化方法将当前节点的状态持久化到磁盘中
	defer rf.persist()

	// 在分布式系统中，状态可能在任何时候改变。当 sendRequestVote 发出请求后，
	// 到收到响应前这段时间里，节点的状态可能已经发生了变化
	// 如果该节点不再是候选者，或者请求节点的任期和当前任期不一致，则直接返回true，无需继续拉票
	if rf.role != Candidate || args.Term != rf.currentTerm {
		// 因为已经成为Leader或者重制为candidate或follower了
		return true
	}
	// 如果收到回复中的任期比当前节点任期大，则转化为follower
	if reply.Term > rf.currentTerm {
		rf.ConvertToFollowerNoLock(reply.Term) // 注意此处，会引起需要持久化的状态变化（currentTerm 和 voteFor）因此此函数中需要进行持久化操作
		rf.voteMsgChan <- struct{}{}
		return true
	}
	// 如果投票被授予了但还是候选者，那么就增加票数
	if reply.VoteGranted && rf.role == Candidate {
		rf.voteCount++
		// 如果获得了过半的票且还是候选者，那么变为leader
		if 2*rf.voteCount > len(rf.peers) && rf.role == Candidate {
			rf.ConvertToLeaderNoLock()
			rf.LeaderMsgChan <- struct{}{}
		}
	}

	return true
}

// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	if rf.role != Leader {
		return -1, -1, false
	}
	// 如果是leader，则将新命令追加到日志中，并返回追加日志索引和任期，以及一个表明当前节点领导身份的标志
	newLog := rf.appendLog(command) // 一次追加一条日志
	term = newLog.Term
	index = newLog.Index

	return index, term, isLeader
}

// appendLog 函数负责将新的命令日志条目追加到当前Raft节点的日志中。
func (rf *Raft) appendLog(command interface{}) LogEntries {
	// 初始化新日志条目，使用当前任期和日志的下一个有效索引
	newLog := LogEntries{
		Command: command,
		Term:    rf.currentTerm,
		Index:   rf.getLastLog().Index + 1,
	}
	// 将新日志追加在日志列表中
	rf.logs = append(rf.logs, newLog)
	return newLog
}

// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{
		mu:        sync.Mutex{},
		peers:     peers,
		persister: persister,
		me:        me,
		dead:      0,
	}

	// Your initialization code here (2A, 2B, 2C).

	// 2A
	rf.role = Follower
	rf.currentTerm = 0
	rf.voteFor = noVoted
	rf.voteCount = 0
	rf.appendEntriesChan = make(chan AppendEntriesReply, chanLen)
	rf.LeaderMsgChan = make(chan struct{}, chanLen)
	rf.voteMsgChan = make(chan struct{}, chanLen)

	// 2B
	rf.commitIndex = 0
	rf.lastApplied = 0
	rf.logs = []LogEntries{{}}
	rf.nextIndex = make([]int, len(peers))
	rf.matchIndex = make([]int, len(peers))
	rf.applyChan = applyCh

	// initialize from state persisted before a crash
	// 从崩溃前保存的状态进行持久化
	rf.readPersist(persister.ReadRaftState())

	// 启动ticker协程开始选举
	go rf.ticker()
	// 启动协程，定时将日志应用在状态机当中
	go rf.doApplyWork()

	return rf
}

func (rf *Raft) ticker() {
	// 直到dead置为1后才退出循环
	for !rf.killed() {
		rf.mu.Lock()
		currentRole := rf.role
		rf.mu.Unlock()
		switch currentRole {
		case Candidate:
			select {
			case <-rf.voteMsgChan:
				// 一旦进入这个分支，那么将退出循环，实现对选举超时计时器的重制

			case resp := <-rf.appendEntriesChan:
				if resp.Term >= rf.currentTerm {
					// 关键点：候选者收到了更大任期的leader心跳信息或者日志复制消息后，需要转化为follower
					rf.ConvertToFollower(resp.Term)
					continue
				}
			case <-time.After(electionTimeout + time.Duration(rand.Int31()%300)*time.Millisecond):
				// 选举超时 重制一下选举状态
				if rf.role == Candidate {
					rf.ConvertToCandidate()
					// 发起投票请求
					go rf.sendAllRequestVote()
				}
			case <-rf.LeaderMsgChan:
			}
		case Leader:
			// Leader负责定期发送心跳与同步日志
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
			// 如果是跟随者，则等待不同事件的发生
			select {
			case <-rf.voteMsgChan:
				// 收到投票消息，继续等待

			case resp := <-rf.appendEntriesChan:
				if resp.Term > rf.currentTerm {
					rf.ConvertToFollower(resp.Term)
					continue
				}
			case <-time.After(appendEntriesTimeout + time.Duration(rand.Int31()%300)*time.Millisecond):
				// 附加日志条目超时，转换为候选人，发起选举
				// 增加扰动，避免多个Candidate同时进入选举
				rf.ConvertToCandidate()
				// 发起投票请求
				go rf.sendAllRequestVote()
			}
		}
	}
}

// SendAllAppendEntries 用于给Leader向其他所有节点调用来复制日志条目，也用作heartbeat
func (rf *Raft) SendAllAppendEntries() {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	for server := range rf.peers {
		// 对于每个不是当前节点的节点，leader启动一个新的goroutine来发送AppendEntries请求
		if server != rf.me && rf.role == Leader {
			// 2B
			// 向follower发送nextIndex最新log日志
			// 如果无需更新 则发送心跳即可。
			// 为了效率 可以一次发送多份
			// 这一段要在锁内处理，防止越界。
			nextId := rf.nextIndex[server] // 获取Leader对于第id个服务器的下一个要发送的日志id
			firstLog := rf.getFirstLog()   // 获取leader的第一条日志

			// 此时检查是否有日志需要发送，有则发送appendEntries信号，让节点复制日志，否则直接发送快照文件，让子节点复制
			if nextId > firstLog.Index {
				// 减去快照的最后一条日志索引，得到相对于快照的在当前日志切片中的下一条需要同步的日志索引
				nextId -= firstLog.Index
				if nextId <= 0 || nextId > len(rf.logs) {
					DPrintf("Server %d: 越界了，给节点 %d 发送的日志索引超出范围: nextId=%d, logs长度=%d, 原始nextId=%d, 快照索引=%d",
						rf.me, server, nextId, len(rf.logs), nextId+firstLog.Index, firstLog.Index)
				}
				lastlLog := rf.logs[nextId-1] // 获取上次发送给follower的最后一条日志条目
				// 创建一个新的切片存放需要同步给follower的尚未同步的日志条目
				logs := make([]LogEntries, len(rf.logs)-nextId)
				copy(logs, rf.logs[nextId:]) // 拷贝尚未同步的日志
				// 构建AppendEntriesArgs结构体，准备发送日志条目
				args := &AppendEntriesArgs{
					Term:         rf.currentTerm,
					LeaderId:     rf.me,
					PrevLogIdx:   lastlLog.Index,
					PrevLogTerm:  lastlLog.Term,
					LeaderCommit: rf.commitIndex,
					Entries:      logs,
				}
				// 异步发送请求给follower
				go func(id int, args *AppendEntriesArgs) {
					reply := &AppendEntriesReply{}
					rf.SendAppendEntries(id, args, reply)
				}(server, args)
			} else {
				// 如果follower落后太多，发送快照
				// 下一条要发送的日志索引小于快照的最后一条日志索引，说明需要全部日志都通过快照保存
				// 构建InstallSnapshotArgs结构体，准备直接发送新建的快照
				args := &InstallSnapshotArgs{
					Term:              rf.currentTerm,
					LeaderId:          rf.me,
					LastIncludedIndex: firstLog.Index,
					LastIncludedTerm:  firstLog.Term,
					Data:              rf.persister.ReadSnapshot(),
				}
				// 异步发送给follower
				go func(id int, args *InstallSnapshotArgs) {
					reply := &InstallSnapshotReply{}
					rf.SendInstallSnapshotRpc(id, args, reply)
				}(server, args)
			}

		}
	}

}

// 向指定的节点发送AppendEntries RPC请求
// rf->leader, args->follower
func (rf *Raft) SendAppendEntries(id int, args *AppendEntriesArgs, reply *AppendEntriesReply) {
	ok := rf.peers[id].Call("Raft.AppendEntriesHandler", args, reply)
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
	// 如果当前节点不再是leader则直接返回
	if rf.role != Leader {
		return
	}
	// 如果响应的任期大于当前的任期，当前节点转化为follower
	if reply.Term > rf.currentTerm {
		rf.ConvertToFollowerNoLock(reply.Term) // 此处修改了currentTerm 和 voteFor
	}

	// 2B
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
			// 处理日志长度不正确的响应
			// leader收到响应后要更新下一个日志条目索引为冲突索引（follwer日志太短，直接跳到follower自身的日志长度）
			rf.nextIndex[id] = reply.ConflictIndex
		} else {
			// 2D更新
			snapLastIndex := rf.getFirstLog().Index
			// 处理日志任期不匹配的响应
			flag := true
			for j := len(rf.logs) - 1; j >= 0; j-- {
				// 找到冲突任期的最后一条日志（冲突任期号为跟随者日志中的最后一条条目的任期号）
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
		rf.nextIndex[id] = max(args.PrevLogIdx+len(args.Entries)+1, rf.nextIndex[id])

		// 更新（已匹配索引）matchIndex[id]为最新的已知被复制到跟随者的日志索引，
		// PrevLogIndex + 新追加的日志条目数量
		// 这有助于领导者跟踪每个跟随者已复制日志的最大索引
		rf.matchIndex[id] = max(args.PrevLogIdx+len(args.Entries), rf.matchIndex[id])
	}

}

// AppendEntriesHandler 由Leader向每个其余节点发送
// rf->follower, args->leader
// 处理来自领导者的 AppendEntries RPC 请求
// 处理接收到的 AppendEntries 请求，包括心跳和日志条目的复制
func (rf *Raft) AppendEntriesHandler(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// 函数退出前持久化状态
	// 由于有改变持久化状态，所以需要调用该函数
	defer rf.persist()

	// 初始化响应任期为当前任期
	reply.Term = rf.currentTerm

	// 解决分区恢复后的脑裂问题，通过判断任期的大小来确保只有一个领导者存活
	if rf.currentTerm > args.Term {
		return
	}
	// 收到Leader更高的任期时，更新自己的任期，转为 leader 的追随者
	if rf.currentTerm < args.Term || (rf.currentTerm == args.Term && rf.role == Candidate) {
		rf.ConvertToFollowerNoLock(args.Term) // 此处修改了currentTerm和voteFor
	}

	// 每次接收有效心跳 → 通过channel通知主goroutine重置选举超时计时器
	// 避免不必要的选举产生，维持系统稳定
	rf.appendEntriesChan <- AppendEntriesReply{Term: rf.currentTerm, Success: true}

	// 2B
	// 该检查是为了确保领导者请求的前一个日志条目索引没有超过跟随者当前日志的实际长度。
	// 如果args.PrevLogIndex大于跟随者日志的长度，这意味着领导者认为跟随者应该有一个比实际更长的日志
	// 直接理解成：leader要追加的日志的前一个日志索引比follwer日志的全长还要长，那肯定不得行，会产生冲突
	// 领导者收到这样的失败响应后，会根据跟随者的反馈调整其nextIndex值，然后重试发送AppendEntries请求，从一个更早的索引开始，
	// 以解决日志不一致的问题。这样，通过一系列的尝试与调整，Raft算法能够最终确保集群间日志的一致性。
	/*
		检查前一条日志是否存在
	*/
	if args.PrevLogIdx >= rf.getLogLen() {
		reply.ConflictTerm = -1 // 表示冲突的原因在于长度而并非任期不匹配
		reply.ConflictIndex = rf.getLogLen()
		return
	}
	// 2D更新
	snapLastIndex := rf.getFirstLog().Index

	// 新增：检查 PrevLogIdx 是否小于快照的最后索引
	if args.PrevLogIdx < snapLastIndex {
		reply.ConflictTerm = -1
		reply.ConflictIndex = snapLastIndex
		return
	}
	// 安全访问：确保索引在有效范围内
	logIndex := args.PrevLogIdx - snapLastIndex
	if logIndex < 0 || logIndex >= len(rf.logs) {
		reply.ConflictTerm = -1
		reply.ConflictIndex = rf.getLogLen()
		return
	}

	// 当领导者尝试追加的日志条目与跟随者日志中的前一条条目任期不一致时，
	// 跟随者需要向领导者反馈冲突信息，以便领导者能够正确地调整日志复制策略。
	// 如果追随者的日志中有 preLogIndex，但是任期不匹配，它应该返回 conflictTerm = log[preLogIndex].Term，
	// 即返回与领导者提供的前一条日志条目任期不同的任期号。
	// 然后在它的日志中搜索任期等于 conflictTerm 的第一个条目索引。
	/*
		检查前一条日志的任期是否匹配
	*/
	// lastLog := rf.logs[args.PrevLogIdx-snapLastIndex] // 减去快照的索引偏移量
	lastLog := rf.logs[logIndex]
	if args.PrevLogTerm != lastLog.Term {
		// 设置冲突任期号为跟随者日志中最后一条条目的任期号，
		// 这是因为跟随者日志的最后一条条目任期与领导者提供的前一条条目任期不一致。
		reply.ConflictTerm = lastLog.Term
		// 然后在它的日志中搜索任期等于 conflictTerm 的第一个条目索引。
		// 从 args.PrevLogIndex 开始向前遍历日志条目，直到找到任期不匹配的第一个条目，
		// 或者遍历到上个快照的最后一条日志的位置。
		for j := args.PrevLogIdx; j >= snapLastIndex; j-- {
			// 找到任期不匹配的条目时，记录其下一个条目的索引作为冲突索引，
			// 这意味着从此索引开始，日志条目需要重新同步
			if rf.logs[j-snapLastIndex].Term != lastLog.Term {
				reply.ConflictIndex = j + 1
				break
			}
		}
		// 完成冲突信息采集后，返回处理结果，结束函数的执行
		return
	}
	// 通过了一致性检查之后，进行日志复制
	reply.Success = true

	// 在PrevLogIndex处开始复制一份日志
	// 实现了 Raft 算法中跟随者接收到领导者发送的日志条目后，根据日志条目的任期号进行日志条目的复制或替换逻辑。
	// 这里要循环判断冲突再复制 不然可能由于滞后性删除了logs
	for idx := 0; idx < len(args.Entries); idx++ {
		// 计算当前条目在跟随者日志中的目标索引位置
		// args.PrevLogIdx 是前一个日志条目的索引（已经通过一致性检查），idx是leader发送的条目数组中的偏移量
		// 如果 args.PrevLogIdx = 5，那么第一个新日志条目(idx=0)应该放在跟随者日志的索引位置 6。
		curIdx := idx + args.PrevLogIdx + 1 - snapLastIndex // 减去快照的索引量
		// 检查当前条目索引是否超出跟随者日志的长度，或者
		// 日志条目的任期号与跟随者现有日志条目中的任期号是否不一致
		if curIdx >= len(rf.logs) || rf.logs[curIdx].Term != args.Entries[idx].Term {
			// 如果存在落后或者冲突，以当前领导者的日志为主，从当前位置开始，用领导者发送的日志条目替换或追加到跟随者日志中
			// 这里使用切片操作，保留原有日志的前半部分，然后追加领导者发送的日志条目
			// 也就是说：保留跟随者日志中从开始到 curIdx-1 的部分（这部分已经通过一致性检查）从 curIdx 开始，
			// 用领导者的日志条目(args.Entries[idx:])替换跟随者的所有后续日志
			rf.logs = append(rf.logs[:curIdx], args.Entries[idx:]...) // 此处修改了logs
			// 替换或追加完毕后，跳出循环
			break
		}
	}

	// // 领导者尝试让跟随者追加的日志条目范围完全落在跟随者已知的已提交日志区间内，那就不需要再复制了
	// // 因为领导者应当总是尝试追加在其已知提交日志之后的新日志，或者是修复不一致的日志。
	// if args.PrevLogIdx+len(args.Entries) < rf.commitIndex {
	// 	return
	// }
	// rf.logs = append(rf.logs[:args.PrevLogIdx+1], args.Entries...)

	// 更新commitIndex
	// args.LeaderCommit是领导者告知的已提交日志的最高索引。
	// 跟随者需要确保自己的commitIndex至少达到这个值，以保证整个集群的一致性。
	if args.LeaderCommit > rf.commitIndex {
		rf.commitIndex = min(rf.getLogLen(), args.LeaderCommit)
	}

}

// SendInstallSnapshotRpc 向指定节点发送安装快照的RPC请求。
// 这个方法是用来在Raft协议中传输快照数据的一部分，用于快速恢复节点状态，而不是从头开始复制日志。
// rf->leader args->follower
// 参数:
//
//	id - 目标节点的ID，表示接收RPC请求的节点。
//	args - 安装快照的参数，包含快照的相关信息，如快照的偏移量、数据和是否完成传输等。
//	reply - RPC请求的回复，用于接收目标节点的响应信息，如是否成功接收快照等。
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
		rf.ConvertToFollowerNoLock(reply.Term)
	}

	snapShotIndex := rf.getFirstLog().Index
	// RPC任期不匹配、或者退位、或者快照下标对不上直接返回即可
	// 再检查一次，如果当前节点不是领导者，则忽略响应
	if rf.currentTerm != args.Term || rf.role != Leader || args.LastIncludedIndex != snapShotIndex {
		return
	}
	// 更新该Follower的日志复制进度
	rf.nextIndex[id] = max(rf.nextIndex[id], args.LastIncludedIndex+1)
	rf.matchIndex[id] = max(rf.matchIndex[id], args.LastIncludedIndex)

	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	if e.Encode(rf.currentTerm) != nil || e.Encode(rf.voteFor) != nil || e.Encode(rf.logs) != nil {
		log.Fatal("Errors occur when encode the data!")
	}
	// 序列化后，从缓冲区获取字节切片准备存储
	data := w.Bytes()
	// 持久化状态和快照
	rf.persister.SaveStateAndSnapshot(data, args.Data)
}

// InstallSnapshotHandler 处理接收到的安装快照请求。
// 当一个节点接收到其他节点发送的快照时，此方法会被调用以更新其状态。
// rf->follower, args->leader
// 参数:
//
//	args - 安装快照请求的参数，包含快照数据、快照的最后包含的索引和任期等信息。
//	reply - 安装快照请求的回复，用于向发送方反馈处理结果。
func (rf *Raft) InstallSnapshotHandler(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Term = rf.currentTerm
	// 如果当前节点的任期大于请求的任期，则忽略请求
	if rf.currentTerm > args.Term {
		return
	}
	if rf.currentTerm < args.Term || (rf.currentTerm == args.Term && rf.role == Candidate) {
		// 如果当前节点的任期小于请求的任期，或者任期相等（收到了leader的请求）则转换为追随者
		rf.ConvertToFollowerNoLock(args.Term)
	}

	// 发送AppendEntriesReply，确认快照接收成功
	rf.appendEntriesChan <- AppendEntriesReply{
		Term:    rf.currentTerm,
		Success: true,
	}
	// 如果请求中的快照最后包含的索引小于等于当前节点的已提交索引，则无需处理
	// 也就是说快照不比当前状态新，那就忽略
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
		snapIndex := rf.getFirstLog().Index // 快照的最后一个日志索引
		newLogs := make([]LogEntries, rf.getLastLog().Index-args.LastIncludedIndex+1)
		copy(newLogs, rf.logs[args.LastIncludedIndex-snapIndex:])
		rf.logs = newLogs
		rf.logs[0].Command = nil
		rf.logs[0].Term = args.LastIncludedTerm
		rf.logs[0].Index = args.LastIncludedIndex
	}

	// 持久化状态和快照
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	if e.Encode(rf.currentTerm) != nil || e.Encode(rf.voteFor) != nil || e.Encode(rf.logs) != nil {
		log.Fatal("Errors occur when encode the data!")
	}
	data := w.Bytes()
	rf.persister.SaveStateAndSnapshot(data, args.Data)
	// 异步发送ApplyMsg，通知应用层处理快照
	go func() {
		rf.applyChan <- ApplyMsg{
			SnapshotValid: true,
			Snapshot:      args.Data,
			SnapshotTerm:  args.LastIncludedTerm,
			SnapshotIndex: args.LastIncludedIndex,
		}
	}()
}

// 该函数在转换过程中会更新节点的状态，包括角色、任期、投票信息
func (rf *Raft) ConvertToCandidate() {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.role = Candidate
	// 自身任期增加
	rf.currentTerm++
	// 投票给自己
	rf.voteFor = rf.me
	rf.voteCount = 1
}

func (rf *Raft) ConvertToFollower(term int) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.role = Follower
	rf.currentTerm = term
	// 重制投票为 尚未投票
	rf.voteFor = noVoted
	rf.voteCount = 0
}

// 创建一个无锁版本的以备不时之需
func (rf *Raft) ConvertToFollowerNoLock(term int) {
	rf.role = Follower
	rf.currentTerm = term
	// 重制投票为 尚未投票
	rf.voteFor = noVoted
	rf.voteCount = 0
}

func (rf *Raft) ConvertToLeader() {
	rf.mu.Lock()
	rf.nextIndex = make([]int, len(rf.peers))
	rf.matchIndex = make([]int, len(rf.peers))
	// 重制nextindex和matchindex数组，并将其初始化为当前节点的日志长度
	for i := range rf.nextIndex {
		rf.nextIndex[i] = rf.getLastLog().Index + 1
	}

	rf.role = Leader
	rf.mu.Unlock()
}

func (rf *Raft) ConvertToLeaderNoLock() {
	rf.nextIndex = make([]int, len(rf.peers))
	rf.matchIndex = make([]int, len(rf.peers))
	// 重制nextindex和matchindex数组，并将其初始化为当前节点的日志长度
	for i := range rf.nextIndex {
		rf.nextIndex[i] = rf.getLastLog().Index + 1
	}
	rf.role = Leader
}

func (rf *Raft) getLastLog() LogEntries {
	return rf.logs[len(rf.logs)-1]
}

func (rf *Raft) getFirstLog() LogEntries {
	return rf.logs[0]
}

func (rf *Raft) getLogLen() int { return rf.getLastLog().Index + 1 }
