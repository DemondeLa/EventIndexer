# Day 3 学习笔记 — 拆 goroutine：生产者-消费者管道

> 主题：把 Day 2 的"sink + main 自己读"单循环模式，拆成生产者 + 消费者两个 goroutine，用 channel 连接，用 sync.WaitGroup 协调退出

---

## 🎯 今天的核心成就

把这条 7 节点数据流亲手实现了出来：

```
节点 → ws → A(go-eth 内部 goroutine) → sink → B(生产者) → eventCh → C(消费者) → onEvent
```

并且让它具备：
- ✅ 优雅退出（Ctrl+C 后所有 goroutine 干净停下，资源不泄漏）
- ✅ 反压机制（消费者慢时整条链路反向阻塞，不丢数据）
- ✅ 可扩展性（Day 4 接 DB 时 indexer.go 一行不改）

---

## 📚 核心概念精炼

### 1. ctx 与 WaitGroup 的心智分工 ⭐ (今天最值钱的认知)

| 维度 | ctx | WaitGroup |
|---|-----|-----------|
| 方向 | **一对多** | **多对一** |
| 语义 | 广播"该停了" | 等待"都停了" |
| 阻塞 | 不阻塞调用方 | 阻塞调用方直到归零 |
| 类比 | 消防警报 | 全员到齐打卡表 |

**配合使用是必需的**：
- 只有 ctx → goroutine 听到警报，但 main 不等 → 资源被关 / 数据丢
- 只有 WG → goroutine 不知道该停 → 永远不 Done → main 永久卡死

**记死这条**：信号 + 同步，分别用两个原语。这是 Go 并发设计的核心模式。

---

### 2. Channel close 铁律

| 规则 | 后果 |
|------|------|
| 只有生产者才能 close | 消费者 close → 生产者写时 panic |
| 多生产者不能单独 close | 多个生产者同时 close → panic |
| 库给你的 channel 永远不 close | 你不是它的生产者 |
| 你 make 给自己用的 channel | 由你的生产者 `defer close` |

**今天的实战对照**：

```go
sink := make(chan *winner.WinnerTakesAllProjectSubmitted, 32)
//         go-ethereum 内部 goroutine 在写 → 你不能 close 它

eventCh := make(chan Event, 32)
//         你的生产者 B 在写 → B 退出时 defer close
```

---

### 3. `for x := range channel` 的退出条件

> **唯一的退出条件：channel 被 close**

如果 channel 永远不 close → for range **永远不退出**。

**Day 2 错的写法**：
```go
for ev := range sink {  // sink 永不 close → 永远阻塞
    ...
}
```

**Day 3 对的写法**：
```go
for ev := range eventCh {  // 生产者 defer close → 自然退出
    ...
}
```

差别**不在语法**，而在 **channel 的所有权（谁负责 close）**。

---

### 4. Channel 读写的 4 种语义

```go
ch := make(chan int, N)   // buffer 容量 = N
```

| 操作 | buffer 状态 | 行为 |
|------|------------|------|
| `ch <- x` (写) | 没满 | 立刻成功 |
| `ch <- x` (写) | **满了** | **阻塞** ⚠️ |
| `<-ch` (读) | 不空 | 立刻成功 |
| `<-ch` (读) | **空了** | **阻塞** |

**关键洞察**：Go channel **永远不丢数据**——满了写入方阻塞，空了读取方阻塞。

---

### 5. 反压（Backpressure）链路

当下游慢时，反压**反向传递**到所有上游：

```
DB 写入慢 (10ms)
  ↓
消费者 C 慢
  ↓
eventCh 满
  ↓
生产者 B 卡在 `eventCh <- e`
  ↓
B 不再读 sink
  ↓
sink 满
  ↓
go-ethereum 的 A 卡在 `sink <- raw`
  ↓
反压一直传到节点
```

**核心心智**：每一环都是"阻塞"，不是"丢弃"。
- 性能瓶颈被显式化（暴露问题，而不是悄悄丢数据）
- "下游慢，上游等" = 优雅的分布式系统姿态

---

### 6. 嵌套 select 的 ctx 兜底

任何 channel 写入操作都应该包在 select 里加 ctx 兜底：

```go
// ❌ 不健壮
case raw := <-sink:
    eventCh <- e   // 如果 eventCh 满了，这里会卡死，ctx 取消也救不了

// ✅ 健壮
case raw := <-sink:
    select {
    case eventCh <- e:
    case <-ctx.Done():
        return    // ctx 取消时立刻退出，丢这一条
    }
```

