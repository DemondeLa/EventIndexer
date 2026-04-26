# 阶段 2 Day 2 学习笔记：WatchXxx + WebSocket — 让程序"长期听着"

> **主题**：从"一次性脚本"过渡到"daemon 雏形"——让 Go 进程长期听着合约事件,并能优雅退出。
>

---

## 🎯 今天的核心问题

阶段 1 写的所有代码都是 "调用 → 返回 → 结束" 的一次性脚本。今天要写的是 "启动 → 长期运行 → 优雅死亡" 的 daemon。

这个跃迁的核心命题:**一旦启动一个长寿命的东西,你就和它签了一份合同——管理它的生命周期。**

---

## 一、FilterXxx vs WatchXxx 的根本差别

### 协议层差异

```
FilterXxx ─────► eth_getLogs    (HTTP 请求-响应)
                 一次查询,返回历史 log
                 适合:启动时同步历史

WatchXxx  ─────► eth_subscribe  (WebSocket 长连接推送)
                 节点主动推送
                 适合:实时监控
                 ⚠️ HTTP 不支持,会报 "notifications not supported"
```

### 心智锚点

- FilterXxx 像 SQL 的 `SELECT WHERE ts > X`
- WatchXxx 像数据库 trigger

### "历史 / 未来" 的精确表述

不是 "FilterXxx 看历史、WatchXxx 看未来" 这种简单二分。更精准:

- **FilterXxx**: 基于"调用时刻"这个**快照**,向**过去**看
- **WatchXxx**: 基于"调用时刻"这个**起点**,向**未来**看

`FilterXxx(End=nil)` 会包含"调用那一刻刚发生的"事件——所以也算历史。这两个 API 是**互补**的,不是对立的。Day 4 写 backfill 时会同时用。

---

## 二、WatchXxx 留下的"三件套"

```go
sub, err := contract.WatchProjectSubmitted(opts, sink, nil, nil)
```

这一行返回后,函数已经结束,但**留下了三个长寿命的东西**:

```
┌──────────────────────────────────────────────────────┐
│  1. sink channel       ── 你创建的,接收事件          │
│  2. 后台 goroutine     ── go-ethereum 内部启动的      │
│                          它在 select 循环里:         │
│                          - 从 WS 收 raw log          │
│                          - 解析成业务结构体          │
│                          - 写入 sink                 │
│  3. WebSocket 连接     ── 跨进程,节点端有状态         │
└──────────────────────────────────────────────────────┘
```

### `event.Subscription` 接口

```go
type Subscription interface {
    Err() <-chan error    // 出错时这里有消息;主动 Unsubscribe 后会被 close
    Unsubscribe()         // 主动取消订阅,清理三件套
}
```

### 关键认知:必须管理生命周期

WatchXxx 的使用者承担了一个 FilterXxx 使用者从来不需要承担的责任——**管理另一个东西的死亡**。

---

## 三、graceful shutdown 的标准 pattern

### 完整骨架

```go
// [1] 创建可取消的 context
ctx, cancel := context.WithCancel(context.Background())
defer cancel()  // 安全网:任何路径退出都保证 cancel

// [2] 注册信号监听
sigCh := make(chan os.Signal, 1)  // ⚠️ 必须 buffered
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

// [3] 独立 goroutine 把信号翻译成 cancel
go func() {
    sig := <-sigCh
    log.Printf("👋 收到信号: %v", sig)
    cancel()  // 触发 ctx.Done()
}()

// [4] 把 ctx 传给 WatchXxx
sub, err := contract.WatchProjectSubmitted(
    &bind.WatchOpts{Context: ctx},  // ← 关键
    sink, nil, nil,
)
if err != nil { log.Fatal(err) }
defer sub.Unsubscribe()
defer client.Close()

// [5] 三路 select 主循环
for {
    select {
    case event := <-sink:
        fmt.Println("收到事件:", event)
    case err := <-sub.Err():
        // TODO: 区分 err == nil(主动 unsubscribe)和真错误
        log.Printf("订阅出错: %v", err)
        return
    case <-ctx.Done():
        fmt.Println("结束监听")
        return
    }
}
```

