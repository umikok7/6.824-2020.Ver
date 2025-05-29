# lab3A

## Debug日志

一个和所做的lab2相关联的错误的原因，来自于以下段落：

`Get` 函数中
```go
	// 设置超时ticker
	timer := time.NewTicker(300 * time.Millisecond)
```
`PutAppend` 函数中
```go
	// 设置超时ticker
	timer := time.NewTicker(300 * time.Millisecond)
```

将超时时间从100ms修改为300ms即解决了问题，看来大概率是因为在做lab2的时候算法不够精细，Raft的想要达成共识的时间需要给足。

最后运行的结果为：

```
Test: one client (3A) ...
  ... Passed --  15.7  5  3764   76
Test: many clients (3A) ...
  ... Passed --  18.1  5  9820  369
Test: unreliable net, many clients (3A) ...
  ... Passed --  19.3  5  1697  271
Test: concurrent append to same key, unreliable (3A) ...
  ... Passed --   5.4  3   257   52
Test: progress in majority (3A) ...
  ... Passed --   0.8  5    72    2
Test: no progress in minority (3A) ...
  ... Passed --   1.6  5   171    3
Test: completion after heal (3A) ...
  ... Passed --   1.2  5    70    3
Test: partitions, one client (3A) ...
  ... Passed --  22.9  5 22001   55
Test: partitions, many clients (3A) ...
  ... Passed --  27.3  5 67322  286
Test: restarts, one client (3A) ...
  ... Passed --  20.4  5 11890   76
Test: restarts, many clients (3A) ...
  ... Passed --  23.0  5 44545  380
Test: unreliable net, restarts, many clients (3A) ...
  ... Passed --  26.4  5  2692  242
Test: restarts, partitions, many clients (3A) ...
  ... Passed --  30.3  5 81522  355
Test: unreliable net, restarts, partitions, many clients (3A) ...
  ... Passed --  30.2  5  4867   95
Test: unreliable net, restarts, partitions, many clients, linearizability checks (3A) ...
  ... Passed --  29.3  7 13077  248
PASS
ok      _/home/umiko/go/MapReduce/6.824/src/kvraft      272.716s
```



## 设计细节

### 1.客户端缓存机制
一种快速获得leader服务器的技巧

```go
serverId := ck.leaderId // 直接读取上一次成功的服务器

...

for {
		reply := GetReply{}
		ok := ck.servers[serverId].Call("KVServer.Get", &args, &reply)
		if ok {
			if reply.Err == ErrNoKey {
				ck.leaderId = serverId
				return ""
			} else if reply.Err == OK {
				ck.leaderId = serverId // 标记一下，便于下一次请求的时候迅速找到可用的服务器

```

---


# Lab3B

## Debug日志
先看执行结果：

```
Test: InstallSnapshot RPC (3B) ...
  ... Passed --  15.7  3  3523   63
Test: snapshot size is reasonable (3B) ...
--- FAIL: TestSnapshotSize3B (165.79s)
    config.go:69: test took longer than 120 seconds
Test: restarts, snapshots, one client (3B) ...
  ... Passed --  20.8  5 12609   76
Test: restarts, snapshots, many clients (3B) ...
  ... Passed --  31.9  5 39138 1401
Test: unreliable net, snapshots, many clients (3B) ...
  ... Passed --  19.5  5  1726  243
Test: unreliable net, restarts, snapshots, many clients (3B) ...
  ... Passed --  25.4  5  2663  222
Test: unreliable net, restarts, partitions, snapshots, many clients (3B) ...
  ... Passed --  36.7  5  4111  122
Test: unreliable net, restarts, partitions, snapshots, many clients, linearizability checks (3B) ...
  ... Passed --  32.0  7 13895  295
FAIL
exit status 1
FAIL    _/home/umiko/go/MapReduce/6.824/src/kvraft      347.755s
```

测试2始终无法通过，暂时分析原因出自于lab2中raft层的实现导致的问题，或许是raft层的设计不够优越，导致了一个应用层的关键问题：
在`Get`与`PutAppend`中：
```go
	// 设置超时ticker
	timer := time.NewTicker(300 * time.Millisecond)
```
- 经过测试，超时时间最短都得设置300ms，才能达成Raft层的共识，而参考的实现设置的为100ms，这就导致了导致第二个测试需要的时间很长；
- 此外在Raft层的频繁进行持久化、大量的数据在锁内处理也可能性能下降
- KV层应该也能优化，后续全部做完再回过头查看


## 设计细节

### 1. 重新认识Raft层的SnapShot函数

实际上要结合KV层与Raft层的实现去理解

```go
  // 如果需要快照并且超出了stateSize
  if kv.maxraftstate != -1 && kv.rf.GetRaftStateSize() > kv.maxraftstate {
    snapshot := kv.PersistSnapShot()           // 创建快照
    kv.rf.Snapshot(msg.CommandIndex, snapshot) // 通知Raft层
  }
```

这样的设计保证了：

```
┌─────────────────┐
│   KVServer      │  负责：创建应用层快照数据
│   (应用层)       │  
└─────────────────┘
         ↓ snapshot data
┌─────────────────┐
│   Raft Layer    │  负责：日志压缩 + 持久化整合
│   (共识层)       │  
└─────────────────┘
```
- 实现如上的分层职责，KV层序列化业务相关的数据内容，Raft层修剪日志并序列化共识需要的数据，同时进行原子保存；
- 先应用后快照，确保状态是正确的；

---

此外的 maxraftstate 和 RaftStateSize 也值得说道：
```go
 if kv.maxraftstate != -1 && kv.rf.GetRaftStateSize() > kv.maxraftstate {
    snapshot := kv.PersistSnapShot()           // 创建快照
    kv.rf.Snapshot(msg.CommandIndex, snapshot) // 通知Raft层
  }
```
注意此处的条件判断：

maxraftstate是在`StartKVServer`中设定的，代表能够容忍的**阈值**，一旦Raft层持久化的数据（`kv.rf.GetRaftStateSize()`）比这个阈值还大，就说明需要进行`Snapshot`操作去让`GetRaftStateSize`变小，从而通过这种方式控制内存与磁盘的使用。

