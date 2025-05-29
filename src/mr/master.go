package mr

import (
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

// 管理任务的状态
// 处理worker的RPC请求
// 检测任务是否超时
type Task struct {
	FileName string
	Status   string
	Start    time.Time
	TaskID   int
}

type Master struct {
	// Your definitions here.
	mapTasks    []Task
	reduceTasks []Task
	nReduce     int
	mapFinished bool
	allFinished bool
	files       []string
	nextTaskID  int
	mu          sync.Mutex
}

// Your code here -- RPC handlers for the worker to call.

// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
func (m *Master) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

func (m *Master) GetTask(args *GetTaskArgs, reply *GetTaskReply) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 检查超时任务
	m.checkTimeOut()
	// 如果所有任务完成则通知worker退出
	if m.allFinished {
		reply.TaskType = ExitTask
		return nil
	}
	// 如果map任务还没有执行完，分配map任务
	if !m.mapFinished {
		for i, task := range m.mapTasks {
			if task.Status == Idle {
				reply.TaskType = MapTask
				reply.FileName = task.FileName
				reply.TaskID = task.TaskID
				reply.NReduce = m.nReduce
				// 更新任务状态
				m.mapTasks[i].Status = InProgress
				m.mapTasks[i].Start = time.Now()
				return nil
			}
		}
		// 如果找不到空闲map任务，并且还没有执行完，那么就让worker等待
		reply.TaskType = WaitTask
		return nil
	}
	// 如果所有map任务执行完了，则分配reduce任务
	for i, task := range m.reduceTasks {
		if task.Status == Idle {
			reply.TaskType = ReduceTask
			reply.TaskID = task.TaskID
			reply.ReduceTaskNum = i
			reply.MapTaskNum = len(m.mapTasks)

			// 更新任务状态
			m.reduceTasks[i].Status = InProgress
			m.reduceTasks[i].Start = time.Now()
			return nil
		}
	}
	// 如果没有找到空闲的reduce任务,还是要求 worker任务等待
	reply.TaskType = WaitTask
	return nil
}

func (m *Master) checkTimeOut() {
	// 超时时间设定为10s
	timeout := 10 * time.Second
	now := time.Now()
	// 检查map任务是否超时
	if !m.mapFinished {
		allCompleted := true
		for i, task := range m.mapTasks {
			if task.Status == InProgress && now.Sub(task.Start) > timeout {
				// 任务超时了
				m.mapTasks[i].Status = Idle
			}
			if task.Status != Completed {
				allCompleted = false
			}
		}
		m.mapFinished = allCompleted
	}
	// 检查reduce任务是否超时
	allCompleted := true
	for i, task := range m.reduceTasks {
		if task.Status == InProgress && now.Sub(task.Start) > timeout {
			m.reduceTasks[i].Status = Idle
		}
		if task.Status != Completed {
			allCompleted = false
		}
	}
	m.allFinished = allCompleted
}

func (m *Master) ReportTask(args *ReportTaskArgs, reply *ReportTaskReply) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if args.TaskType == MapTask {
		for i, task := range m.mapTasks {
			if task.TaskID == args.TaskID && task.Status == InProgress {
				// 更新任务状态为已完成
				m.mapTasks[i].Status = Completed
				// 检查所有map任务是否已经完成
				allCompleted := true
				for _, task := range m.mapTasks {
					if task.Status != Completed {
						allCompleted = false
						break
					}
				}
				// 更新map阶段完成标志
				m.mapFinished = allCompleted
				reply.OK = true
				return nil
			}
		}
	} else if args.TaskType == ReduceTask {
		for i, task := range m.reduceTasks {
			if task.TaskID == args.TaskID && task.Status == InProgress {
				m.reduceTasks[i].Status = Completed
				// 检查所有reduce任务是否已经完成
				allCompleted := true
				for _, task := range m.reduceTasks {
					if task.Status != Completed {
						allCompleted = false
						break
					}
				}
				// 更新整个作业完成标志
				m.allFinished = allCompleted
				reply.OK = true
				return nil
			}
		}
	}

	reply.OK = false
	return nil
}

// start a thread that listens for RPCs from worker.go
func (m *Master) server() {
	rpc.Register(m)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := masterSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

// main/mrmaster.go calls Done() periodically to find out
// if the entire job has finished.
func (m *Master) Done() bool {
	ret := false

	// Your code here.
	if m.allFinished {
		ret = true
	}

	return ret
}

// create a Master.
// main/mrmaster.go calls this function.
// nReduce is the number of reduce tasks to use.
func MakeMaster(files []string, nReduce int) *Master {
	m := Master{
		files:       files,
		nReduce:     nReduce,
		mapTasks:    make([]Task, len(files)),
		reduceTasks: make([]Task, nReduce),
		mapFinished: false,
		allFinished: false,
		nextTaskID:  0,
	}
	// 初始化map任务
	for i, file := range m.files {
		m.mapTasks[i] = Task{
			FileName: file,
			TaskID:   i,
			Status:   Idle,
		}
	}
	// 初始化reduce任务
	for i := 0; i < nReduce; i++ {
		m.reduceTasks[i] = Task{
			TaskID: i,
			Status: Idle,
		}
	}

	// Your code here.

	m.server()
	return &m
}