### 三个 channel 的角色

| channel | 谁写 | 谁读 | 作用 |
|---|---|---|---|
| sink | go-ethereum 后台 goroutine | main 主循环 | 传**事件数据** |
| ctx.Done() | cancel() 函数 | 后台 goroutine + main 主循环 | 传**"该停了"信号** |
| sigCh | Go runtime(OS 信号到达时) | 独立 goroutine | 传**OS 信号** |

### 为什么不直接把 sigCh 加进主循环 select?

```go
// ❌ 耦合写法
case <-sigCh:
    sub.Unsubscribe()
    db.Close()
    httpServer.Shutdown(...)
    return

// ✅ 分层写法:sigCh → cancel → ctx → 所有人监听
```

**核心理由**:今天只有 1 个组件,两种写法都行。但 Day 3-6 会加 SQLite、HTTP server、worker pool 等更多组件——

- **耦合写法**: 每加一个组件,主循环 case 里多一行清理代码,main 函数变成"清理大杂烩"
- **分层写法**: 每个组件自己监听 ctx,自己清理自己,main 不用改

这是**为了未来扩展**而做的设计选择。

---

## 四、context 的核心心智

### 1. context 是派生的,不是配置的

```
context.Background()                    永不取消,做根
   │
   ├─ context.WithCancel(parent)        手动 cancel 取消
   ├─ context.WithTimeout(parent, d)    时间到自动取消
   └─ context.WithDeadline(parent, t)   到某个时间点自动取消
```

不会"修改"已有 context。永远是**派生**新的子 context。

### 2. cancel 是幂等的,defer 是安全网

`cancel()` 内部有 "已取消?直接 return" 检查,多调一次没事。

```go
ctx, cancel := context.WithCancel(...)
defer cancel()  // 哪怕别处已经调过 cancel,这里再调也没事

// 还可以在 goroutine 里也调 cancel
go func() { <-sigCh; cancel() }()
```

为什么要 `defer cancel()`?——情况 B 兜底:`sub.Err()` 触发时 main 会 return,但**没人按 Ctrl+C**,goroutine 里的 `<-sigCh` 永远不会触发,cancel 永远不被调。defer 兜底比"我能想清楚每条退出路径"更省脑子。

### 3. cancel 的传播:一次调用,所有派生 goroutine 都能感知

这是 context 链的核心价值——**一处 cancel,处处响应**。

### 4. context.Background() 的 Done() 永远不触发

```go
ctx := context.Background()
<-ctx.Done()  // ❌ 永远阻塞
```

它的设计意图是"做 context 链的根",必须从它派生才有用。**直接传 Background() 给 WatchOpts 等于没传**。

---

## 五、signal handling 关键细节

### 为什么 sigCh 必须 buffered?

```go
sigCh := make(chan os.Signal, 1)  // ✅ buffer = 1
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
```

**Go 官方文档原话**: "signal.Notify will not block sending to c"——也就是 runtime 在送信号到 channel 时**不会阻塞**。

如果用 unbuffered (`make(chan os.Signal)`):
- runtime 试图写入,但没有读者立刻接收
- runtime 不会等,直接放弃 → **信号丢了**

如果用 buffer=1:
- runtime 写入 buffer,立刻完成
- 读者后面慢慢读

正常用户只按一次 Ctrl+C,buffer=1 完全够用。是约定俗成的写法。

---

## 六、close(channel) 的语义

```go
ch := make(chan error, 3)
ch <- errors.New("A")
ch <- errors.New("B")
close(ch)

err1 := <-ch    // → "A"   缓冲里还有东西,先消费
err2 := <-ch    // → "B"
err3 := <-ch    // → nil   缓冲空了,且已 close,返回零值
err4 := <-ch    // → nil   再读还是零值,不阻塞
```

### `close` 实际做了两件事

1. **写端永久禁用**:再 `ch <- x` 会 panic
2. **读端获得"知情权"**:能区分"暂时没数据(阻塞)"和"永远没了(立即返回零值)"

