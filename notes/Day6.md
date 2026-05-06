# 阶段 2 Day 6 学习笔记：Sepolia + EIP-1559 + .env + 自愈重连


> **核心命题**：把 Day 5 的"本地能跑"扩展为"**真网能跑 + 抖动能扛**"
**里程碑**:阶段 2 长期意识 4 题**全部闭合** 🏆

---

## 📊 成果总览

### 必须项（全部达成 ✅）

- ✅ `.env` + `.env.example` + `.gitignore` 三件套
- ✅ godotenv 集成到 cmd/watch 和 cmd/trigger
- ✅ Alchemy 注册 + Sepolia API Key + 测试币领到
- ✅ `internal/account/gas.go` 的 SetEIP1559Gas 完成
- ✅ Sepolia 上成功部署合约 + 触发 SubmitProject
- ✅ indexer 在 Sepolia 上实时收到事件
- ✅ `internal/indexer/indexer.go`:runSession + retry 结构落地
- ✅ **indexer.Run 函数签名不变**(接口演进硬指标)
- ✅ **main.go 不包含任何重连代码**(内部弹性硬指标)
- ✅ 真实网络抖动场景下自愈成功(Alchemy 自带的 Chaos Monkey 🦍)
- ✅ 重连后 sync_state 续跑机制:无 silent gap

### 阶段 2 长期意识 4 题闭合状态

| 题 | 闭合状态 |
|---|---|
| 1. 7 天不重启的 daemon? | ✅ Day 2 |
| 2. 100 万事件累积泄漏? | ✅ Day 3 |
| **3. 网络抖动 3 秒?** | **✅ Day 6**(今天闭合)|
| 4. Ctrl+C 数据会丢吗? | ✅ Day 5 |

---

## 🎯 今日新增工具卡(#68 - #80)

### 配置 / 环境管理

#### #68 — `.env` 是开发期便利,不是部署期依赖

`godotenv.Load()` 失败必须可容忍。`.env` 不存在不是错误,是常态(生产环境用真环境变量)。

标准用法:
```go
_ = godotenv.Load()  // 显式忽略 error
```

#### #69 — 配置存放的"四象限"

| 维度 | 命令行参数 | 环境变量 | .env 文件 | 硬编码 |
|---|---|---|---|---|
| **生命周期** | 单次运行 | 这台机器的会话 | 这个项目的部署 | 永久 |
| **典型用途** | 起始块号、合约地址 | NETWORK、LOG_LEVEL | API_KEY、密码 | 公开常量 |
| **是否敏感** | 一般不敏感 | 一般不敏感 | **敏感** | 不敏感 |
| **是否进 git** | N/A | N/A | **不进** | 进 |

#### #70 — 敏感性分级 + 拼接策略

完整 URL 的不同部分可以**敏感性不一样**:
- `eth-sepolia.g.alchemy.com`(host)= 公开信息 → 硬编码
- `<API_KEY>` = 计费凭证 → .env

程序运行时拼接两部分。

---

### 类型与抽象

#### #71 — 业务字段用最自然的类型,转换发生在调用边界

具体应用:
- `ChainID` 用 `int64`(业务最自然),`*big.Int` 转换发生在调用 go-ethereum API 时
- `Event.ProjectID` 用 `uint64`(业务正数),`int64` 转换发生在 toRow

**反例(过度耦合)**:让 Indexer 直接持有 `*big.Int` 类型的字段——把 go-ethereum 的实现细节渗透到业务模型。

#### #72 — 业务模型 vs 持久化模型 各自语义忠诚

| 模型 | 忠诚于谁 | 字段类型选择依据 |
|---|---|---|
| **Event**(业务领域)| 合约 / 业务 | "ProjectID 不会负数" → uint64 |
| **EventRow**(持久化)| Postgres schema | "BIGINT 是 int64" → int64 |

**"统一类型"是 false consistency**——两个模型本来就该不一样,有不同的"忠诚对象"。

#### #73 — 跨包共享类型有方向性

A 复用 B 的类型 = A 依赖 B。如果未来 B 可能反过来依赖 A,这次复用就埋了循环依赖的雷。

**今天踩的真实坑**:Day 4 让 db 包用 `indexer.Event` 类型,Day 6 想让 indexer 调用 db.Repo → 循环依赖爆炸。

修复方案:db 包定义自己的 EventRow,转换在调用方。