**为什么必要**：select 只在语句开始时检查所有 case，一旦走进某个分支就脱离 select。`eventCh <- e` 这种普通 channel 写入**没有任何兜底机制**——会破坏 graceful shutdown。

---

### 7. graceful shutdown ≠ zero data loss

> "优雅退出"的承诺是"已经处理完的事件不丢、资源不泄漏"
>
> **不是**"队列里所有事件必须处理完才能退"

- 关机时丢失正在搬运的 1 条事件 → 可接受
- 程序无法响应 Ctrl+C → 不可接受

真正严肃的"不丢任何事件"要靠**持久化 + 重放**，不靠 goroutine 协调。

---

### 8. Drain Before Exit（退出前排空）

消费者用 `for range channel` + 上游 close 的模式，**天然实现**了"退出前排空 buffer"：

```
Ctrl+C → ctx 取消
  ↓
生产者 B 立刻退出 → defer close(eventCh)
  ↓
消费者 C 不知道 ctx，但发现 eventCh close 了 → range 退出
  ↓ 在 range 退出前，C 会先把 buffer 里残留的事件全部消费完
```

**这不是你写的逻辑，是 channel 语义自动给你的**——非常优雅。

---

### 9. WatchOpts.Context 不会让 WatchXxx 停下来

`bind.WatchOpts.Context` 只管初始化阶段。**运行时停 WatchXxx 必须靠 `sub.Unsubscribe()`**。

**正确退出链**：
```
ctx cancel → B 收到信号 → B 调 sub.Unsubscribe() → A 才停 → B 退出
```

B 是 A 的"刽子手"——B 必须主动通知 go-ethereum 停 A，A 不会自己跟着 ctx 停。

---

### 10. Go struct 的设计哲学（思维迁移）

**C++ / Java 视角**（之前的视角）：
- class = 属性的集合 + 操作属性的方法
- 对象 = 一组数据 + 操作这组数据的能力

**Go 视角**（今天进入的视角）：
- struct = **一组协作工具的容器**
- 对象 = **一个行为单元**，字段是它干活时调用的工具

**判断对象价值的方法**：先看方法在做什么，再回头看它需要哪些工具。

**今天的实例**：
```go
type Indexer struct {
    contract *winner.WinnerTakesAll  // 字段只是工具
}
```
Indexer 的价值不在字段，而在 `Run` 方法里那 50 行协调逻辑。

---

## 🛠️ 实战架构

### 文件结构

```
EventIndexer/
├── abigen/winner/winner.go     (abigen 自动生成，不动)
├── internal/
│   ├── abiutil/decoder.go       (Day 1)
│   └── indexer/indexer.go       (今天新建)
└── cmd/
    ├── watch/main.go            (今天改造，变薄)
    └── trigger/main.go          (今天补完投票+结算)
```

### Indexer 核心 API

```go
package indexer

// 业务事件类型（解耦合约升级）
type Event struct {
    ProjectID   uint64
    Name        string
    URL         string
    Submitter   string  // hex
    TxHash      string  // hex（Day 4 主键用）
    LogIndex    uint64  // (Day 4 主键用)
    BlockNumber uint64  // (Day 4 主键用)
}

// Indexer 对象
type Indexer struct {
    contract *winner.WinnerTakesAll
}

// 构造函数
func NewIndexer(contract *winner.WinnerTakesAll) (*Indexer, error) {
    return &Indexer{contract: contract}, nil
}

// 核心 Run 方法（启动 + 阻塞运行）
func (idx *Indexer) Run(ctx context.Context, onEvent func(Event) error) error {
    // 详见下方完整代码
}
```

### Run 方法的完整骨架

```go
func (idx *Indexer) Run(ctx context.Context, onEvent func(Event) error) error {
    var wg sync.WaitGroup

    // 1. 订阅链上事件
    sink := make(chan *winner.WinnerTakesAllProjectSubmitted, 32)
    sub, err := idx.contract.WatchProjectSubmitted(
        &bind.WatchOpts{Context: ctx}, sink, nil, nil,
    )
    if err != nil {
        return fmt.Errorf("订阅失败: %w", err)
    }

    // 2. 创建业务 channel
    eventCh := make(chan Event, 32)
    wg.Add(2)  // ⚠️ 必须在 go 之前 Add

    // 3. 生产者 goroutine
    go func() {
        defer wg.Done()
        defer close(eventCh)        // 关下游
        defer sub.Unsubscribe()     // 关上游

        for {
            select {
            case raw := <-sink:
                e := convertToEvent(raw)
                select {
                case eventCh <- e:
                case <-ctx.Done():    // 嵌套 select 兜底
                    return
                }
            case err, ok := <-sub.Err():
                if !ok {
                    log.Println("订阅 channel 已关闭，主动退订")
                } else if err != nil {
                    log.Printf("订阅出错: %v", err)
                }
                return
            case <-ctx.Done():
                log.Println("收到取消信号，生产者退出")
                return
            }
        }
    }()

    // 4. 消费者 goroutine
    go func() {
        defer wg.Done()
        for event := range eventCh {  // eventCh close 后自然退出
            if err := onEvent(event); err != nil {
                log.Printf("处理失败 (projectId=%d): %v", event.ProjectID, err)
            }
        }
    }()

    // 5. 等所有 goroutine 真停
    wg.Wait()
    return nil
}
```

