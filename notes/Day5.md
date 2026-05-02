# 阶段 2 Day 5：接口演进与历史同步

> **主题**：让 indexer 重启后从上次进度续跑 —— 把 Day 4 的"实时索引"扩展为"启动同步 + 实时订阅"两段无缝衔接。
>
> **核心仪式**：第一次面对 `indexer.go` 必然要改的真实压力测试 —— 但改的方式应该是"演进"不是"破坏"。

---

## 🎯 Day 5 核心成就

1. ✅ 第一次写 FilterXxx，掌握迭代器四件套
2. ✅ 第一次做"接口演进 vs 破坏"的真实工程决策
3. ✅ 完整闭合阶段 2 长期题"Ctrl+C 数据会丢吗"
4. ✅ 把"演进 vs 破坏"提炼成通用判断公式
5. ✅ 反驳教练至少 5 次，反向洞察 Rust 所有权设计

---

## 第一部分：核心心智地基

### 1.1 演进式修改 vs 破坏式修改

**核心认知**：修改已完成的代码 ≠ 设计失败。关键看**修改的形态**。

#### 判断公式（工具卡 #65）

```
1. 这次改动新增了什么？
   - 新方法 / 新字段 / 新文件 → 永远是演进 ✅

2. 这次改动触动了什么已存在的公开契约？
   - 导出方法签名 / 公开字段类型 / 接口定义 → 触动 = 破坏 ❌

3. 私有部分怎么改都行
   - 除非破坏了语义但保留了签名（更隐蔽的破坏，Day 6+ reorg 主题）
```

#### Go 中的可见性边界

| 改动 | 公有 vs 私有 | 演进还是破坏？ |
|---|---|---|
| 新增 `Sync` 方法 | 公有但**新增** | ✅ 演进 |
| 改 `Run` 签名 | 公有，**修改** | ❌ 破坏 |
| `Indexer` 加私有字段 `httpClient` | 私有 | ✅ 演进 |
| `convertToEvent` 内部改实现 | 私有函数 | ✅ 演进 |
| `Event` 加新字段 `Removed bool` | 公有但**新增** | ✅ 演进（编译兼容）|

**关键认知**：Go 用首字母大小写表达可见性，这个机制和"接口演进 vs 破坏"完美对齐 —— 公开的契约（导出方法、公开字段、签名）不能动，**内部实现想怎么改怎么改**。

#### 编译兼容 ≠ 语义兼容（工具卡 #57）

| 维度 | 加字段 | 改签名 |
|---|---|---|
| 编译兼容 | ✅ | ❌ |
| 语义兼容 | ⚠️ 看情况 | ❌ 通常破坏 |
| 算演进吗？ | ✅ 算 | ❌ 不算 |

**Go 结构体加字段为什么编译兼容**：
- Go 支持具名字段初始化 `Event{BlockNumber: 100, TxHash: "0xabc"}`
- 加新字段后，旧代码不写新字段 → **新字段自动取零值**
- 老代码无需修改即可继续编译

但**语义可能受影响** —— 比如 `Removed bool` 字段，老代码不感知，可能把"撤回事件"也写进 DB 造成脏数据。这是 Day 6 reorg 主题。

---

### 1.2 接口演进决策的 3 个候选方案

面对"加 backfill 能力"这个需求，有 3 种改造方式：

#### 方案 A：新增 Sync 方法，Run 不动 ⭐ 今天选定

```go
func (idx *Indexer) Sync(ctx, fromBlock, toBlock uint64, onEvent func(Event) error) error
func (idx *Indexer) Run(ctx, onEvent func(Event) error) error  // 完全不动
// 调用方：先 Sync 再 Run
```

- ✅ 旧调用方零迁移成本
- ✅ 职责单一：Sync 管历史，Run 管现在+未来
- ✅ 测试友好：单独测 Sync 容易
- ⚠️ 调用方要写两段，可能调错顺序

#### 方案 B：扩展 Run 参数

```go
func (idx *Indexer) Run(ctx, opts RunOptions, onEvent ...) error
```

- ❌ 破坏式改 —— 所有已有调用方必须改
- ❌ Run 内部职责变多

#### 方案 C：新增 RunWithBackfill，旧 Run 也保留

```go
func (idx *Indexer) RunWithBackfill(ctx, fromBlock, onEvent ...) error
func (idx *Indexer) Run(ctx, onEvent ...) error  // 也保留
```