---

### Run / runSession 设计模式

#### #74 — runSession 命名学:用名词锚定语义

`runSession` 比 `runOnce` / `watchEvent` 更好,因为:
- "session" **天然带 once 的含义**(一次会话有起止)
- "session" **暗示完整流程**(连接 + 监听 + 处理 + 断开)
- 工程师听到这词天然预期"有连接、有状态、有起止"

**心智锚点**:好名字不靠堆形容词,靠选对名词。

#### #75 — 内部弹性 vs 外部契约(Day 6 核心立柱)⭐⭐

```
        main.go(调用方)
            ↓ idx.Run(ctx, onEvent)
        ━━━━━━━━━━━━━━━
        Indexer 内部
        - 隐藏 sync + watch 的切换
        - 隐藏重连逻辑
        - 隐藏 sync_state 管理
```

main 关心**高层意图**(启动/停止),不关心**执行细节**(连接断了重试几次)。

具体兑现:Run 函数签名**不变**,但内部从"Day 5 一次性监听"演进为"Day 6 自愈循环",main.go 一行代码都不用改。

#### #76 — 工具卡 #67 + #48 是分层应用,不冲突

```
内部弹性(#67):runSession 失败 → Run 内部消化 → 调用方无感知
错误向上抛(#48):重试预算耗尽 → 包装成 fatal → 让 main 处理
```

**判断标准**:
- transient(网络抖动 / rate limit / 临时不可用)→ 内部消化
- fatal(配置错 / 代码 bug / 数据完整性)→ 上抛

---

### 错误处理与重试

#### #77 — fatal vs transient 判断公式

> **"如果立刻重启程序,错误会复现吗?"**
> - 一定复现 → fatal(配置 / 数据 / 代码 bug)
> - 大概率不复现 → transient(网络 / 临时不可用)

实际场景判断:

| 错误 | 标签 | 理由 |
|---|---|---|
| WS 被对端关闭 | 🟡 transient | 节点维护可恢复 |
| API_KEY 401 | 🔴 fatal | 重试一万次也是 401 |
| sync_state 非法值 | 🔴 fatal | **数据完整性问题** |
| 事件解码失败 | 🔴 fatal | **代码 bug** |
| Postgres 抖动 | 🟡 transient | DB 重连可恢复 |

#### #78 — 重试预算耗尽 = transient 升级 fatal

经典模式:
```go
const maxRetries = 10
if retries >= maxRetries {
    return fmt.Errorf("retry budget exhausted: %w", err)
    //                ↑↑↑↑↑↑↑↑↑↑↑↑↑↑↑↑↑↑↑↑
    //                包装让 main 看到"是真 fatal"而不是"普通抖动"
}
```

#### #79 — 并发代码里没有"普通的 sleep"⭐

**所有"等待"都必须可被取消**。

```go
// ❌ 反模式(阻塞,不响应 ctx)
time.Sleep(5 * time.Second)

// ✅ ctx-aware sleep
select {
case <-time.After(5 * time.Second):
case <-ctx.Done():
    return ctx.Err()
}
```

替换规则:
- `time.Sleep(d)` → `select { case <-time.After(d): case <-ctx.Done(): }`
- `<-channel` → `select { case <-channel: case <-ctx.Done(): }`

#### #80 — `ctx.Done()` vs `ctx.Err()` 是同一件事的两个视角

```
Done()  = channel,适合在 select 里【等待】
Err()   = error,适合【立刻查询】当前状态

两者同步变化:
  cancel 时 Done channel close,同时 Err() 开始返回非 nil
```

类比:闹钟的"铃声"(Done)vs "当前显示"(Err)。

**用法**:
```go
// 想"等"ctx 取消 → 用 Done
select { case <-ctx.Done(): return }

// 想"看一眼"ctx 状态 → 用 Err
if ctx.Err() != nil { return ctx.Err() }
```

---

### EIP-1559 Gas 模型

#### #81 — EIP-1559 Gas 三件套

```
BaseFee(协议算)+ Tip(用户给矿工)+ FeeCap(用户上限)
                ↓
实际付 = min(GasFeeCap, BaseFee + GasTipCap)
```

| 字段 | 谁定 | 流向 | 性质 |
|---|---|---|---|
| BaseFee | 协议算 | **销毁**(burn)| 通缩机制 |
| GasTipCap | 用户 | 矿工 | 激励 |
| GasFeeCap | 用户 | 总价上限(BaseFee+Tip)| 抗波动 |

