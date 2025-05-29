package mr

//
// RPC definitions.
//
// remember to capitalize all names.
//

import (
	"os"
	"strconv"
)

//
// example to show how to declare the arguments
// and reply for an RPC.
//

type ExampleArgs struct {
	X int
}

type ExampleReply struct {
	Y int
}

// Add your RPC definitions here.

// 任务类型常量
const (
	MapTask    = "map"
	ReduceTask = "reduce"
	ExitTask   = "exit"
	WaitTask   = "wait"
)

// 任务状态常量
const (
	Idle       = "idle"
	InProgress = "in-progress"
	Completed  = "completed"
)

// 请求任务
type GetTaskArgs struct {
	WorkerID int
}

type GetTaskReply struct {
	// map操作
	TaskID   int
	TaskType string
	FileName string
	// reduce操作
	MapTaskNum    int
	ReduceTaskNum int // reduce任务的编号，负责分区编号
	NReduce       int // 一共多少个reduce任务(桶)
}

// 汇报任务状态
type ReportTaskArgs struct {
	TaskType  string
	WorkerID  int
	TaskID    int
	Completed bool
}

type ReportTaskReply struct {
	OK bool
}

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the master.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func masterSock() string {
	s := "/var/tmp/824-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}
