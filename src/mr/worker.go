package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/ioutil"
	"log"
	"net/rpc"
	"os"
	"sort"
	"time"
)

// Map functions return a slice of KeyValue.
type KeyValue struct {
	Key   string
	Value string
}

// for sorting by key.
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// main/mrworker.go calls this function.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.
	wokerID := os.Getpid() // 使用进程 ID 作为 Worker ID
	for {
		// 请求任务
		task := GetTask(wokerID)
		switch task.TaskType {
		case MapTask:
			doMap(task, mapf, wokerID)
		case ReduceTask:
			doReduce(task, reducef, wokerID)
		case WaitTask:
			time.Sleep(500 * time.Millisecond)
			continue
		case ExitTask:
			return
		}
	}
	// uncomment to send the Example RPC to the master.
	// CallExample()

}

func doMap(task GetTaskReply, mapf func(string, string) []KeyValue, wokerID int) {
	// 读取文件
	fileName := task.FileName
	file, err := os.Open(fileName)
	if err != nil {
		log.Fatalf("cannot open %v", fileName)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v", fileName)
	}
	file.Close()

	// 调用一下用户自定义的map函数去处理数据
	kva := mapf(fileName, string(content))

	// 将中间的结果分成nReduce个桶
	// 二维数组结构：第一维表示分区(对应将来的 reduce 任务)，第二维存储键值对
	intermediate := make([][]KeyValue, task.NReduce)
	for _, kv := range kva {
		// 使用 hash 函数确定键值对应放入哪个桶
		bucket := ihash(kv.Key) % task.NReduce
		intermediate[bucket] = append(intermediate[bucket], kv)
	}
	// 将每个桶放入对应的临时文件中
	for i := 0; i < task.NReduce; i++ {
		// 创建临时文件
		//tempFile, err := os.CreateTemp("", "mr-tmp-*")
		tempFile, err := ioutil.TempFile(".", "mr-tmp-*")
		if err != nil {
			log.Fatalf("can not create temp file: %v", err)
		}
		// 将键值对序列化到文件中
		enc := json.NewEncoder(tempFile)
		for _, kv := range intermediate[i] {
			err := enc.Encode(&kv)
			if err != nil {
				log.Fatalf("cannot encode %v", kv)
			}
		}
		tempFile.Close()
		// 将临时文件重命名 mr-map任务编号-reduce桶编号
		// 命名规则: mr-<map任务ID>-<reduce任务ID>
		outputFile := fmt.Sprintf("mr-%d-%d", task.TaskID, i)
		//os.Rename(tempFile.Name(), fmt.Sprintf("mr-%d-%d", task.TaskID, i))
		err = os.Rename(tempFile.Name(), outputFile)
		if err != nil {
			log.Fatalf("cannot rename temp file to %v: %v", outputFile, err)
		}
	}
	// 汇报任务完成
	reportTaskDone(task.TaskType, task.TaskID, wokerID)
}

func doReduce(task GetTaskReply, reducef func(string, []string) string, wokerID int) {
	reduceTaskNum := task.ReduceTaskNum
	mapTaskNum := task.MapTaskNum

	intermediate := []KeyValue{}
	// 读取该reduce任务负责的中间文件
	for i := 0; i < mapTaskNum; i++ {
		filename := fmt.Sprintf("mr-%d-%d", i, reduceTaskNum)
		file, err := os.Open(filename)
		if err != nil {
			log.Fatalf("cannot open %v", filename)
		}
		// 从文件中读取键值对
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			intermediate = append(intermediate, kv)
		}
		file.Close()
	}
	// 对intermediate进行shuffe排序
	sort.Sort(ByKey(intermediate))
	// 创建输出的临时文件
	tmpFile, err := os.CreateTemp(".", "mr-out-tmp-*")
	if err != nil {
		log.Fatalf("cannot creat temp file")
	}
	// 对每一个key调用reduce函数(示例mrsequential.go里有给出)
	i := 0
	for i < len(intermediate) {
		j := i + 1
		for j < len(intermediate) && intermediate[j].Key == intermediate[i].Key {
			j++
		}
		values := []string{}
		for k := i; k < j; k++ {
			values = append(values, intermediate[k].Value)
		}
		// 调用用户的 reduce 函数
		output := reducef(intermediate[i].Key, values)
		// 写入结果
		fmt.Fprintf(tmpFile, "%v %v\n", intermediate[i].Key, output)
		i = j
	}
	tmpFile.Close()
	// 5. 原子地将临时文件重命名为最终输出文件
	// 将临时文件重命名 mr-reduce任务编号
	outputFile := fmt.Sprintf("mr-out-%d", reduceTaskNum)
	//os.Rename(tmpFile.Name(), fmt.Sprintf("mr-out-%d", reduceTaskNum))
	err = os.Rename(tmpFile.Name(), outputFile)
	if err != nil {
		log.Fatalf("cannot rename temp file to %v: %v", outputFile, err)
	}
	// 报告任务完成
	reportTaskDone(ReduceTask, task.TaskID, wokerID)
}

func GetTask(wokerID int) GetTaskReply {
	args := GetTaskArgs{
		WorkerID: wokerID,
	}
	reply := GetTaskReply{}
	call("Master.GetTask", &args, &reply)
	return reply
}

func reportTaskDone(taskType string, taskID int, workerID int) {
	args := ReportTaskArgs{
		TaskType:  taskType,
		TaskID:    taskID,
		WorkerID:  workerID,
		Completed: true,
	}
	reply := ReportTaskReply{
		OK: true,
	}
	call("Master.ReportTask", &args, &reply)
}

// example function to show how to make an RPC call to the master.
//
// the RPC argument and reply types are defined in rpc.go.
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	call("Master.Example", &args, &reply)

	// reply.Y should be 100.
	fmt.Printf("reply.Y %v\n", reply.Y)
}

// send an RPC request to the master, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := masterSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