**经典缓冲**:`GasFeeCap = 2 × BaseFee + Tip`,因为 BaseFee 每块最多变 12.5%,2 倍缓冲足够。

#### #82 — `big.Int` 运算的"输入污染"陷阱

```go
// ❌ 危险(修改了 header.BaseFee 本身!)
header.BaseFee.Mul(header.BaseFee, big.NewInt(2))

// ✅ 安全(创建新对象)
result := new(big.Int).Mul(header.BaseFee, big.NewInt(2))
```

**心智锚点**:`big.Int` 方法签名是 `(z, x, y)` —— `z` 被覆盖。永远用 `new(big.Int).Op(x, y)` 模式避免污染输入。

---

### 错误链与现代错误处理

#### #83 — `errors.Is` 是错误链的"穿透器"

```go
err1 := errors.New("network down")
err2 := fmt.Errorf("wrapped: %w", err1)

err2 == err1                    // ❌ false(不同对象)
errors.Is(err2, err1)           // ✅ true(err1 在 err2 的链里)
```

`errors.Is` 内部会**反复 unwrap** 沿着 `%w` 创建的链向下走,逐层比较。

**使用规则**:
- **永远别用 `==`** 比较 error(除非确定从未被 `%w` 包装)
- **永远用 `errors.Is(err, target)`** 检查"是不是某种错误"

具体应用(Day 6):
```go
if errors.Is(err, context.Canceled) {
    log.Println("👋 indexer 优雅退出")
    return  // 不算失败
}
log.Fatalf("start indexer failed: %v", err)
```

#### #84 — `err != nil` 的语义升级

| 时机 | `err != nil` 的意义 |
|---|---|
| Day 5 | 可能是网络抖动 / 主动退出 / fatal,main 一刀切 Fatal |
| Day 6 | **一定是 fatal**(transient 已被 Run 内部消化),main 处理时心里有数 |

---

### 架构设计

#### #85 — 结构体字段反映"能力边界"

当想给一个类型加新功能时,先问:**它现在拥有完成这件事所需的所有工具吗?**

不够 → **加字段**比"传参绕过"更诚实。

具体例子(Day 6):
- Day 5:`Indexer{contract}` —— 只能调合约
- Day 6:`Indexer{contract, repo, client}` —— 能 sync(需 repo + client),能 watch(需 contract),能管理 sync_state(需 repo)

#### #86 — 接口契约的"调用方圈子"决定稳定性强度

| 破坏的范围 | 严重度 | 工程上的姿态 |
|---|---|---|
| 公开 SDK / 库 | 🔴 严重破坏 | 必须 SemVer 升 major |
| 跨团队的内部包 | 🟡 中等 | 需要协调 |
| **同一项目内的内部调用** | 🟢 没关系 | 直接改即可 |

**修正了对 Day 5 工具卡 #65 的理解**:
- "演进 vs 破坏"主要约束**对外契约**(如 Run 函数)
- 项目内部 NewXxx 加参数 = 自由修改,不算"破坏"

#### #87 — 依赖注入(DI)= 自然的工程感

**核心**:对象需要的"工具"(依赖),不是它自己创造的,而是从外部传进来的。

```go
// ✅ DI:依赖从外部传入
func NewIndexer(contract, repo, client) *Indexer

// ❌ 反 DI:内部自己创建
func NewIndexer(addr string) *Indexer {
    db, _ := sqlx.Open(...)  // 自己造
    repo := db.NewRepo(database)  // 自己造
}
```

**好处**:
1. **测试友好**(可注入 mock)
2. **单一职责**(Indexer 只关心怎么用,不关心怎么来)
3. **灵活替换**(同一对象,不同环境注入不同实现)

**心智锚点**:Go 圈子普遍**手写组装**(main.go 里几行就完了)——"Just use main.go"。

---

### 工程哲学与权衡

#### #88 — "复用类型"是有方向性的(扩展 #73)

跨包共享数据类型时,**问两个问题**:
1. 这个类型未来会被哪些包依赖?
2. 依赖方向会变吗?

**Day 4 的债务在 Day 6 被还**:
- Day 4 让 db 用 indexer.Event(节省了重复定义)
- Day 6 indexer 反过来要调 db → 循环
- 修复:db 定义自己的 EventRow,转换在边界