- ✅ 旧调用方不破坏
- ⚠️ indexer 多一个方法，可能冗余

#### 选 A 的真实理由

> "Sync 管读历史，Run 管监听现在 & 未来" —— 这句话本身就是 indexer 包对外的契约文档。

---

### 1.3 工程师真正的核心循环

加新功能时的标准心智流程：

```
1. 来了一个新需求："加 backfill 能力"

2. 拆成小功能：
   - 拉历史事件 → FilterXxx（新写）
   - 持久化事件 → InsertEvent（已有，复用）
   - 持久化进度 → UpdateLastSyncedBlock（新写）
   - 决策起点终点 → main 组合（新写）

3. 已有的不动，没有的新增

4. → 自然就是"演进式改"
```

**实用判断口诀**：
1. 这个新功能能拆成几个小步骤？
2. 每个小步骤，是不是已经有代码做这件事了？
3. 没有的那部分，是新增方法/字段，还是改现有的？

---

## 第二部分：FilterXxx —— 第一次实操

### 2.1 FilterXxx vs WatchXxx 的本质差异

| | WatchXxx | FilterXxx |
|---|---|---|
| **底层 RPC** | `eth_subscribe`（必须 WS） | `eth_getLogs`（HTTP/WS 都行） |
| **数据流向** | Push（节点推你） | Pull（你拉节点） |
| **取数据接口** | `chan` 接收 | `iter.Next()` 推进 |
| **出错检查** | `<-sub.Err()` | `iter.Error()` |
| **资源释放** | `sub.Unsubscribe()` | `iter.Close()` |
| **生命周期** | 长连接，看着未来 | 一次请求,看过去 |
| **类比** | 数据库 trigger | SQL 查询 |

**心智锚**：**Watch 像 trigger，Filter 像 SQL 查询**。同一个连接都能干。

**为什么 ws client 也能跑 FilterXxx**：`eth_getLogs` 是个 RPC 方法，HTTP 端口和 WS 端口都接受 RPC。生产环境拆双 client 是为了**职责分离**和**套餐成本**，不是技术必须。

---

### 2.2 FilterXxx 迭代器四件套

```go
opts := &bind.FilterOpts{
    Start:   fromBlock,     // uint64，起点（含）
    End:     &toBlock,      // *uint64，终点（含），nil = "到最新"
    Context: ctx,
}

iter, err := contract.FilterProjectSubmitted(opts, nil, nil)
if err != nil {
    return fmt.Errorf("filter events: %w", err)
}
defer iter.Close()              // ① 必须释放

for iter.Next() {                // ② 推进游标
    ev := iter.Event             // ③ 拿当前事件
    // 处理...
}

if err := iter.Error(); err != nil {  // ④ 区分"正常结束"vs"中途出错"
    return fmt.Errorf("iterate events: %w", err)
}
```

#### 关键机制

**`iter.Next()` 出错就立刻停止**：
- 返回 true → 还有下一个事件
- 返回 false → 1) 正常结束 OR 2) 出错停止
- 一旦出错，循环立刻退出，**根本不会走到"最后一个"**
- 所以 `iter.Error()` 永远只有一个值

**`FilterOpts.End` 为什么是 `*uint64`**：
- Go 没有 `Optional<T>`
- "可选字段"用指针表达：`nil` = 没设置（节点理解为"到最新"）
- 必须取变量地址：`End: &toBlock` ✅，`End: &123` ❌

#### Go 标准库的"流式读取 + 后置错误检查"通用模式

`bufio.Scanner`、`sql.Rows`、`json.Decoder`、`abigen FilterIterator` —— 全是这套：

```go
for scanner.Scan() { ... }
if err := scanner.Err(); err != nil { ... }
```

**心智锚**：**Next() 是"还能继续吗？"，Err() 是"刚才为啥停下？"**

---

### 2.3 Sync 函数最终实现

```go
func (idx *Indexer) Sync(
    ctx context.Context,
    fromBlock, toBlock uint64,
    onEvent func(Event) error,
) error {
    opts := &bind.FilterOpts{
        Start:   fromBlock,
        End:     &toBlock,
        Context: ctx,
    }

    iter, err := idx.contract.FilterProjectSubmitted(opts, nil, nil)
    if err != nil {
        return fmt.Errorf("filter events: %w", err)
    }
    defer iter.Close()

    for iter.Next() {
        ev := convertToEvent(iter.Event)  // 复用 Day 3/4 防腐层
        if err := onEvent(ev); err != nil {
            log.Printf("处理失败 (tx=%s block=%d projectId=%d): %v",
                ev.TxHash, ev.BlockNumber, ev.ProjectID, err)
            return err  // fail fast
        }
    }
    if err := iter.Error(); err != nil {
        return fmt.Errorf("iterate events: %w", err)
    }
    log.Println("✅ 同步完成")
    return nil
}
```