更准的比喻:`close(ch)` 是给读取者发**最后一条消息**——这条消息的内容是"**没有更多消息了**"。

### `sub.Err()` 的陷阱

```
sub.Unsubscribe() 被调用 → sub.Err() 被 close → 读出 nil
```

```go
case err := <-sub.Err():
    log.Printf("订阅出错: %v", err)  // ← 可能打印 "订阅出错: <nil>"
    return
```

健壮版本:

```go
case err := <-sub.Err():
    if err == nil {
        // 主动 Unsubscribe 触发,不是真错误
        return
    }
    log.Printf("订阅出错: %v", err)
    return
```

### 显式判断:双返回值

```go
err, ok := <-ch
// ok = true:  从 channel 读到一个值(哪怕是 nil)
// ok = false: channel 已关且 buffer 空
```

---

## 七、WebSocket 关键概念

### 与 HTTP 的核心差异

| | HTTP | WebSocket |
|---|---|---|
| 谁能发消息 | 客户端发,服务器答 | ⭐ 双方都能主动发 |
| 连接寿命 | 一次请求-响应完事 | ⭐ 一直开着 |
| 适合的场景 | 问答、抓数据 | ⭐ 推送、实时通知 |

### 协议升级

```
[第 1 步] HTTP Upgrade 握手
客户端 ──"Upgrade: websocket"──▶ 服务器
服务器 ──"101 Switching Protocols"──▶ 客户端

[第 2 步] 之后这条 TCP 连接走 WebSocket 协议
```

**关键**:`http://127.0.0.1:8545` 和 `ws://127.0.0.1:8545` 是**同一个端口、同一个 TCP 服务器**,只是握手时说不同的话。

### close codes

| code | 含义 | 真实场景 |
|---|---|---|
| 1000 | Normal Closure | 双方握手,优雅关闭 |
| 1001 | Going Away | 计划内的关闭 |
| **1006** | **Abnormal Closure** | **没收到 close 帧就断了——对方进程消失** |
| 1011 | Internal Error | 服务器内部错 |

**1000 vs 1006 的差异**就是"优雅关闭 vs OS 收尸"在协议层的表现。生产环境监控应该把这两种**分开统计**。

---

## 八、abigen 事件结构体的二元设计

```go
type WinnerTakesAllProjectSubmitted struct {
    ProjectId  *big.Int        // 业务字段(emit 出去的值)
    Submitter  common.Address  // 业务字段
    Name       string          // 业务字段
    Url        string          // 业务字段

    Raw        types.Log       // ⭐ 原始上下文
}
```

`types.Log` 包含:
- `Address`: 合约地址
- `Topics`: 事件签名 hash + indexed 字段
- `Data`: 非 indexed 字段的字节流
- `BlockNumber`: 块号
- `TxHash`: 交易 hash
- `LogIndex`: 在该交易中的 log 序号
- **`Removed`**: 是否因 reorg 被撤回 ⚠️

### 为什么 Raw 重要

| 需求 | 用 Raw 的什么字段 |
|---|---|
| 事件去重(写 DB 主键) | `(TxHash, LogIndex)` 唯一键 |
| "我处理到哪个块了" | `BlockNumber` |
| 处理 reorg | `Removed` |
| 验证事件真实性 | 整个 Raw 重新查节点 |

📌 **业务字段是"事件说什么",Raw 字段是"事件值不值得信"**。

---

## 九、今天踩过的真实坑

### 坑 1:`http://` 跑 WatchXxx

错误: `notifications not supported`

```
go-ethereum 识别出 "http://" → 创建 HTTP transport
→ HTTP 不支持长连接推送 → 前置检查 fail
→ 根本没发任何 RPC 给节点
```

**修复**: `http://` 改成 `ws://`,Hardhat 同端口支持。

### 坑 2:Ctrl+C 不优雅

```
^Csignal: interrupt    ← shell 报告"非正常退出"
```

原因: 没注册 signal handler,Go 默认行为是 `os.Exit(130)`——**绕开 defer**。

```
✗ os.Exit() 不跑 defer
✗ 信号默认行为不跑 defer
✓ 只有正常 return / panic-recover 才跑 defer
```

