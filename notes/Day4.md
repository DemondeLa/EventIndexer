# 阶段 2 Day 4 学习笔记 — 事件持久化 + 抽象回报兑现

> **主题**：把 Day 3 的 `onEvent` 从"打日志"改为"写 Postgres",并亲眼验证 Day 3 抽象设计的成功。
>
> **最重要的事**:`internal/indexer/indexer.go` 修改 0 行,DB 接入完全封装在新的 `db` 包里。

---

## 🌟 三大里程碑

1. **抽象回报兑现**:`indexer.go` 0 行修改 → Day 3 的回调签名 `func(Event) error` 经受住考验
2. **反驳活力完全恢复**:不仅反驳"陷阱代码"(Q3 的 TOCTOU),还反驳"陷阱复盘"(认知诚实),还反驳了自己的第一反应(Q5.1 → Q5.2)
3. **认知重塑**:"三层结构体 = 形式主义" → "三层结构体 = 单一变化轴"

---

## 📐 一、架构与设计哲学

### 1.1 三层数据映射(今天最值的认知重塑)

```
┌────────────────────────────────────────────┐
│ 第 1 层:链上原始结构体(abigen 生成)         │
│   *winner.WinnerTakesAllProjectSubmitted    │
│   受合约 ABI 控制,合约升级会变               │
└────────────────┬───────────────────────────┘
                 │ Day 3 的 convertToEvent
                 ▼
┌────────────────────────────────────────────┐
│ 第 2 层:业务事件(indexer.Event)              │
│   业务稳定,跨场景复用(DB / API / 推送)      │
└────────────────┬───────────────────────────┘
                 │ Day 4 的 toRow
                 ▼
┌────────────────────────────────────────────┐
│ 第 3 层:DB 行(db.EventRow)                   │
│   受 schema 控制,db tag 映射列名             │
└────────────────────────────────────────────┘
```

**核心洞察 — "单一变化轴"**:每一层只服务一种变化轴:
- 业务字段会随**业务规则**变(加新事件类型)
- DB 字段会随**schema/检索需求**变(加索引列、分区列)
- 链上字段会随**合约 ABI** 变(合约升级)

**团在一起的代价**:一个结构体同时承载 3 种变化 → 任何变化都要改它 → 永远稳定不下来。

**分开的代价**:写转换代码(`toRow` / `convertToEvent`)= 付"分层税",换来每一层独立稳定。

### 1.2 抽象回报兑现仪式

**Day 3 的预言**:"DB 接入时,Run 函数不需要修改,只改 main.go 里那一块就 ok"

**Day 4 的事实**:
```
internal/indexer/indexer.go  |  0 ✅
cmd/watch/main.go            | 16 ++++++++++++++++
internal/db/                  | (全新增)
```

**机制**:Day 3 的 `Run(ctx context.Context, onEvent func(Event) error) error` 把"具体做什么"做成**注入点**,业务核心不需要知道下游是 log 还是 DB。这就是**控制反转 (Inversion of Control)** 和**依赖倒置**的体现。

### 1.3 接口先行 (Design Before Code)

git diff 看到 0 那一刻的"平静"感受 ↔ 设计透彻的奖励。

**接口设计的 3 步**:
1. 识别**关注点** — 拆成几个独立部分,每个服务一种变化轴
2. 设计**接口** — 不是实现;参数、返回值、错误就是契约
3. 验证**接口稳定性** — 想象 3 种未来变化,接口签名会不会变

### 1.4 防腐层 (Anti-Corruption Layer)

不同抽象边界之间放一个"翻译层",防止一边的变化污染另一边:
- 合约 ABI 变了 → 撞到 `indexer.Event` 这道墙 → 业务层不动
- schema 改了 → 撞到 `EventRow` 这道墙 → 业务层不动

### 1.5 依赖方向规则

```
indexer 包 ←──── db 包
   ↑                ↑
业务层(核心)     适配层(边缘)
不知道 DB 存在    懂业务、懂 DB
```

**箭头单向**:边缘依赖核心,核心不依赖边缘。这套思想叫 **Hexagonal Architecture / Ports and Adapters**。

---

## 🛠️ 二、Postgres + sqlx 技术细节

### 2.1 协议决定并发模型