#### 设计要点

1. **签名设计**：`fromBlock, toBlock uint64` 由调用方传入
   - indexer 包不应认识 DB（依赖方向规则）
   - "上次同步到哪" 是调用方的事，不是 indexer 的事

2. **复用 convertToEvent**：防腐层不破，链上类型不泄漏到 indexer.Event 之外

3. **错误向上抛**：onEvent 报错立刻 return，fail fast 优于默默吞错

4. **`iter.Error()` 调用纪律**：先存变量再用
   ```go
   if err := iter.Error(); err != nil {
       return fmt.Errorf("iterate events: %w", err)
   }
   ```
   原因：可读性 + 抗未来变更 + debug 友好。Rust 中这是所有权问题，Go 中是工程纪律。

---

## 第三部分：DB 层 —— sync_state 单行表

### 3.1 单行表（singleton row）设计

```sql
CREATE TABLE IF NOT EXISTS sync_state (
    id          INT          PRIMARY KEY DEFAULT 1,
    last_block  BIGINT       NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO sync_state (id, last_block) VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;
```

#### 设计哲学对比

| 维度 | events 表 | sync_state 表 |
|---|---|---|
| 数据本质 | 多条独立的事件**记录** | 一个全局**状态** |
| 行数 | N 条（持续增长） | 永远 1 行 |
| 主要操作 | INSERT | UPDATE |
| id 的作用 | 区分不同事件（代理主键） | 纯粹是 PRIMARY KEY 的形式要求 |

**`id INT PRIMARY KEY DEFAULT 1` 的真实含义**：
> "硬性规定这张表只能有一行（id=1），不允许插入其他行 —— 这是约定，不是技术保证。"

#### 配置数据存储决策树（工具卡 #60）

| 场景 | 存储介质 |
|---|---|
| 多个独立可变的键（feature flags / A/B 配置） | KV 表 |
| 单一进度/状态，且需事务性原子更新 | **单行表** |
| 启动时读一次、运行中不变 | 配置文件 / .env |

**单行表的灵魂是事务性，不是省地方**：
```sql
BEGIN;
INSERT INTO events ...;
UPDATE sync_state SET last_block = ...;
COMMIT;
```
事件入库 + 进度更新**要么都成要么都不成** —— 只有 DB 才能给这种保证。

---

### 3.2 Repo 方法实现

```go
const getLastSyncedBlockSQL = `
    SELECT last_block FROM sync_state WHERE id = 1
`
const updateLastSyncedBlockSQL = `
    UPDATE sync_state SET last_block = $1, updated_at = NOW() WHERE id = 1
`

func (r *Repo) GetLastSyncedBlock(ctx context.Context) (uint64, error) {
    var lastSyncedBlock uint64
    err := r.db.GetContext(ctx, &lastSyncedBlock, getLastSyncedBlockSQL)
    if err != nil {
        return 0, fmt.Errorf("get last synced block: %w", err)
    }
    return lastSyncedBlock, nil
}

func (r *Repo) UpdateLastSyncedBlock(ctx context.Context, block uint64) error {
    _, err := r.db.ExecContext(ctx, updateLastSyncedBlockSQL, block)
    if err != nil {
        return fmt.Errorf("update last synced block: %w", err)
    }
    return nil
}
```

#### 关键认知

**1. sqlx API 选择**：
- `GetContext` 取 1 行（位置参数 `$1`，自动 scan 进 dest）
- `Select` 取多行
- `ExecContext` 写入不需要返回结果

**2. `updated_at = NOW()` 必须显式写**：
- Postgres 的 `DEFAULT NOW()` 只在两种情况生效：
  1. INSERT 时没指定该字段
  2. UPDATE 时显式写 `updated_at = DEFAULT`
- UPDATE 时只动 `last_block`，`updated_at` **默认不会自动刷新**
- 想刷新就必须手动写