### 关键设计决策

| 决策点 | 选择 | 理由 |
|-------|------|------|
| Run 是否接受回调 | 是（onEvent func(Event) error） | 依赖倒置，Day 4 接 DB 不改 indexer |
| onEvent 是否返回 error | 是 | 留扩展空间（错误分类） |
| 今天怎么处理 onEvent error | 打 log 继续 | daemon 不能因单条失败崩 |
| Run 返回值用途 | 主要给"启动失败" | 运行中错误内部消化 |
| eventCh 用值还是指针 | 值（chan Event） | 数据小、并发安全 |
| Event 字段类型 | uint64 / string | 业务友好、跨平台稳定 |

---

## 🔑 工具卡（今天新增 5 条）

### #25 ctx 与 WG 的心智分工 ⭐
ctx 是"一对多 广播通知，仅通知不等待"
WG 是"多对一 等待全员完成"
两者必须配合，缺一会死锁或数据丢失

### #26 Go struct 的设计哲学
字段 = 干活时用的工具
方法 = 我能做什么（价值在这里）
看一个对象的价值，先看它的方法

### #27 主干 + 推导的抗遗忘学习法
不要试图记住所有细节，记住关键锚点：
- 有形的代码实体（channel / 函数 / struct）比抽象概念好记
- 能自动暗示其他细节
- 互相可推导（A → B → C，形成网状）

### #28 代码即文档（动手版）
自己亲手写过的代码 = 最好的"记忆压缩文件"
3 个月后忘了概念？打开自己写的代码，整套设计会自动重建
读 100 篇文章不如自己写一次

### #29 抽象好坏的判断标准
不是看代码"看起来优雅"，而是看**未来的修改集中在哪里**：
- 好的抽象：新增功能时改动集中在一个地方
- 坏的抽象：改动散布在多个文件
今天的设计：Day 4 接 DB 时 indexer.go 一行不改 ✅

---

## ⚠️ 今天踩过/学到的坑

### 坑 1：errors.Is(err, nil) 是错误用法
- `errors.Is` 是判断**特定错误类型**用的
- 判断 nil → 用 `err == nil`
- 判断 channel close → 用 `, ok := <-ch`

### 坑 2：select 必须配 for（持续监听场景）
```go
// ❌ 只跑一次
go func() {
    select { case ...: ... }
}()

// ✅ 持续监听
go func() {
    for {
        select { case ...: ... }
    }
}()
```

### 坑 3：手动构造 TransactOpts 丢失自动设置字段
```go
// ❌ 丢了 NewKeyedTransactor 的 GasLimit / Nonce / Context 等
&bind.TransactOpts{From: x.From, Signer: x.Signer}

// ✅ 用 NewKeyedTransactor 返回的实例
// 或写个 helper 做值复制：
func NewTxOpts(base bind.TransactOpts, value *big.Int) *bind.TransactOpts {
    base.Value = value
    return &base
}
```

### 坑 4：evm_increaseTime 是相对偏移
- 不是设置绝对时间，是从当前时间往后推
- 测试时间控制：跨边界永远多偏一点（3601 比 3600 稳）

### 坑 5：大整数转换不报错只截断
- `*big.Int.Uint64()` 超界时静默截断
- 严谨写法应该先 `IsUint64()` 检查

### 坑 6：多 goroutine 共享 stdout 时输出顺序不可信
- 看到的输出顺序是"运行时偶然性"，不是"代码逻辑顺序"
- 调试并发问题时，**只让一个 goroutine 打印**

### 坑 7：Indexer 字段稀薄不代表它简单
- 不要把"struct 字段少"等同于"对象简单"
- 价值在方法里（Run 的 50 行协调逻辑）

---

## 🧪 反压实验（亲眼看到的现象）

