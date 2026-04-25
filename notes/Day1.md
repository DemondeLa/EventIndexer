# 阶段 2 Day 1 学习笔记

> **主题**：穿透 abigen 屏蔽层 — Custom Error 精确解码
> **仓库**：`EventIndexer`（新建）
> **Commit**：`91996eb` Stage 2 Day 1: implement custom error decoder

---

## 一、阶段 2 的命题转换

| | 阶段 1 | 阶段 2 |
|---|---|---|
| **核心命题** | 会不会 | 稳不稳 |
| **关注点** | 能否连链/部署/查事件 | 出错能否看清/断连能否恢复/数据不丢 |
| **典型工作姿态** | 一次性脚本 | 长期运行服务 |

---

## 二、核心心智：「连接链」vs「监听链」

阶段 2 整条主线的根问题。**两者差异不在 API**，在工程结构的三个层面：

| 维度 | 连接链（程序 A） | 监听链（程序 B） |
|---|---|---|
| **生命周期** | 代码决定结束 | 外部信号决定结束 |
| **状态** | 内存即可，跑完就扔 | 必须持久化，崩了要能接着跑 |
| **失败** | 退出 → 重跑 | 自愈 → 继续 |

一句话："**连接链是一次性事务，监听链是长期服务**"。

### 这三个差异如何推出阶段 2 的节奏

```
生命周期（B 要能"好好结束"）
  ├─ Day 2：WatchXxx
  ├─ Day 3：goroutine + channel + context 取消
  └─ Day 6：graceful shutdown

状态（B 要"记得自己干到哪了"）
  └─ Day 4：SQLite 持久化

失败（B 要"自己扛过去"）
  ├─ Day 5：连 Sepolia
  └─ Day 6：结构化日志 + 重试 + 降级
```

Day 1（今天）不在这张图里——是个**前置突破**：在进入"长期运行"之前，先把 `execution reverted` 这堵墙凿穿，让后续每一天的 debug 都有"眼睛"。

---

## 三、Custom Error 的 5 节点数据路径

```
节点 1：Solidity 合约执行 revert InvalidProjectId(999);
    ↓
节点 2：EVM 构造 revert data = selector (4 bytes) + abi.encode(args)
    ↓
节点 3：节点把 revert data 打进 JSON-RPC 响应
    response = { error: { code, message: "execution reverted", data: "0x..." } }
    ↓
节点 4：go-ethereum rpc.Client 把响应包装成实现 ErrorData() 的错误对象
    ↓
节点 5：abigen 生成的合约调用返回 err
    err.Error() → 仅给 message 部分（"execution reverted"）
    ErrorData() → 真正的 revert data，需要主动取出来
```

**今天 Demo 2 的实测验证**：

```
原始 hex (return data):
  0xc88f99b400000000000000000000000000000000000000000000000000000000000003e7
  ↑↑↑↑↑↑↑↑↑                                                                  
  4 字节 selector              32 字节 = uint256(999)，999 = 0x3e7
```

每个字节的位置都和上面 5 节点路径完全对应——这是阶段 2 第一次"亲眼看到"的链路完整跑通。

---

## 四、selector 的数学

```
合约：error InsufficientFee(uint256 sent, uint256 required);

签名字符串（不含参数名，只有类型）：
  "InsufficientFee(uint256,uint256)"

selector = keccak256("InsufficientFee(uint256,uint256)")[:4]
         = 4 字节
```

### 为什么是「前 4 字节」、为什么 4 字节够用

- 2^32 ≈ 42.9 亿个空间
- 单个合约通常只有几十个 error/function
- 单合约内碰撞概率极低
- **跨合约碰撞是可能的**——这是 Day 8 那个 `InvalidDeadline` 共享 selector 问题的同源现象

---

## 五、Decoder 的设计决策（6 题立场）

| # | 问题 | 决策 | 原则 |
|---|---|---|---|
| 1 | struct 还是函数 | struct | 持有不可变状态 |
| 2 | ABI 何时注入 | 构造时 | 昂贵 + 不变 → 算一次存起来 |
| 3 | 返回签名 | `Decode(err) string` | 输出端要稳定 API，不要 `(string, error)` |
| 4 | `Decode(nil)` | 返回 `""` | nil 进 / 零值出，让"成功"沉默 |
| 5 | 非 revert 错误 | 透传 `err.Error()` | 不拒绝输入，自己降级 |
| 6 | 未知 selector | `"unknown error (selector=0x...): ..."` | 降级 + 信息保留 |

### 关键元原则