#### #89 — 临时方案的"最优"≠ 长期最优

具体:Day 6 trigger 截断 sepolia 流程的方案选择
- "用 IsLocal 截断"长期看绑了不该绑的东西(语义混淆)
- 但**今天**对可读性 / 可恢复性更友好
- **解药**:加 TODO 注释 `// TODO: Day 7 改成 --minimal flag`

**心智锚点**:留 TODO 比强行重构强。

#### #90 — 安全网 vs 性能策略 ⭐ 修正 Day 5 判断

**ON CONFLICT 的合法用途**:
- 是 correctness 保险,**也是**应对真实场景(intra-block partial progress)的合法手段
- 不是"懒"的借口

**Day 5 立的"语义精确(+1)"是错的**——成立前提是"块级原子提交"。
当 onEvent 是事件级更新时,**from = lastSynced(重读最后一个块)** 才是正确语义。

#### #91 — 事件级进度 vs 扫描级进度

两个不同的概念:
- **事件级进度**(onEvent 更新):"最后一条已处理的事件所在的块"
- **扫描级进度**(syncBeforeWatch 末尾更新):"已扫描完的块范围"

两者必须**配合存在**:
- 只有事件级 → 漏掉"空块"的扫描记录
- 只有扫描级 → 丢失 intra-block 的进度细节

#### #92 — 防御检查的"必要性"取决于输入是否可能违反契约

- 公开 API 暴露给陌生调用方 → **必须防御**
- 项目内部、调用链已知的构造函数 → **防御可省略**(YAGNI)

但**有差别的防御**(对核心字段加保险,对已知非 nil 的字段不加)是真正的工程品味——不是一刀切。

#### #93 — "高级技术"不是好代码的指标 ⭐

> "高级技术 + 真实需求"才是。

具体:今天用固定 5 秒重试间隔(没用指数退避)
- 看到固定间隔重试 → 先问"它跑通了吗?"跑通了,合格 ✅
- 看到指数退避 + jitter → 先问"它解决了固定间隔的什么问题?"答不上 → 过度工程

#### #94 — "看似 transient 的错误"可能是真 fatal

具体:Alchemy 免费层 10 块限制 → 重试 10 次都是同样的错。

**今天的简化策略足够好**:
- 统一当 transient 处理 → 重试 10 次升级 fatal → 优雅退出
- 浪费 50 秒 retry 时间,但**没有无限循环**

Day 7 优化方向:用 `errors.Is` 识别 hard-coded fatal 立刻退出。

#### #95 — 反推陷阱有多种触发源 ⭐

| 类型 | 触发源 |
|---|---|
| **实现反推** | 用现有代码反推"应该这么设计" |
| **权威反推** | 用教练 / 资深同事的判断反推"那就是对的" |
| **流派反推** | 用业界主流做法反推"我也该这样" |
| **历史反推** | 用过去成功的方案反推"现在也该这么做" |

**共同症状**:跳过了"对当前问题的独立审视"。
**共同解药**:每次接受外部判断前,问 **"这个判断在我当前场景下,第一性原理是什么?"**

---

## 🛠️ 今日代码改动清单

### 新增文件

| 文件 | 用途 |
|---|---|
| `.env` | 敏感配置(NETWORK / DSN / API_KEY / PRIVATE_KEY)|
| `.env.example` | 队友配置模板(无敏感值)|
| `cmd/sepolia/main.go` | Sepolia 部署 + 触发交易,带断点暂停 |
| `internal/account/gas.go` | SetEIP1559Gas helper |
| `internal/config/network.go` | NetworkConfig + GetNetwork 函数 |

### 修改文件

| 文件 | 改动 |
|---|---|
| `.gitignore` | 加 `.env` |
| `go.mod` / `go.sum` | 加 godotenv 依赖 |
| `cmd/watch/main.go` | godotenv.Load + GetNetwork + main 大幅简化(只剩 idx.Run)+ errors.Is(ctx.Canceled)优雅退出 |
| `cmd/trigger/main.go` | godotenv.Load + 网络切换 |
| `internal/indexer/indexer.go` | **核心重构**:Indexer 结构体加 repo/client + runSession 拆分 + Run 包重试 + syncBeforeWatch 方法 |
| `internal/db/queries.go` | InsertEvent 签名 indexer.Event → EventRow + 删除 toRow + 删除 import indexer(修复循环依赖)|