| | ethclient (WebSocket)  | sqlx.DB (Postgres) |
|---|---|---|
| 底层 | 1 根 WS 连接 + 多路复用 | 连接池(N 根独立连接) |
| 并发方式 | 应用层 goroutine + id 派发 | 物理多根 TCP 同时跑 |
| 协议支持 | WS / HTTP/2 / gRPC 支持多路复用 | PG / MySQL / Redis 不支持 |

**心智锚点**:
- **多路复用** = "1 根管子塞多个请求,靠 id 区分"
- **连接池** = "多根管子各跑各的"

### 2.2 副作用导入 `_ "github.com/lib/pq"`

```go
import (
    "github.com/jmoiron/sqlx"
    _ "github.com/lib/pq"   // 副作用导入
)
```

**机制**:
1. `_` = "我故意不用这个包的符号,但要导入它"
2. 触发包的 `init()` 函数 → `sql.Register("postgres", &Driver{})`
3. 把驱动注册到 `database/sql` 的全局注册表
4. 之后 `sqlx.Connect("postgres", ...)` 才能从注册表查到

**漏掉会报错**:`sql: unknown driver "postgres"`(运行时错,不是编译错)

⚠️ IDE 经常自动删"未使用的导入" → 必须用 `_` 别名。

### 2.3 sqlx 字段映射(`db:` tag)

```go
type EventRow struct {
    ProjectID int64 `db:"project_id"`   // sqlx 反射读 tag
    Name      string `db:"name"`
}

db.NamedExecContext(ctx,
    "INSERT INTO events (project_id, name) VALUES (:project_id, :name)",
    row)   // 按 :占位符 → 字段名映射
```

**没有 tag 会怎样**:sqlx 默认用"字段名小写"匹配(`ProjectID` → `projectid`),驼峰转蛇形需要额外配置。

### 2.4 `database/sql` vs `sqlx`

```
database/sql(标准库)
   ├─ 参数按位置传(?, $1)
   ├─ rows.Scan(&a, &b, &c) 手动按列顺序映射
   └─ 必须 import 驱动(_ "github.com/lib/pq")

         ↓ sqlx 是它的"薄薄一层封装"

sqlx
   ├─ Connect(同时 Open + Ping)
   ├─ NamedExec(按结构体字段名传,更可读)
   ├─ Select / Get(自动 StructScan)
   └─ DB 类型嵌入 *sql.DB,向下兼容
```

### 2.5 Connect vs Open

```go
sqlx.Open("postgres", dsn)      // 懒加载,不真连
sqlx.Connect("postgres", dsn)   // = Open + Ping,确认能连上
```

**今天用 Connect** — fail-fast 是好工程。DB 是必需依赖,启动失败立刻报错比"等真用时才发现"好。

### 2.6 DSN 格式对比

```
PG (URL 格式):   postgres://dev:dev@localhost:5432/event_indexer?sslmode=disable
MySQL:           root:123456@tcp(127.0.0.1:3306)/realworld?charset=utf8mb4
```

DSN 格式由**驱动作者**自由决定,`database/sql` 只规定"驱动名 + DSN 字符串"两个参数。

### 2.7 sslmode

| 取值 | 含义 |
|---|---|
| `disable` | 完全不加密(本地开发 / docker localhost) |
| `require` | 必须加密,但不验证证书 |
| `verify-ca` | 必须加密 + 验证 CA |
| `verify-full` | 必须加密 + 验证证书 + 验证主机名(生产推荐) |

---

## 🗄️ 三、Schema 设计

### 3.1 完整 schema

```sql
CREATE TABLE IF NOT EXISTS events (
    id            BIGSERIAL    PRIMARY KEY,
    project_id    BIGINT       NOT NULL,
    name          TEXT         NOT NULL,
    url           TEXT         NOT NULL,
    submitter     TEXT         NOT NULL,
    tx_hash       TEXT         NOT NULL UNIQUE,
    block_number  BIGINT       NOT NULL,
    indexed_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### 3.2 代理键 vs 自然键

```
方案 X(自然键当主键):  PRIMARY KEY (tx_hash, log_index)
方案 Y(代理键):        PRIMARY KEY id (BIGSERIAL)
                       + UNIQUE (tx_hash, log_index)