**修复**: 加 signal.Notify + cancel pattern。

### 坑 3:`evm_increaseTime` 把链时间推到未来

```
第一次 seed: time.Now() = T,deadline = T + 3600,链时间被推到 T + 7200
第二次 seed: time.Now() = T+1,deadline = T+1 + 3600 = T+3601
                                ↑
                                比当前链时间(T+7200)还早
                                合约 require: deadline > block.timestamp 失败
                                撞 InvalidDeadline(provided=T+3601, required=T+7200)
```

**根因**: 真实世界的 `time.Now()` 和链上的 `block.timestamp` 是**两套时钟**——`evm_increaseTime` 让它们脱钩后,无法用真实时间反推链时间。

**今天的解法**: 重启 Hardhat 节点(链时钟回到真实"现在")。

**Day 4-5 的真解**: indexer 一切时间相关查询都用**链时钟**(block 号或 block.timestamp),不用 `time.Now()`。

### 坑 4:`go run` 的退出码 ≠ 程序的退出码

```
你的程序: return → exit 0  ✅
go run 包装器: 收到 SIGINT 干扰 → 报告 exit 1
```

**修复**: `go build -o /tmp/watch ./cmd/watch` 后直接跑二进制。

### 坑 5:杀 Hardhat 时 watch 收到 1006

```
websocket: close 1006 (abnormal closure): unexpected EOF
```

不是 bug,是**正确观察**——节点进程消失,客户端从 TCP EOF 推断对方没了。这正是 `sub.Err()` 该触发的场景。

---

## 🧰 工具箱(本日新增第 13-22 条)

> 工具箱 = 阶段 1 末已经积累的可复用心智库,今天新增 9 条。

| # | 心智 | 一句话 |
|---|---|---|
| 13 | 接口能调用 ≠ 调用能成功 | 抽象层把"能不能做"的判断推给运行时,依赖底层 transport 的能力 |
| 14 | 程序的"健康"不只看自己 | 长寿命进程要看它和外部世界的关系是否干净(WS、文件锁、DB 连接) |
| 15 | defer cancel() 是 context 安全网 | cancel 幂等,多调没事;不调可能 leak |
| 16 | 单一退出信号源 | N 个组件汇聚到 1 个 ctx,触发点 N 种,传递机制 1 种 |
| 17 | 怀疑工具 vs 怀疑代码 | 代码逻辑对但观测不对时,先怀疑包装层(go run/docker/pytest) |
| 18 | 协议层关闭 vs 传输层 EOF | normal close 是计划内,1006 是事故,生产监控要分开 |
| 19 | channel close 时返回零值 | 读 closed channel 不阻塞,返回零值;用 `, ok` 显式区分 |
| 20 | 调试器是天然的同步原语 | 开发期错峰运行别加 sleep,断点就是免费的同步机制 |
| 21 | 业务字段 + 原始上下文(Raw) | 业务字段说"事件是什么",Raw 说"事件值不值得信" |
| 22 | 类比是抗遗忘的 | 死记 N 条 checklist 三个月后只剩 0 条;记一个**模式**还能套用到新 API |

---

## 🪝 留给后续的钩子(今天感受到了,没解决)

| 钩子 | 何时撞到 | Day x 解决 |
|---|---|---|
| sink buffer 满了之后会反压 | 慢消费者 + 高频事件 | Day 3 拆 goroutine + SQLite |
| `sub.Err() == nil` 的分支必须真处理 | 主动 Unsubscribe 时 | Day 3 |
| 事件去重(TxHash, LogIndex 主键) | 写 DB 时 | Day 3 |
| 进程重启后从哪个块续上 | indexer 重启 | Day 4 backfill |
| Filter 历史 + Watch 未来的"缝" | 启动时回填 | Day 4 |
| 链时钟 vs 物理时钟脱钩 | 第二次 seed 撞 InvalidDeadline | Day 4-5 |
| `Raw.Removed` 处理 reorg | 连 Sepolia 后 | Day 5 |
| 自愈重连(节点崩了不退出而是重连) | 真实网络抖动 | Day 6 |
| 结构化日志 | daemon 启动时 | Day 6 |