- **降级 > 报错**：工具函数遇到不认识的输入，输出降级版本比返回 error 更适合"被到处调用"的场景
- **nil 进 / 零值出**：Go 惯例，让"成功"保持沉默
- **构造函数 vs 方法**：把"昂贵 + 不变"算一次存起来，方法只做"廉价 + 每次不同"的工作

---

## 六、Decoder 的实现要点

### 6.1 dataError 接口（局部接口模式）

```go
type dataError interface {
    ErrorData() interface{}
}
```

- **小写**——包内私有
- **匿名 / 局部接口**——在使用处定义，不做全局抽象
- **依赖接口而不是具体类型**——HTTP / WebSocket / SimBackend 各自的错误类型不同，但都实现这个方法

### 6.2 双重 type assertion

```go
de, ok := err.(dataError)              // 第 1 次：err 实现了 ErrorData？
data, ok := de.ErrorData().(string)    // 第 2 次：返回的 interface{} 装的是 string？
```

ErrorData 的返回类型是 `interface{}`，不是 string——必须再做一次 assertion 拆箱。

### 6.3 版本兼容：string vs map

不同版本/后端的 `ErrorData()` 返回形态不同：

```go
raw := de.ErrorData()

// case 1：直接返回 string
if s, ok := raw.(string); ok { return s, true }

// case 2：返回 map[string]interface{}，data 字段是 hex
if m, ok := raw.(map[string]interface{}); ok {
    if s, ok := m["data"].(string); ok { return s, true }
}
```

**今天踩到的真坑**——概念 4 教练给的代码示例只覆盖 case 1，但当前 go-ethereum 在 Hardhat 后端下返回 case 2。这是「依赖接口而不是具体类型」**没有完全解决**的问题——接口的返回类型语义仍然可能变化。

### 6.4 hex string → bytes 的显式转换

```go
msg, ok := extractRevertData(err)             // msg 是 "0xabcd..." 字符串
dataBytes, err := hexutil.Decode(msg)         // 必须显式转 []byte
var selector [4]byte
copy(selector[:], dataBytes[:4])              // [4]byte 数组，不是 []byte 切片
```

ErrorByID 要求 `[4]byte` 数组类型。

### 6.5 参数解码

```go
errorDef, _ := d.parsedABI.ErrorByID(selector)
args, _ := errorDef.Unpack(dataBytes)         // 注意：传完整 data，库内部跳过 selector
return fmt.Sprintf("custom error %s%v", errorDef.Name, args)
```

`%v` 对空切片输出 `[]`，对有参数切片输出 `[999]`——格式自动统一。

---

## 七、今天踩的关键坑

### 坑 1：string 和 bytes 的类型语义混淆 🌟

**这是今天最值的一条心智重构**。

```go
msg := "0xabcd1234"
len(msg) == 10        // 10 个 ASCII 字符
                      // ❌ 不是 4
```

string 和 []byte 在内存里可能装相似的东西，但**类型决定意图**：
- `string` → 字符序列，按字符理解
- `[]byte` → 字节序列，按二进制理解

`hexutil.Decode` 不只是格式转换——是**意图翻译**：「我之前的理解是字面值，现在我要按它表达的二进制理解」。

**显式转换是 Go 让你做的"意图标注"**。

### 坑 2：err 变量遮蔽

阶段 1 的思维定式：err 用一次就丢，可以随便覆盖。

阶段 2 不行——Decode 函数里 err 是**贯穿全函数的上下文信息**：

```go
errorDef, lookupErr := d.parsedABI.ErrorByID(selector)  // ✅ 不要遮蔽 err
if lookupErr != nil {
    return fmt.Sprintf("..., %s", err.Error())          // 这里要的是入参 err
}
```

**两种 err 的生命周期**：
- 操作结果型：用一次就丢，可以遮蔽
- 上下文型：贯穿整个函数，必须保护

### 坑 3：跨语言习惯污染

| | C++ 习惯 | Go 现实 |
|---|---|---|
| 函数空实现作 TODO | `int foo() {}` 编译警告 | 直接编译错误 |
| main 文件位置 | 项目根目录的 main.cpp 是惯例 | 根目录禁放 main，全在 `cmd/` |

原则：**当你"顺手"做一件事的瞬间，停一下问自己：这是 Go 的惯例还是别的语言的？答不上就先不做。**

### 坑 4：环境异常吞输出

GoLand 内置终端吞了 `grep` 输出——以为命令没生效，其实是终端问题。

**纪律**：阶段 2 命令行操作默认用独立终端，IDE 内置终端只跑短命令。

### 坑 5：Hardhat 时间控制的"绝对 vs 相对"