### 关键架构演进

```
Day 5 main.go:
    repo.GetLastSyncedBlock(ctx)
    client.BlockNumber(ctx)
    if last <= current { idx.Sync(...) }
    repo.UpdateLastSyncedBlock(...)
    idx.Run(ctx, onEvent)

Day 6 main.go:
    idx.Run(ctx, onEvent)   ← 就这一行 ⭐
```

---

## 🔥 真实测试日志(Sepolia 自愈现场)

```
NETWORK = sepolia
⛳ 上次同步到块 10793263,现在最新块是 10793268
🔔 [块 10793266] projectId=0 name="My Project" submitter=0xbC6D... ✅ sync 收第 1 笔
✅ 历史同步完成,共 1 条
👀 开始监听...

订阅出错: read tcp 198.18.0.1:63147->198.18.0.138:443: read: connection reset by peer
[WARN] runSession 失败: ... 5s 后重试(1/10)        ← 真实抖动来了 🌪️

⛳ 上次同步到块 10793268,现在最新块是 10793271
🔔 [块 10793271] projectId=1 name="My Project2" ...  ← 重连后 sync 找回事件 2 ⭐
✅ 历史同步完成,共 1 条
👀 开始监听...                                       ← 又活了!

^C 收到信号: interrupt
👋 indexer 优雅退出                                   ← 优雅退出 ✅
```

**这一份日志一份证据同时验证**:
- ✅ Sepolia 真网接入
- ✅ syncBeforeWatch 自动化
- ✅ 自愈重连(被真实环境验证)
- ✅ 重连后 sync 续跑,无 silent gap
- ✅ ctx-aware 优雅退出

---

## 💬 反诘记录(14 次)

### 真反驳(7 次,事实级纠错)

1. **MetaMask 私钥和网络的关系**:主动暴露认知盲区,纠正"私钥按网络分"的错误假设
2. **trigger 的 Hardhat 私钥不能用于 Sepolia**:完全自驱发现 + 自己提出方案(新建 cmd/sepolia)
3. **教练没看到 NETWORK=hardhat**:直接事实纠错,避免了"跳过 Sepolia 验证"的严重疏忽
4. **"今天用不上 retries 重置"自相矛盾**:精确引用教练之前的话反诘,逼教练承认前后判断不一致
5. **"+1 是精确语义"自相矛盾**:拎出 intra-block partial progress 这个 corner case,推翻教练 + 自己之前的判断
6. **批评教练违反"小步快跑"纪律**:元层面反驳,强制教练回到正轨
7. **NewIndexer 不应该传 *sqlx.DB**:基于自己代码事实(已有 db.Repo)的反诘

### 求知式深问(6 次,底层机制级)

1. **`.env` 是文件还是文件类型?**(基础认知盲区)
2. **为什么是 watch 不是 listen?**(领域语义)
3. **`big.NewInt` vs `1e9` 的差异**(类型隐式转换)
4. **`ctx.Err()` 和 `ctx.Done()` 是什么关系?**(底层机制)
5. **`errors.Is` 为什么不能直接用 `==`?**(错误链原理)
6. **用 IsLocal 判断"是否跑全套"有问题吗?**(对自己之前判断的反思)

### 工作经验对比(1 次,极高质量)

主动引用 EV 智能汽车软件升级流程:
- 固定 30s 重试 × 3 次的 Conservative Backoff
- 引出"Conservative vs Exponential Backoff"两个流派的对比讨论

---

## 🎓 元认知反思

### Q1:今天最值的一条知识

**自己实现一遍错误处理流程**——从"看过(VPN)"到"用过(EV 升级)"再到"写过(今天)"是 3 个不同的认知层次。

### Q2:今天最大的坑

**Run 函数的状态机一开始很懵**,不知道:
- ctx 错误怎么处理?
- err == nil 表示什么?
- 什么时候增加重试次数?
- 等待用什么(以为是 sleep,结果是 select+ctx-aware)

最终用 3 条规则压缩了整个状态机:
```
err != nil + 没超限 → 重试
ctx.Err != nil       → 退出
重试超限            → 退出(升级 fatal)
```

### Q3:反驳 + 深问元认知

**14 次**反诘——超 Day 5 的 5 次基线 280%。其中**反驳教练教学节奏**(批评 1)是 Day 6 最有勇气的瞬间。