---

## 🎯 今天的元收获(关于学习方式)

### 1. 在 Go 里思考

Go 并发的官方箴言:**"Don't communicate by sharing memory; share memory by communicating"**。

其他语言的 graceful shutdown 用共享变量 + 主动轮询(Java `isInterrupted()`、Python `event.is_set()`、C++ `atomic<bool>`)——每个组件**主动检查** flag,容易忘。

Go 用 channel + select——goroutine **被动阻塞**等待多件事,cancel 一调立刻醒。今天的代码里这个模式被实现了 4 次(sink、sub.Err、ctx.Done、sigCh)。

### 2. 反驳教练的 3 种模式

| 模式 | 定义 | 今天的例子 |
|---|---|---|
| A. 反驳 | 教练说错了,我有反例 | seed 流程描述错(2 次) |
| B. 验证 | 我自己模拟了一遍程序,某细节存疑 | cancel 调两次的疑问 ⭐ |
| C. 澄清 | 我没听懂教练想问什么 | Q1 选项设计 |

模式 B 最难、含金量最高——它说明你已经能在脑子里"模拟运行"程序,这是工程师心智模型成熟的标志。

### 3. "内化成习惯"是结果,不是方法

距离 Day 1 踩过 `_ 吃掉 err` 大坑才 24 小时。三周不写代码后,肌肉记忆会衰退。

**廉价的维护方法**: 每次 commit 前 grep `, _ :=` 和 `, _ =`。

### 4. 卡住要问的时候,先把整段代码自查一遍

今天 `make chan` 语法卡住时,代码里同时有 3 个问题(语法 + 没接 err + 冗余 for-break),我只问了 1 个。下次该一次问完。

这是 Day 1 "3 件事一起做完再报告" 纪律的升级版。

---

## ✅ 验收清单

### 必须项

- [x] 开局必答 "WatchXxx vs FilterXxx 生命周期差异" 答到了"留下三件套"层
- [x] Q1/Q2/Q3 三题都给出有内容的答案
- [x] 亲自踩过 `http://` 跑 WatchXxx 的错误
- [x] `cmd/watch/main.go` 用 `ws://127.0.0.1:8545` 连接成功
- [x] trigger 触发 → watch 实时收到 3 个事件并打印
- [x] Ctrl+C 优雅退出
- [x] `defer sub.Unsubscribe()` + `defer client.Close()` 都有写
- [x] 三路 select 主循环结构正确
- [x] `signal.Notify` 的 channel 是 buffered

### 深度项

- [x] 能画出 WatchXxx 数据流的至少 6 节点图
- [x] 能解释为什么 `signal.Notify` 的 channel 必须 buffered
- [x] 能解释 `sub.Err()` 在哪些场景有值、哪些场景 close
- [x] 能说出三路 select 中哪个 case 可能永远不触发
- [x] 反驳教练 4 次(超额完成 — 目标是 ≥1 次)
- [x] Step 0 用 5 分钟验证了 Day 1 Decoder 的真实工作能力

---

## 📁 今日代码产出

```
EventIndexer/
├── cmd/
│   ├── indexer/main.go     ← Day 1 Decoder 演示(保留不动)
│   ├── watch/main.go       ← 今天新增:graceful shutdown 监听器
│   └── trigger/main.go     ← 今天新增:部署 + 3 账户提交项目
├── abigen/winner/...       ← Day 1 已有
└── internal/abiutil/...    ← Day 1 已有
```

---

## 🔮 Day 3 预告

主题:**拆 goroutine(生产者-消费者模式)+ 引入 SQLite**

需要做的:
- 把 sink 的消费者从 main 拆出来,放进独立 goroutine
- 引入 sync.WaitGroup 协调多 goroutine 退出
- 写完整 trigger(投票 + 结算)
- 事件用 `(TxHash, LogIndex)` 写入 SQLite
- 真正处理 `sub.Err() == nil` 的分支

---

*"今天我反驳了教练 4 次。Day 1 的纪律延续上了。"*