**生产彩蛋**：可以用 trigger 强制自动更新：
```sql
CREATE TRIGGER update_sync_state_modtime
BEFORE UPDATE ON sync_state
FOR EACH ROW
EXECUTE FUNCTION update_modified_column();
```

**3. uint64 vs int64 边界纪律（工具卡 #40）**：
- Go 的 `uint64` ↔ Postgres 的 `BIGINT`（有符号 int64）
- 显式转换更稳：`int64(block)` / `uint64(blk)`
- 抗驱动版本差异 + 代码意图清晰

---

## 第四部分：main.go 改造 —— 串联流程

### 4.1 启动流程

```go
// 1. 读 DB：上次同步到哪一块
lastSynced, err := repo.GetLastSyncedBlock(ctx)
if err != nil {
    log.Fatalf("get last synced block failed: %v", err)
}

// 2. 读链：当前最新块
currentBlock, err := client.BlockNumber(ctx)
if err != nil {
    log.Fatalf("get current block failed: %v", err)
}

// 3. 定义 callback（Sync 和 Run 共用）
onEvent := func(e indexer.Event) error {
    fmt.Printf("🔔 [块 %d] projectId=%d name=%q submitter=%s tx=%s\n",
        e.BlockNumber, e.ProjectID, e.Name, e.Submitter, e.TxHash[:10]+"...")
    if err := repo.InsertEvent(ctx, e); err != nil {
        log.Printf("insert failed: tx=%s block=%d err=%v", e.TxHash, e.BlockNumber, err)
        return err
    }
    return repo.UpdateLastSyncedBlock(ctx, e.BlockNumber)
}

// 4. 决策：要不要 Sync？（注意是 <=，不是 <）
if lastSynced <= currentBlock {
    fmt.Printf("⛳ 上次同步到块 %d，现在最新块是 %d\n", lastSynced, currentBlock)
    // TODO(Day 6+): Sync 完后显式 UpdateLastSyncedBlock(ctx, currentBlock)
    //   当前依赖 onEvent 推进 sync_state，导致"扫描了空块但 sync_state 不更新"
    //   本地 Hardhat 块少没影响，Sepolia/主网回填时必须改
    if err := idx.Sync(ctx, lastSynced, currentBlock, onEvent); err != nil {
        log.Fatalf("initial sync failed: %v", err)
    }
} else {
    log.Println("✅ 历史数据已是最新，跳过同步")
}

// 5. 接 Watch
if err := idx.Run(ctx, onEvent); err != nil {
    log.Fatalf("start indexer failed: %v", err)
}
```

---

### 4.2 关键设计决策

#### 决策 1：`lastSynced <= currentBlock`（用 `<=` 不是 `<`）

**理由**：启动瞬间区块可能从 100 变 101，`==` 时也要 Sync 做保险。重叠交给 ON CONFLICT 兜底。

**心智锚**：**"宁可重叠不可缝隙"** —— 重叠有 ON CONFLICT 兜底，缝隙没有任何兜底。

#### 决策 2：`fromBlock = lastSynced`（不是 `lastSynced + 1`）

**理由**：保守一点，宁可多写一个区块，有 ON CONFLICT 兜底。这是 Day 4 钩子 ① 在 Day 5 真正兑现工程价值的瞬间。

#### 决策 3：用 `BlockNumber` 不用 `HeaderByNumber`（工具卡 #41）

| | HeaderByNumber(ctx, nil) | BlockNumber(ctx) |
|---|---|---|
| 返回 | 整个 Header（~600 bytes） | 只返回 uint64（~10 bytes） |
| 底层 RPC | `eth_getBlockByNumber("latest", false)` | `eth_blockNumber` |
| 用途 | 需要其他 header 信息时 | **只需要块号时** |

**最小依赖原则**：调用 API 时只取你需要的最小信息。多取的字段不仅浪费流量，还增加心智负担。

#### 决策 4：Sync 和 Run 共用同一个 callback

**真实场景拆解**：

如果 Run 期间不更新 sync_state：
```
第一次启动：sync_state.last_block = 0
  Sync(0, 100) ✅
  Run() 处理 100-200 ✅
  Ctrl+C

第二次启动：sync_state.last_block 还是 0！
  Sync(0, 200) → 重新扫描 200 个块，全靠 ON CONFLICT 兜底，浪费！
```

**正解**：Run 阶段也必须更新 sync_state，因为 sync_state 是"全局进度"，不是"sync 阶段的私有变量"。

