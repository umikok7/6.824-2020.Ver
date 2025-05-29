package raft

import "time"

// RoleType 记录节点的三种状态
type RoleType int

// chan缓冲区长度
var chanLen = 10

// 选举超时时间为 1s
// 由于测试程序限制心跳频率为每秒10次，选举超时需要比论文的150到300毫秒更长，
// 但不能太长，以避免在五秒内未能选出领导者。
var electionTimeout = 300 * time.Millisecond

// var electionTimeout = 150 * time.Millisecond

// 心跳超时时间为200ms
var appendEntriesTimeout = 300 * time.Millisecond

// heartBeatInterval 心跳间隔 每1秒发送10次 领导者发送心跳RPC的频率不超过每秒10次
var heartBeatInterval = 100 * time.Millisecond

// var heartBeatInterval = 35 * time.Millisecond

// commitInterval rf节点提交日志的间隔时间
var commitInterval = 200 * time.Millisecond

// noVoted 表示没有投票，常量值为-1
const noVoted = -1

const (
	Leader    RoleType = iota // 0 领导者
	Candidate                 // 1 候选者
	Follower                  // 2 跟随者
)