```go
// ❌ 错：传绝对时间戳，链时间会被推进 17 亿秒
client.Client().CallContext(ctx, nil, "evm_increaseTime", now+4000)

// ✅ 对：传相对增量
client.Client().CallContext(ctx, nil, "evm_increaseTime", 4000)
```

Day 7 立过的经验，70 多天没碰就忘了——说明经验需要写下来才不会丢。

---

## 八、阶段 2 装上的工作流纪律

### 1. Single Step Discipline

教练让你做什么就做什么，不要"反正都要写，先占个位"。空壳代码引入额外的失败点，干扰你正在验证的事。

> **每次编译只该有 1 件事可能出错。**

### 2. Full Disclosure 报告纪律

阶段 1 的简略报告（"已修复"）在阶段 2 不行。完整报告格式：

```
原本指令：xxx
我做了什么超出指令的事：xxx
为什么会这么做：xxx
具体诱因：xxx
我的修复方式：xxx
当前状态：（贴代码）
```

### 3. 先预测后观察

任何命令执行前应有心理预期。没预期的实测是浪费实验机会。

### 4. 单元测试纪律

阶段 1 没写过测试，阶段 2 默认要写。每发现一种新场景，加一个测试覆盖（今天的 mockRevertErrMap 就是这种逻辑——map 形态发现后立刻补测试）。

### 5. mock 类型 + 编译期接口检查

```go
type mockRevertErr struct { ... }
func (e *mockRevertErr) Error() string         { ... }
func (e *mockRevertErr) ErrorData() interface{} { ... }

var _ dataError = (*mockRevertErr)(nil)   // 编译期检查接口实现
var _ error    = (*mockRevertErr)(nil)
```

接口实现错误在编译时暴露，不用等到测试运行。

### 6. 临时诊断代码不污染产品代码

debug 时加 `fmt.Printf` 等诊断代码——加在**调用方**（main.go），不加在**被调用方**（decoder.go）。问题搞清楚就整段删掉。

---

## 九、调用 Day 8 工具的实例

阶段 1 立的工具今天的应用：

| 工具 | 今天的应用场景 |
|---|---|
| **代码三层架构**（人写 / 机器生成 / 上游库） | Step 1 决定哪些文件迁移、哪些重写、哪些不带 |
| **抽象层命名原则**（反映能力，不反映载体） | Step 1 选 `internal/abiutil/` 而非 `internal/indexer/` |
| **后端分层**（链本体 ≠ 客户端 handle ≠ 链上状态） | "Decoder 持有的是 Go 进程里的状态，不是合约状态" |
| **重构 vs 增强** | account.go 迁移到 `internal/account/` 改 package 是重构 |
| **不共享就不用锁** | parsedABI 不可变 → 天然线程安全 |

---

## 十、Go 语言知识点

### 接口实现

- 严格签名匹配（返回类型不一致就不算实现）
- 不需要 `implements` 关键字（结构体只要有匹配的方法就自动实现）
- 接口在使用处定义（局部接口惯用法）

### const vs var

- `const` 只能修饰编译期可计算的字面量
- 结构体、指针、map 必须用 `var`
- `var X = ...` + 全大写 + init 时赋值 + 运行时不改 = effectively immutable variable

### comma-ok 模式

```go
v, ok := m[key]            // map 查找
v, ok := <-ch              // channel 接收
v, ok := i.(string)        // type assertion
```

bool 表"操作是否成立"，不要把 bool 用作内容语义。

### `interface{}` 装箱拆箱

```go
func foo() interface{} { return "hello" }   // string 自动装箱
v, ok := foo().(string)                     // 拆箱
```

### 参数名是「软 API」

- 语言层：参数名不是 API（换名字编译过）
- 文化层：参数名是 API（`go doc` 显示、IDE 提示、社区可读性）

Go 是「语言松、文化紧」的生态。

### `./...` 递归通配符

```bash
go build ./...      # 当前目录所有子包
go test ./...
```

阶段 2 默认用 `./...`。

---

## 十一、今天的代码资产

### 仓库结构

```
EventIndexer/
├── contracts/WinnerTakesAll.sol         （从阶段 1 带来）
├── abigen/winner/WinnerTakesAll.go      （make winner 生成，git 忽略）
├── internal/abiutil/
│   ├── decoder.go                       （核心产物：3 段实现）
│   └── decoder_test.go                  （5 个测试用例）
├── cmd/seed/main.go                     （部署 + 2 个 Demo）
├── Makefile                             （make winner / make clean）
├── .gitignore
├── go.mod / go.sum
└── README.md
```