```

**今天选代理键(Y)的理由**:
1. **外部引用紧凑**:8 字节 BIGINT vs 68 字节 (tx_hash + log_index)
2. **API/URL 友好**:`/events/12345` vs `/events/0xabc.../0`
3. **变化兜底**:改 UNIQUE 容易,改 PRIMARY KEY 牵连所有外部引用
4. **哲学层**:业务字段会变、会复用、会有特殊情况;id 是 DB 自己造的,**永远不变**

### 3.3 业务唯一 = NOT NULL + UNIQUE(SQL 标准的反常识)

```sql
CREATE TABLE foo (x TEXT UNIQUE);
INSERT INTO foo VALUES (NULL);   -- ✓
INSERT INTO foo VALUES (NULL);   -- ✓ 也成功!
```

**原因**:SQL 标准把 NULL 定义为"未知 (unknown)",`NULL = NULL` 结果是 NULL(不是 true),UNIQUE 不触发拒绝。

**结论**:任何"业务上必须唯一"的字段,**两个约束都加**。

### 3.4 约束放宽容易,收紧难

```
今天用 UNIQUE (tx_hash) 严格
明天接复杂合约 → 改 UNIQUE (tx_hash, log_index) 宽松 → 旧数据自动满足 ✓

反过来:
今天用 UNIQUE (tx_hash, log_index) 宽松
明天想严 → 旧数据可能违反新约束 → 必须先清洗
```

**设计原则**:**宁可一开始就严**。

### 3.5 双时钟意识 — `block_number` vs `indexed_at`

| | block_number(链上时间) | indexed_at(物理时间) |
|---|---|---|
| 本质 | 逻辑时钟(事件序号) | 物理时钟(人类时钟) |
| 谁定的 | 区块链 | indexer/PG |
| 表达 | 业务时序 | 运维时序 |
| 用途 | 业务逻辑 | debug、运维追溯 |

**核心洞察**:做业务逻辑看链上时间;debug 看物理时间。

**时钟权威归数据持有者**:让 PG 用 `DEFAULT CURRENT_TIMESTAMP` 自动填,不传 Go 端 `time.Now()`。理由:Go 拿事件 → 等网络/重试 → 真正写入 DB,**三个时间点都不同**,DB 端时间最贴近"数据真正落库的时刻"。

### 3.6 Postgres vs MySQL 关键差异

| | MySQL | Postgres |
|---|---|---|
| 占位符 | `?` | `$1, $2, $3` |
| 自增主键 | `AUTO_INCREMENT` | `BIGSERIAL` |
| 插入冲突忽略 | `INSERT IGNORE` | `ON CONFLICT (col) DO NOTHING` |
| 变长字符串 | `VARCHAR(255)` | `TEXT`(无长度限制) |
| 看表结构 | `DESC table` | `\d table` |
| 时间类型 | `DATETIME` / `TIMESTAMP` | `TIMESTAMPTZ`(推荐) |

### 3.7 PG TIMESTAMPTZ 的反直觉

⚠️ 名字里带 "TZ" 但**实际不存时区**:
- INSERT 时:把传入时间 → 转成 UTC → 存 8 字节整数
- SELECT 时:把 UTC → 转成会话时区返回

```
台北用户 INSERT '2025-12-21 10:00+08'
   ↓ 内部存:'2025-12-21 02:00 UTC'
东京用户 SELECT
   ↓ 返回:'2025-12-21 11:00+09'
```

---

## ⚡ 四、幂等写入

### 4.1 三种实现方式对比

```
❌ 方案 A:先 SELECT 再 INSERT(TOCTOU race)
   func InsertEvent(e) {
       if !exists(e.TxHash) { Insert(e) }
   }
   → 多消费者并发时,两个查到都不存在,两个都插入 = 重复

🟡 方案 B:让 INSERT 失败时忽略错误
   _, err := db.Exec("INSERT INTO events ...")
   if err != nil && isDuplicateError(err) { return nil }
   → 能用,但要识别 PG 错误码(23505),脆

✅ 方案 C:用 DB 原生的"插入或忽略"
   INSERT INTO events ... ON CONFLICT (tx_hash) DO NOTHING
   → DB 引擎在事务级别处理,无 race,无错误处理负担