#### 决策 5：ctx 闭包捕获

```go
onEvent := func(e indexer.Event) error {
    err := repo.InsertEvent(ctx, e)  // ← 直接捕获外层 ctx
    return repo.UpdateLastSyncedBlock(ctx, e.BlockNumber)
}
```

**心智锚**：Go 闭包能捕获外层变量。callback 不需要把 ctx 写进签名也能访问 —— 但要意识到捕获的是哪个作用域的变量。Ctrl+C 时所有正在执行的 InsertEvent / UpdateLastSyncedBlock 都会被外层 ctx cancel。

---

## 第五部分：演示验证

### 5.1 场景一：首次运行

```bash
# 清空旧数据
docker exec eventindexer-postgres-1 \
  psql -U dev -d event_indexer \
  -c "TRUNCATE events, sync_state; INSERT INTO sync_state (id, last_block) VALUES (1, 0);"

# 部署 + 触发
go run ./cmd/trigger
# 拿到 <addr>

# 启动 indexer
go run ./cmd/watch <addr>
```

**期望输出**：
```
⛳ 上次同步到块 0，现在最新块是 4
🔔 [块 2] projectId=0 name="Alice's Project" ...   ← Sync 阶段
🔔 [块 3] projectId=1 name="Bob's Project" ...
🔔 [块 4] projectId=2 name="Carol's Project" ...
✅ 历史同步完成，共 3 条
👀 开始监听...
🔔 [块 5] projectId=3 ...   ← Watch 阶段
```

---

### 5.2 场景二：断点续跑

```bash
# 不清数据，直接重启
go run ./cmd/watch <addr>
```

**期望输出**：
```
⛳ 上次同步到块 7，现在最新块是 13
🔔 [块 7] projectId=5 ...
2026/05/02 17:15:44 event with tx_hash 0xee1ce103... already exists  ← ON CONFLICT 兜住！
✅ 历史同步完成，共 1 条
👀 开始监听...
```

#### 验证

```bash
docker exec eventindexer-postgres-1 \
  psql -U dev -d event_indexer \
  -c "SELECT * FROM sync_state; SELECT COUNT(*) FROM events;"
```

`events` 数量 = 实际唯一事件数（不重复）。

---

### 5.3 重要发现：链区块号 ≠ 业务事件数（工具卡 #62）

通过观察 Hardhat 日志发现：
- 块 7 = 第一次 trigger 的最后一笔（C's Project 提交）
- 块 8 = `evm_mine` 时间偏移产生的空块
- 块 9-11 = 投票阶段（三人投票）
- 块 12 = 又一次时间偏移空块
- 块 13 = 结算事件

**关键认知**：链上一直在动，但 indexer 只**订阅了 ProjectSubmitted 这一种事件**，所以投票/结算这些事件**根本不会触发 onEvent**。

**双时钟意识扩展**：
- 区块号 = **物理时钟**（链一直在转，每个 tx 一个块，空块也算）
- 订阅事件 = **业务时钟**（只关心 ProjectSubmitted）
- 两者**完全解耦**

#### Hardhat 出块特性

- Hardhat **按交易触发出块**（auto-mining 模式）—— 没交易就不出块
- Sepolia 不一样：每 12 秒一块，不管有没有交易
- 这是 Day 6 上 Sepolia 时会感受到的差异

---

## 第六部分：分布式系统心智

### 6.1 "现在"是程序对世界的一次快照（工具卡 #64）

```go
currentBlock, err := client.BlockNumber(ctx)
```

**关键认知**：
1. "现在"不是绝对时间，是**程序的相对时刻**
2. 这个"过期的快照"就是 at-least-once 的根源
3. 从你读 BlockNumber 到 Sync 真正开始 Filter，已经过去了几毫秒到几秒

**推广心智**：任何分布式系统里的"当前状态"，从你读到它的那一刻起，**已经过期了**。

设计能容忍"快照过期"的系统（at-least-once + idempotent），比追求"读到永远准的现在"（不可能）更工程化。

---

### 6.2 at-least-once + idempotent = exactly-once 效果（工具卡 #58）

**核心 pattern**：
- 与其费尽心思保证"只处理一次"（往往做不到）
- 不如允许"至少处理一次"（可能重复）+ 处理逻辑做成幂等（重复处理不出错）