### Decoder 的完整行为表

```
Decode(err) 走 5 个分支：

err == nil                       → ""
extractRevertData 失败           → err.Error()                透传
hexutil.Decode 失败              → "invalid hex...: <err>"   降级保留
len(dataBytes) < 4               → "invalid revert data..."  降级保留
ErrorByID 找不到                  → "unknown error (selector=0x...): <err>"
ErrorByID 找到 + Unpack 成功     → "custom error <Name>[<args>]"
ErrorByID 找到 + Unpack 失败     → "custom error <Name> (unpack failed)"
```

### 测试覆盖

```
TestDecode_Nil                    Decode(nil) → ""
TestDecode_PlainError             普通 error → 透传
TestDecode_KnownSelector          已知 selector（string 形态）
TestDecode_UnknownSelector        未知 selector
TestDecode_KnownSelector_MapForm  已知 selector（map 形态）
```

### 真链演示

```
Demo 1: VotePhaseNotActive
  原始 err:    Error: VM Exception... (return data: 0x795a5abd)
  解码后 err:  custom error VotePhaseNotActive[]

Demo 2: InvalidProjectId(999)（先 evm_increaseTime 进入 vote 阶段）
  原始 err:    Error: VM Exception... (return data: 0xc88f99b4...3e7)
  解码后 err:  custom error InvalidProjectId[999]
```

---

## 十二、复盘四问的答案

### Q1：今天最值的一条知识

> **string 是 bytes 的视图，类型决定意图——不是格式转换，是"意图翻译"。**

阶段 2 后续会反复撞：Day 2 的 topics、Day 4 的 SQLite 存储、Day 5 的真链 hash——三种东西看起来都是 string 但语义不同。

### Q2：今天最大的坑

> **err 变量遮蔽——阶段 1 把 err 当一次性变量的思维定式，在 Decoder 这种"err 是上下文"的函数里破产。**

修复方向不是改参数名，是反转默认习惯：默认起新名字，"嫌麻烦想用 err"时停下问"上下文 err 还需要吗"。

### Q3：心智模型的变化

| | 之前 | 之后 |
|---|---|---|
| revert 是什么 | 不可读的黑盒 | 一段密文 + 我有解码工具 |
| 我的位置 | 链给我什么我就显示什么 | **我有主动权——决定是否要解、解成什么样** |

### Q4：反驳教练的次数

**3 次**：
1. **技术决策反驳**：仓库迁移姿势（"阶段 2 仍是学习不是工程"）
2. **关系反馈反驳**：教学风格结构（"选择题和任务中间不要塞背景"）
3. **教学契约反驳**：能力预设（"我还在学 go-ethereum，不知道有什么轮子"）

3 次反驳的性质各不相同——这是 Day 8 留下"反驳教练至少一次"这条交接时**真正想要看到的**。

---

## 十三、Day 1 → Day 2 的钩子

### 已装上的工具（Day 2 可以直接用）

- ✅ Decoder 框架——任何合约的 revert 都能解
- ✅ mockXxxErr 模式——后续测试可以复用
- ✅ 单元测试纪律——Day 2 默认要写
- ✅ inline 时间控制写法——debug 时复用

### Day 1 触及但没解决的事（推到后续）

| 事项 | 推到 |
|---|---|
| `InvalidDeadline()` 两处共享 selector 验证 | Day 2 之前的小复盘 |
| `account.go / devchain.go` 的真正重写 | Day 2 真用到时 |
| 参数解码的边界 case 测试（有参数 mock） | 后续工程纪律 |
| README 的完整版 | v1.0 时 |

### Day 2 主题预告

**WatchXxx 事件订阅** — 「监听链」的第一次正式实操。

会立刻撞到的事：
- 「连接 vs 监听」差异里**生命周期**层（今天答到了）
- 「连接 vs 监听」差异里**状态**层（Day 4 SQLite）—— 还没建立
- 「连接 vs 监听」差异里**失败**层（Day 5-6）—— 还没建立

---

## 十四、阶段 2 的"长期运行"意识预告

今天的 Decoder 是**纯函数式**——和"长期运行"没直接关系。

但从 Day 2 起，所有代码要问自己：

- 这段代码会不会在 7 天不重启的 daemon 里跑？
- 如果连续处理 100 万个事件，有没有累积泄漏？
- 如果网络抖动 3 秒，会发生什么？
- 如果进程被 Ctrl+C，数据会不会丢？

**今天先在心里装上这个意识，Day 2 开始实操。**

---

*笔记完成于 2026-04-25，commit 91996eb。*