```

### 4.2 TOCTOU race(Time Of Check to Time Of Use)

```
TOC (检查):   SELECT WHERE tx_hash = X  → 不存在
   ↓ ← ← ← ← ← ← ← ← ← 危险窗口(时间差)
TOU (使用):   INSERT X
```

**普遍现象**:不止数据库,文件系统、权限检查、库存扣减、缓存读写都常见。

**解法 3 选 1**:原子操作 / 事务 / 锁。
**最优**:DB 原生原子操作(`ON CONFLICT`)。

### 4.3 心智锚点

> **该数据库做的事,让数据库做。**
> "应用层先 SELECT 再 INSERT" 是把 DB 当成纯 KV 存储用,**浪费了 DB 真正的价值** — 它有事务、有约束、有原子操作,是为并发并发再并发设计的。

### 4.4 事务 ≠ 阻止 TOCTOU(默认隔离级别下)

PG 默认 `READ COMMITTED`,事务**只保证 A 自己的 SELECT 和 INSERT 之间一致**,不能阻止 B 在中间插入。要阻止 B,需要 `SERIALIZABLE` 或 `SELECT ... FOR UPDATE`。

但有 UNIQUE 约束兜底:B 的 INSERT 失败,要处理错误。**还是 ON CONFLICT 最干净**。

---

## 💻 五、代码核心结构

### 5.1 Repository Pattern

```go
type Repo struct {
    db *sqlx.DB    // 私有字段(小写)= 封装
}

func NewRepo(db *sqlx.DB) *Repo {
    return &Repo{db: db}
}

func (r *Repo) InsertEvent(ctx context.Context, e indexer.Event) error {
    // ...
}
```

**本质**:持有句柄 + 暴露能力。和 Day 3 的 `Indexer` 是同一种东西的不同实例。

**对外只暴露业务语义的方法**(`InsertEvent` / `ListEvents`),**隐藏实现细节**(具体 SQL、连接管理)。

### 5.2 Config + Connect 模式

```go
type Config struct {
    Host, User, Password, DBName, SSLMode string
    Port int
}

func (c Config) DSN() string {
    return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
        c.User, c.Password, c.Host, c.Port, c.DBName, c.SSLMode)
}

func Connect(cfg Config) (*sqlx.DB, error) {
    db, err := sqlx.Connect("postgres", cfg.DSN())
    if err != nil {
        return nil, fmt.Errorf("connect postgres: %w", err)
    }
    return db, nil
}
```

**设计意图**:
- Config struct vs 一坨参数:可扩展、可序列化、调用清晰
- DSN 是 Config 方法:职责单一、可测试、可复用
- 值接收 `(c Config)`:不修改、轻量、隐含不可变

### 5.3 toRow 转换函数

```go
func toRow(e indexer.Event) EventRow {
    return EventRow{
        ProjectID:   int64(e.ProjectID),     // 显式 uint64 → int64
        Name:        e.Name,
        URL:         e.URL,
        Submitter:   e.Submitter,
        TxHash:      e.TxHash,
        BlockNumber: int64(e.BlockNumber),
    }
}
```

**纯转换函数无 error**:只搬运、不计算、不查询、不副作用。

### 5.4 InsertEvent + 包级 SQL 常量

```go
const insertEventSQL = `
    INSERT INTO events (
        project_id, name, url, submitter, tx_hash, block_number
    ) VALUES (
        :project_id, :name, :url, :submitter, :tx_hash, :block_number
    )
    ON CONFLICT (tx_hash) DO NOTHING
`