**重叠区间的竞态窗口**：
```
时间轴：
  t1: 程序读 currentBlock = 100
  t2: 链上区块 100 出现新事件（Sync 还没读到）
  t3: Sync 执行 FilterXxx [0, 99]   ← 100 没包含
  t4: Sync 完成
  t5: Watch 调用 WatchProjectSubmitted（默认从订阅时刻最新块开始）
  t6: 节点说"区块 100 已经过去了"——你订阅的"未来"从 101 开始

→ 区块 100 的事件被漏掉
```

**解法**：让 Sync 终点 = 当前块（含），Watch 也包括当前块。重叠的部分靠 ON CONFLICT 兜住。

---

### 6.3 at-least-once 的边界（工具卡 #61）

**它能解决**：已经处理过的事件重复处理（ON CONFLICT 兜住）

**它不能解决**：从未处理过的事件被漏掉（**silent gap**）

**典型场景**：
- watch "悄悄漏了"（连 sub.Err 都没触发，比如节点静默丢了几个事件）
- sync_state 还是会被更新到最新事件的块号
- 中间漏的就**永远漏了**

**Day 6+ 的兜底方案**：定期回扫 / 高度低于当前块就触发 backfill。

---

### 6.4 内部弹性 vs 外部契约（工具卡 #67）

```go
// 调用方视角永远不变：
err := idx.Run(ctx, onEvent)
```

**调用方根本不需要知道**："Run 内部断了几次？重连了几次？"

它只关心：
- "Run 还在跑吗？" → ctx 没 cancel 就还在跑
- "Run 真的挂了吗？" → return error 才算挂

**封装的真实工程含义**：不是"信息隐藏"那么虚，而是"**让调用方的心智模型尽可能简单**"。

重试、重连、降级、缓存 —— 都是**内部弹性**，不暴露给调用方。

---

## 第七部分：今日新增工具卡（11 张）

| # | 名称 | 一句话 |
|---|---|---|
| #57 | 演进的代价分两层 | 编译兼容是必要条件，语义兼容是工程纪律 |
| #58 | at-least-once + idempotent | 与其追求"只处理一次"，不如允许重复 + 幂等处理 |
| #59 | 参数 vs 内部读取 | 不属于自己的依赖，让调用方传进来 |
| #60 | 配置数据存储决策树 | 单行表的灵魂是事务性 |
| #61 | at-least-once 的边界 | 解决重复处理，不解决 silent gap |
| #62 | 双时钟意识扩展 | 链区块号 ≠ 你订阅的事件 |
| #63 | 延迟优化的合法理由 | "今天不撞瓶颈"是真理由，"不符合业务逻辑"是软理由 |
| #64 | "现在"是程序的快照 | 任何分布式系统的"当前状态"，读到那一刻起就过期 |
| #65 | 演进 vs 破坏判断公式 | 新增永远是演进，触动公开契约就是破坏 |
| #66 | 跨语言对比是认知杠杆 | 一门语言的痛点是另一门语言的设计动机 |
| #67 | 内部弹性 vs 外部契约 | 重试/重连不暴露给调用方 |

**累计工具卡：67 张**（Day 4 末 56 张 + Day 5 新增 11 张）

---

## 第八部分：阶段 2 长期意识闭合

### 第 4 题完整闭合："Ctrl+C 数据会丢吗"

```
Day 4 闭合："已落盘事件不丢"半题（数据已经写进 DB 就不丢）
Day 5 闭合："shutdown 中丢一两条也能在重启时通过 backfill 追回"另半题
```

**完整闭合的逻辑链**：
1. 即使 shutdown 时 ctx cancel 让某条事件没写进去，重启时 sync_state 不会更新到那个块
2. 重启后 Sync 会从 sync_state.last_block 开始重新拉取那个区间
3. ON CONFLICT 兜住已存在的，补回缺失的
4. → at-least-once + idempotent = exactly-once 效果 ✅

---

## 第九部分：Day 6 形态变化预测

### 不变 / 演进 / 修改地图

```
✅ 完全不变（接口稳定，今天设计的红利）：
   - internal/indexer/indexer.Indexer 公开方法签名（Run / Sync）
   - internal/indexer/Event 结构体
   - internal/db/Repo 公开方法签名
   - convertToEvent 防腐层

🔧 内部演进（外部无感知）：
   - indexer.Run 内部加重连循环
   - indexer.Sync 内部加分批循环
   - main.go 读 .env 而不是硬编码

➕ 新增（不动旧的）：
   - .env 文件
   - 可能新增 RetryConfig 之类的私有结构
   - Event 可能新增 Removed bool 字段（reorg 用）

⚠️ 唯一可能破坏的（小心）：
   - sync_state 表 schema 改动（如果 reorg 需要回退）
```