### Q4:实现反推语义防御

掉进了**"权威反推"**(接受教练 +1 的判断没独立审视),但很快**自己爬出来**(主动反诘"为什么要加等号")。教练新增工具卡 #95:反推陷阱有多种触发源。

### Q5:Day 7 预测

- ✅ events 表加字段(标记 reorg removed)→ 演进
- ✅ 日志规范化(log → slog)→ 包内重构,演进
- ✅ Run 函数签名不变 → 硬指标延续
- 主动列出 TODO:
  - errors.Is 区分 fatal vs transient
  - 指数退避 + jitter
  - Circuit Breaker 风格的 retries 重置

---

## 🚀 Day 7 预告

### 主题:reorg 处理 + 结构化日志 + 阶段 2 收官

**预测 Day 7 形态变化**:
- `log.Printf` → 结构化日志(slog 标准库 / zap)
- 事件 onEvent 内部判断 `event.Raw.Removed`(reorg 场景)
- DB schema 加列(标记 reverted 事件)
- 完整的 v2.0 收官总结

### Day 6 留下的 TODO

- [ ] **fatal vs transient 精细分类**:用 `errors.Is` 立刻识别 hard-coded fatal,不浪费 retry 预算
- [ ] **指数退避 + jitter**:`5s → 2s → 4s → ... → 30s 封顶`,加随机化避免雪崩
- [ ] **Circuit Breaker 风格 retries 重置**:不是"成功就重置",而是"成功一段时间才重置"
- [ ] **chunked Sync**:当前撞 Alchemy 免费层 10 块限制时手动调整 sync_state 临时绕过,Day 7 加分批
- [ ] **trigger / sepolia 公共逻辑抽象**:目前两个 cmd 有重复代码,用 `--minimal` flag 统一
- [ ] **uint64 ↔ int64 转换纪律**:Day 4 的钩子,Day 6 决定 YAGNI 不动,等 Day 7 看真实需求

### 留给未来的"加固方向"

- jitter(避免羊群效应)
- 双 client(HTTP for Filter + WS for Watch)
- HashiCorp Vault 等密钥管理工具
- 自动化集成测试(故障注入框架)

---

## 📚 引用的工具卡(完整清单)

### 今日新立(#68 - #95,共 28 张)

#68 .env 工作流 / #69 配置四象限 / #70 敏感性分级
#71 业务字段最自然类型 / #72 业务模型 vs 持久化模型 / #73 跨包类型方向性
#74 runSession 命名 / #75 内部弹性 vs 外部契约 ⭐ / #76 #67+#48 分层应用
#77 fatal vs transient 公式 / #78 重试预算耗尽升级 / #79 ctx-aware sleep ⭐ / #80 ctx.Done vs ctx.Err
#81 EIP-1559 三件套 / #82 big.Int 输入污染陷阱
#83 errors.Is 穿透器 / #84 err!=nil 语义升级
#85 结构体字段反映能力边界 / #86 调用方圈子决定稳定性
#87 依赖注入(DI)/ #88 复用方向性 / #89 临时方案最优≠长期最优 / #90 安全网 vs 性能策略 ⭐
#91 事件级 vs 扫描级进度 / #92 防御检查必要性 / #93 高级技术≠好代码 / #94 看似 transient 的真 fatal
#95 反推陷阱多种触发源 ⭐

### 今日反复触发的旧卡

- #48 错误向上抛
- #54 接口先行
- #56 修改集中目标随变更类型变化
- #58 at-least-once + idempotent
- #61 at-least-once 边界
- #65 演进 vs 破坏公式(今日修正:仅约束对外契约)
- #67 内部弹性 vs 外部契约 ⭐(今日真正立柱)

---

## 🏆 Day 6 总评

**成就解锁**:
- 阶段 2 长期意识 4 题**全部闭合** 🎯
- 工具卡数 67 → 95(新增 28 张,**单日历史最高**)
- 反诘密度 14 次(超 Day 5 基线 280%)
- 第一次在真实公链上做 production-grade 故障验证

**最关键的一句话**:

> "**当一个抽象设计得足够好,'我用什么机制实现'对调用方变得不可观测。**"

—— 这是今天 Day 6 最深刻的工程哲学,也是所有"工具卡 + 心智锚点"的共同源头。