func (r *Repo) InsertEvent(ctx context.Context, e indexer.Event) error {
    row := toRow(e)
    result, err := r.db.NamedExecContext(ctx, insertEventSQL, row)
    if err != nil {
        return fmt.Errorf("insert event: %w", err)
    }
    counts, err := result.RowsAffected()
    if err != nil {
        return fmt.Errorf("get rows affected: %w", err)
    }
    if counts == 0 {
        log.Printf("event with tx_hash %s already exists", e.TxHash)
    }
    return nil
}
```

**SQL 用包级 const** 而不是内联:
- 调用点干净
- 调试时可被引用
- 列名/占位符用对齐排版,肉眼对得齐

---

## ⚠️ 六、Go 错误处理与类型系统

### 6.1 错误向上抛(Error Bubbling Up)

**核心原则**:底层只负责发现错误、添加上下文,然后 return。决策(停/继续/重试/降级)留给最上层。

**反模式**:
```go
// ❌ 偷走上层的决策权
if err != nil {
    log.Printf("err: %v", err)
    return nil    // 吞掉错误,假装没发生
}
```

**打印 ≠ 处理。打印只是可观察,真正的处理是决定后果。**

### 6.2 `%w` vs `%v` vs `%s`

```go
fmt.Errorf("connect postgres: %s", err)   // 字符串拼接,链丢失
fmt.Errorf("connect postgres: %v", err)   // 默认格式,链丢失
fmt.Errorf("connect postgres: %w", err)   // ★ 错误包装,保留链
```

**只在 `err != nil` 时包**(否则 happy path 会被污染):
```go
// ❌ Bug 写法
err = fmt.Errorf("connect: %w", err)   // err 是 nil 时,包出"假错误"
return db, err

// ✅ 正确
if err != nil {
    return nil, fmt.Errorf("connect postgres: %w", err)
}
return db, nil
```

**`errors.Is` 沿链查根因**:
```go
if errors.Is(err, sql.ErrNoRows) { ... }
if errors.Is(err, context.Canceled) { ... }
```

### 6.3 错误前缀惯例

```go
// ❌ "failed to" 是冗余的
fmt.Errorf("failed to connect: %w", err)

// ✅ 只描述"在做什么"
fmt.Errorf("connect postgres: %w", err)
```

链最终长这样:`"connect postgres: connection refused"` — 简洁。

### 6.4 早 return 模式

```go
// ❌ 末尾合并
if err != nil { ... }
return result, err

// ✅ 早 return
if err != nil {
    return nil, fmt.Errorf("...: %w", err)
}
return result, nil
```

主路径在最后,无缩进,大脑负担轻。

### 6.5 类型边界要显式转换

`indexer.Event.ProjectID` 是 `uint64`,`EventRow.ProjectID` 是 `int64`(对应 PG `BIGINT`)。

**Go 不允许整型间隐式转换**,必须 `int64(e.ProjectID)`。

**为什么 PG 没有 unsigned BIGINT**:SQL 标准只有有符号整数,PG / Oracle / SQL Server 都遵循,只 MySQL 是孤例。

### 6.6 ctx 永远是参数,永远是第一个

```go
func (r *Repo) InsertEvent(ctx context.Context, e indexer.Event) error
//                          ↑ 第 1 参,固定位置
```

**为什么 DB 操作要传 ctx**:
1. **取消正在执行的查询**:Ctrl+C 时,sqlx 会通知 lib/pq → PG 中止当前查询
2. **超时控制**:`context.WithTimeout` 给单条查询设超时

**凡是可能"等"的操作(网络、磁盘、DB),第一个参数都该是 ctx**。

### 6.7 err == nil ≠ 业务成功

DB API 的 err 表达"SQL 执行成功/失败",**业务语义**(真的插入了?真的更新了?)藏在 `RowsAffected`。

`ON CONFLICT` / `UPDATE WHERE` / `DELETE WHERE` 都可能 `err==nil` 但 `RowsAffected==0`。

---

## 🐳 七、Docker Compose 入门

### 7.1 docker run ↔ docker-compose.yml

```bash
# docker run 命令
docker run -d -p 5432:5432 --name postgres \
  -e POSTGRES_USER=dev -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=event_indexer \
  -v pgdata:/var/lib/postgresql/data \
  postgres:16
```

```yaml
# 等价的 docker-compose.yml
services:
  postgres:                              # ↔ --name + image (一部分)
    image: postgres:16                   # ↔ 镜像
    ports:
      - "5432:5432"                      # ↔ -p
    environment:
      POSTGRES_USER: dev                 # ↔ -e
      POSTGRES_PASSWORD: dev
      POSTGRES_DB: event_indexer
    volumes:
      - pgdata:/var/lib/postgresql/data  # ↔ -v
volumes:
  pgdata:
