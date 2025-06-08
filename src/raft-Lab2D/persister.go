package raft

//
// support for Raft and kvraft to save persistent
// Raft state (log &c) and k/v server snapshots.
//
// we will use the original persister.go to test your code for grading.
// so, while you can modify this code to help you debug, please
// test with the original before submitting.
//
// 支持 Raft 和 kvraft 保存持久化的 Raft 状态（日志等）和键值服务器快照。
//
// 我们将使用原始的 persister.go 来测试你的代码进行评分。
// 因此，虽然你可以修改此代码来帮助调试，但请在提交之前使用原始代码进行测试。
//

import "sync"

// Persister 结构体用于保存 Raft 状态和键值服务器快照
type Persister struct {
	mu        sync.Mutex // 互斥锁保护对共享状态的访问
	raftstate []byte     // 持久化存储的 Raft 状态
	snapshot  []byte     // 持久化存储的键值服务器快照
}

// MakePersister 创建一个新的 Persister 实例
func MakePersister() *Persister {
	return &Persister{}
}

// clone 创建一个原始字节切片的副本
func clone(orig []byte) []byte {
	x := make([]byte, len(orig))
	copy(x, orig)
	return x
}

// Copy 创建并返回 Persister 的一个副本
func (ps *Persister) Copy() *Persister {
	// 加锁
	ps.mu.Lock()
	defer ps.mu.Unlock()
	np := MakePersister()
	np.raftstate = ps.raftstate
	np.snapshot = ps.snapshot
	return np
}

// SaveRaftState 保存 Raft 状态到持久化存储
func (ps *Persister) SaveRaftState(state []byte) {
	// 加锁
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.raftstate = clone(state)
}

// ReadRaftState 读取持久化存储的 Raft 状态
func (ps *Persister) ReadRaftState() []byte {
	// 加锁保证一致性
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return clone(ps.raftstate)
}

// RaftStateSize 返回持久化存储的 Raft 状态的大小
func (ps *Persister) RaftStateSize() int {
	// 加锁
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return len(ps.raftstate)
}

// SaveStateAndSnapshot 将 Raft 状态和键值服务器快照一起保存为一个原子操作，避免它们不同步
// Save both Raft state and K/V snapshot as a single atomic action,
// to help avoid them getting out of sync.
func (ps *Persister) SaveStateAndSnapshot(state []byte, snapshot []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.raftstate = clone(state)
	ps.snapshot = clone(snapshot)
}

// ReadSnapshot 读取持久化存储的键值服务器快照（副本）
func (ps *Persister) ReadSnapshot() []byte {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return clone(ps.snapshot)
}

// SnapshotSize 返回持久化存储的键值服务器快照的大小
func (ps *Persister) SnapshotSize() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return len(ps.snapshot)
}

func (rf *Raft) GetRaftStateSize() int {
	return rf.persister.RaftStateSize()
}
