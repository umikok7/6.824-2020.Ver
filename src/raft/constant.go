package raft

import (
	"time"
)

type RoleType int

const (
	Follower RoleType = iota //0
	Candidate
	Leader
)

var chanLen = 10

// 选举超时时间为 1s
// 由于测试程序限制心跳频率为每秒10次，选举超时需要比论文的150到300毫秒更长，
// 但不能太长，以避免在五秒内未能选出领导者。
var electionTimeout = 300 * time.Millisecond

// 超时时间为200ms
var appendEntriesTimeout = 300 * time.Millisecond

// 心跳间隔，每隔1s发送10次
var HeartBeatInterval = 100 * time.Millisecond

// commitInterval rf节点提交日志的间隔时间
var commitInterval = 200 * time.Millisecond

const noVoted = -1