```

### 7.2 三大优势

1. **可版本化**:提交进 git,同事 clone 后 `docker compose up -d`
2. **可组合多服务**:今天 PG,明天加 Redis、监控
3. **一键清场**:`docker compose down -v` 包括停容器、删网络、删 volume

### 7.3 named volume vs bind mount

```
bind mount(-v $PWD/data:/var/lib/mysql):
   优点:能直接看到主机目录里的文件
   缺点:路径绑死,跨机器不可移植;权限问题多

named volume(volumes: - pgdata:/var/lib/...):
   优点:docker 自己管,跨机器可移植
   缺点:看不到具体存哪了
```

**PG 用 named volume 是因为对数据目录权限敏感**,docker 完全托管省心。

### 7.4 容器命名规则

```
默认格式:  {项目名}_{服务名}_{序号}
例子:     eventindexer-postgres-1
```

**永远先 `docker compose ps` 确认容器名**,不要靠记。

### 7.5 PG 启动慢

第一次启动可能要 5-10 秒才接受连接,`docker compose up -d` 后立刻跑 indexer 可能报连接被拒。

### 7.6 国内网络拉镜像

`net/http: TLS handshake timeout` 是常见现象。解法:

**配置 Docker 镜像加速器**(Docker Desktop → Settings → Docker Engine):
```json
{
  "registry-mirrors": [
    "https://docker.m.daocloud.io",
    "https://dockerproxy.com",
    "https://mirror.baidubce.com"
  ]
}
```

---

## 🎯 八、元认知与设计哲学

### 8.1 抽象是延迟收益的赌注

```
抽象的代价 = 预付成本(代码量、理解成本、调试复杂度)
抽象的回报 = 未来某天兑现的延迟收益
```

**短命项目**:直接写死最快。
**长命项目**:抽象兑现得越多次越值。

Day 3 你预付了"理解回调"的成本,Day 4 你兑现了"DB 接入 0 修改"的收益。

### 8.2 第一反应可疑原则

直觉答案先怀疑:是真理解,还是"看起来该这样"?

**关键判断**:"这件事属于哪一层"比"要修改哪个文件"更根本。

(Day 4 体验:Q5.1 "main 肯定要改" → Q5.2 "其实属于 indexer 内部" 的自我修正。)

### 8.3 认知诚实 (Epistemic Honesty)

答案是什么就是什么 — 不为了"看起来深刻"过度包装。

**警惕**:复盘报告里硬挤"行动项"、教训总结里硬升华、PR 描述里硬找"亮点"。

(Day 4 体验:今天的坑都是 A 类知识/粗心,**不是**类型 B 思维陷阱 — 拒绝过度归因。)

### 8.4 直观代码 ≠ 正确代码

"新人能看懂"是优点,但不该成为决策依据。

决策应该看:边界条件、并发、失败模式、可维护性。

(典型例子:"先 SELECT 再 INSERT" 看起来直观,但藏着 TOCTOU race。)

### 8.5 "修改集中"的目标随变更类型变化

```
引入新依赖(DB / Cache / MQ)→ 业务核心 0 行修改是目标
加新业务功能(backfill / 新事件)→ 业务核心应该改
重构 / 优化           → 接口稳定,实现内部改
```

**用错目标**:陷入"不该改的硬不改、该改的不敢改"。

### 8.6 抽象失效的 3 个常见根因

1. **接口签名不够**:参数类型/数量不能表达新需求
2. **错误语义不清**:底层错误如何传给上层、上层怎么决策
3. **数据模型不全**:业务结构体字段不够用

(Day 4 体验:Day 3 的设计三个根因都不撞 — 这就是好抽象的标志。)

### 8.7 回调签名的"层级感"

回调收到的应该是**业务层结构**,不是上游的原始结构。

**反例**:传原始 HTTP request 给业务 handler → handler 被迫懂 HTTP。
**正例**:传 ParsedRequest 给 handler → handler 只关心业务。

Day 3 选 `func(Event) error` 而不是 `func(*winner.WinnerTakesAllProjectSubmitted)` 就是这个原则。

### 8.8 生产者-消费者抽象的家族

`Run(ctx, onEvent)` 的形态可以推广到:
- File scanner / stream consumer
- Pub/sub / event emitter
- Pipeline (Go stage processing)

共同骨架:**生产**和**消费**通过**回调或 channel** 解耦。

---

## 📋 九、Day 4 完整工作流回顾

### 9.1 修改地图

```
新增:
  docker-compose.yml
  migrations/001_create_events.sql
  internal/db/db.go         (Config / Connect)
  internal/db/queries.go    (Repo / EventRow / toRow / InsertEvent)