**结论**：Day 6 是一次大改造，但**90% 是新增 + 内部修改**。这就是 Day 5 接口演进意识的真正回报。

---

## 第十部分：复盘核心反思

### 最值的一条知识

明确 Watch 和 Filter 的本质差异，更重要的是明确"**现在**"的定义 —— 这个 currentBlock 就是代码运行到 `client.BlockNumber(ctx)` 那一刻的快照。

这条认知串起：
- at-least-once 的根源
- 重叠区间的必要性
- ON CONFLICT 是工程刚需而非性能优化
- 分布式系统心智的入门

### 最大的坑

**认知错误**：简单地认为"修改已完成的代码 = 破坏 / 之前设计不充分"。

需要区分**演进式修改**和**设计不充分**：
- 新增功能，原代码公有部分不修改 → 演进，之前的设计 OK
- 新增功能需要修改原来公共部分 → 设计考虑不充分

### 反向洞察

Go 中 `iter.Error()` 调两次的纪律意识 → 反向理解 Rust 所有权的设计动机：
- Go 的选择：自由调用，靠程序员纪律避免重复调用副作用方法（写得快，需要经验）
- Rust 的选择：所有权 + move 语义，编译期强制（写得慢，编译过了就稳）

两个不是"对错"，是不同的设计哲学。

### 工程师核心能力推广

加新功能时的标准心智：
1. 拆成小功能
2. 能复用已有代码就复用
3. 不能复用的再自己写
4. 自然就是演进式改

**口诀**：
- 新增 → 演进
- 触动公开契约 → 停下，先问"是不是拆解不够细？"

---

## 第十一部分：明确不做（严控范围）

今天不引入 / 不处理：

- ❌ 双 client（HTTP for Filter + WS for Watch）→ Day 6
- ❌ .env / godotenv → Day 6
- ❌ 结构化日志 → Day 6
- ❌ reorg / Removed 字段 → Day 6
- ❌ 自愈重连 → Day 6
- ❌ 复杂 checkpoint（多 indexer / 多合约）→ 未来
- ❌ migrate 工具 → 未来
- ❌ 批处理优化（chunked filter）→ 未来

**不做也要有理由**（工具卡 #63）：
- "今天不撞瓶颈"
- "范围严控"
- "未来某个具体节点必须做"

---

## 📊 Day 5 验收总结

| 类别 | 项目 | 状态 |
|---|---|---|
| 必须项 | 演进决策有理由 | ✅ |
| 必须项 | indexer.go 只新增 Sync，Run 不动 | ✅ |
| 必须项 | sync_state 表 + Repo 方法 | ✅ |
| 必须项 | main.go 改造 | ✅ |
| 必须项 | Sync FilterOpts 四件套 | ✅ |
| 必须项 | Sync 复用 convertToEvent | ✅ |
| 必须项 | 场景一首次运行 | ✅ |
| 必须项 | 场景二断点续跑 | ✅ |
| 必须项 | 场景三重叠区间 | 🚫 主动跳过（成本/收益判断）|
| 深度项 | 讲清 ws 跑 Filter 为什么没问题 | ✅ |
| 深度项 | 用工具卡 #56 解释修改集中目标 | ✅ |
| 深度项 | 讲出 A/B/C 各自代价 | ✅ |
| 深度项 | 反驳教练 ≥ 1 次 | ✅ ≥ 5 次 |
| 深度项 | 预测 Day 6 形态变化 | ✅ |

**反驳教练高光时刻**：
1. "Day 4 预告说 Run 加参数 —— 这难道不是破坏式改吗？"
2. "这个'当前'指的是哪个区块？"
3. "不能直接 BlockNumber(ctx) 获得当前区块高吗？"
4. "iter.Error 调两次 —— Rust 中所有权转移会出大问题"
5. 主动看 hardhat 日志找证据解释 currentBlock=13

---

> **Day 5 收官** —— 阶段 2 第一次"必须修改 indexer.go"的真实压力测试，顶住了，并把"演进 vs 破坏"提炼成了通用判断框架。
>
> **Day 6 见**：Sepolia 真网 + EIP-1559 Gas + 自愈重连 + .env 管理。