### 实验 1：buffer = 32，消费者 sleep 3s

**现象**：3 条事件立刻全部进入 buffer，生产者飞速完成，消费者按 3 秒间隔慢慢消费。

**洞察**：buffer 没满时反压不触发——生产者跑得飞快，慢的全堆给消费者。

### 实验 2：buffer = 1，消费者 sleep 3s

**现象**：每 3 秒打印一条事件，生产者被反压卡住。

**T=0 时刻快照**：
- sink: [e3]
- eventCh: [e1]
- B 生产者：卡在嵌套 select 的 `eventCh <- e2`（手里捏着 e2）
- C 消费者：在 onEvent(e1) 的 sleep 中

**洞察**：反压链路真实存在——下游慢一拍，上游全跟着等。

### 实验 3：捕捉 drain before exit

**现象**：Ctrl+C 时生产者立刻打印"结束监听"，但消费者**继续打印了** e3。

**洞察**：优雅退出 ≠ 立刻退出。channel 语义自动实现了"退出前排空"。

---

## 🌟 阶段 2 进度（长期运行意识）

### 阶段 2 的 4 个长期问题

| # | 问题 | Day 3 处理状态 |
|---|------|-------------|
| 1 | 这段代码会不会在 7 天不重启的 daemon 里跑？| Day 2 已闭合（context + signal）|
| 2 | 连续处理 100 万个事件有累积泄漏吗？| **✅ Day 3 处理：拆 goroutine + buffer 反压 = 不无限堆** |
| 3 | 网络抖动 3 秒会发生什么？| Day 6 才解决 |
| 4 | 进程被 Ctrl+C，数据会不会丢？| **🔧 Day 3 留出 onEvent 钩子，Day 4 接 DB 落盘** |

### Day 4 预告

主题：**事件持久化（DB）+ 主键去重**

今天的设计已经为 Day 4 留好钩子：
- ✅ Event 结构体已经有 TxHash + LogIndex + BlockNumber
- ✅ onEvent 是接 DB 的天然位置
- ✅ "1 个消费者写 DB 不需要锁"——明天会被验证

**预测**：Day 4 main.go 改动 ≤ 10 行，indexer.go 0 行。如果改超过 20 行，今天的抽象有问题。

---

## 💡 元认知收获

### 今天最值的一条
> **ctx 和 WG 的心智分工**——这是今天**质变**而不是量变的认知升级。
> 它的复利效应特别强：未来任何 daemon 程序都跑不掉这个 pattern。

### 今天最大的迷茫
> **Indexer vs Event 的混淆**——把 struct 当成 C++ 类，从字段角度找价值。
> 通过深问解决：**字段是工具，方法是价值**——完成了思维范式的迁移。

### 元认知警示
今天反驳教练数 = 0（Day 1: 3 / Day 2: 4）。
- 部分原因是 A：教练讲得更准
- 部分原因是 C：深问代替了反驳（4 个高质量深问）
- 但要警觉 D：开始过度信任教练
- **明天 Day 4 留意**：遇到指令/纠正时，问自己一秒"这个判断的依据是什么？我同意吗？"

---

## 📝 验收清单

### 必须项 ✅ 全部完成
- ✅ 开局必答（3 goroutine 退出顺序）答到 🟢
- ✅ Q1/Q2/Q3 三题全部 🟢（部分有提示）
- ✅ `internal/indexer/indexer.go` 创建完成
- ✅ 生产者 + 消费者两个 goroutine
- ✅ sync.WaitGroup 正确使用
- ✅ 生产者 defer close(eventCh)
- ✅ 生产者没有 close(sink)
- ✅ 消费者用 for range eventCh
- ✅ sub.Err() == nil 分支真处理
- ✅ eventCh <- e 包在嵌套 select 里
- ✅ cmd/watch/main.go 改造完成，main 变薄
- ✅ trigger 补完投票 + 结算
- ✅ 端到端跑通
- ✅ 反压实验跑过

### 深度项 ✅ 全部达成
- ✅ 能画出 7 节点完整数据流图
- ✅ 能解释 sink vs eventCh 的 close 不对称
- ✅ 能解释为什么今天 for range eventCh 对了
- ✅ 能解释 ctx 和 WaitGroup 的分工
- ✅ 反压实验后能描述传播链
- ✅ 验证设计：Day 4 接 DB 时 main.go 大约改 10 行

---

## 🎓 一句话总结今天

> **把"程序怎么响应停"和"程序怎么扩展"两件事，从"想得明白"升级到"代码里立得住"。**

---