修改:
  cmd/watch/main.go         16 行(添加 DB 初始化 + onEvent 注入 repo)

不变:
  internal/indexer/indexer.go    0 行 ✅
```

### 9.2 端到端验证流程

```bash
# 1. 启动基础设施
docker compose up -d
docker exec -i eventindexer-postgres-1 \
  psql -U dev -d event_indexer < migrations/001_create_events.sql

# 2. 启动 trigger 部署合约,记下地址
go run ./cmd/trigger

# 3. 启动 indexer
go run ./cmd/watch <contract_address>

# 4. trigger 触发 3 笔提交
# → indexer 终端看到 3 行 "🔔 [块 X]..."

# 5. 验证 DB
docker exec -it eventindexer-postgres-1 \
  psql -U dev -d event_indexer \
  -c "SELECT project_id, name, tx_hash, block_number, indexed_at
      FROM events ORDER BY block_number"
# → 看到 3 行

# 6. Ctrl+C 退出 indexer

# 7. 重复步骤 5
# → 3 行【仍在】 ← "Ctrl+C 数据不丢"半题闭合
```

### 9.3 阶段 2 长期意识第 4 题闭合状态

| 问题 | 状态 |
|---|---|
| 1. 7 天不重启的 daemon? | ✅ Day 2 闭合 |
| 2. 100 万事件累积泄漏? | ✅ Day 3 闭合 |
| 3. 网络抖动 3 秒? | 🔧 Day 6 解决 |
| **4. Ctrl+C 数据会丢吗?** | **✅ Day 4 闭合"已落盘的事件不丢"半题** |

⚠️ 第 4 题严格说还有 0.5 半题:shutdown **过程中**正在写的那一条事件,如果 ctx 被 cancel 可能写不进去。Day 5+ 讨论"drain on shutdown"时再处理。

---

## 🔮 十、Day 5 预告

**主题**:历史事件同步(backfill)+ 启动对齐

**今天留下的钩子**:
1. **`ON CONFLICT DO NOTHING`** → backfill 末尾和 watch 开始的重叠区天然去重
2. **`repo.InsertEvent(ctx, indexer.Event)`** 通用接口 → backfill 完全复用今天的 DB 写入
3. **`EventRow` 删了 `ID` 字段** → Day 5 SELECT 时可能要加回(YAGNI 原则)

**预测**:
- `internal/indexer/indexer.go` **会改** — 增加 backfill 阶段(这次不再是 0 行)
- `internal/indexer/indexer.go` 的 Run 接口**会演进** — 增加 `RunOptions { BackfillFromBlock: ... }`
- "indexer.go 0 行修改"不是永恒目标,**变更类型**决定该改哪里
- "从哪个块开始"是调用方策略,不是 indexer 内部知识

**关键认知**:接口演进 ≠ 接口破坏,接口本就该随业务演进。

---

## 📚 附录:Day 4 工具卡索引

> Day 4 新建了 27 张工具卡。这里是最值得长期记住的 7 张。

| # | 名字 | 一句话 |
|---|---|---|
| 31 | 协议决定并发模型 | 多路复用 vs 连接池由协议层决定 |
| 34 | 双时钟意识 | 业务用逻辑时钟,运维用物理时钟,两者都存 |
| 39 | 防腐层 | 不同抽象边界之间放翻译层 |
| 43 | 依赖方向规则 | 核心不依赖边缘,边缘适配核心 |
| 48 | 错误向上抛 | 底层加上下文 + return,决策留给上层 |
| 53 | 认知诚实 | 答案是什么就是什么,不过度包装 |
| 54 | 接口先行 | 设计的核心不是写代码,是定接口 |

---

*Day 4 完整收官 ✨*  
*"平静"那一刻,是设计透彻的奖励 — 以后会反复领。*
