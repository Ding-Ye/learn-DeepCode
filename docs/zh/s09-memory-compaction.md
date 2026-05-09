---
title: "s09 · 记忆压缩（清空式）"
chapter: 09
slug: s09-memory-compaction
est_read_min: 13
---

# s09 · 记忆压缩（清空式）

> 一个纯函数 `Compact(messages) → messages'`：往回扫到最后一次 `write_file`，从那里截断；前面拼上 system prompt 和重新生成的 initial plan；保留窗口里只留白名单工具的 `tool_use` / `tool_result` 对。**没有 I/O，没有 goroutine，没有副作用**——s10 的工作流循环里它就是一行 `messages = agent.Compact(messages)`。

---

## Problem

s06 的 runner 跑十轮工具就够了。但是 s10 的 workflow 是 **per-file iteration**——给一个十文件的 plan，runner 在每个文件上各跑五到十轮工具，一共 50-100 轮。每轮里 `read_file`、`search_code`、`execute_python`、`get_file_structure` 各回灌一次，`tool_result` 平均 1KB，半小时下来 transcript 已经 80KB+——再加上 LLM 自己的回复和三次重读 plan，总长度逼近 200K tokens 的 context window。

朴素的做法是"截前 K 条"——但 transcript 里 tool_use 和 tool_result **必须配对**，截一刀大概率把"我之前调过 read_file"切掉只剩"工具回了 'package a\nfunc Stub() {}' 给我"，模型就懵了。更糟糕的是 plan 在第 2 条消息里——一截就丢，模型不再知道自己在干什么。

上游 `workflows/agents/memory_agent_concise.py` 给的方案（2000 行版本）：每次 `write_file` 完成后**清空对话**，但保住三件事：

1. **system prompt**——index 0，永远在；
2. **initial plan**——重新合成一条 user 消息；
3. **当前轮的工具结果**——从最后一次 `write_file` 起往后保留，里面进一步只留白名单工具（`read_file` / `write_file` / `execute_python` / `execute_bash` / `search_code` / `search_reference_code` / `get_file_structure` / `read_code_mem`），其他统统扔。

**关键不变量**：丢一个 `tool_use` 必须同时丢配对的 `tool_result`；反之亦然。任何"孤儿 ToolUseID"都会让 provider 直接拒掉请求。

s09 把这套逻辑抽出来做成一个 ~150 行的纯函数。

## Solution

```ascii-anim frames=1
                input messages
                       │
                       ▼
                ┌──────────────────┐
                │ Compact(msgs)    │
                └────────┬─────────┘
                         │
   ┌─────────────────────┼─────────────────────┐
   │                     │                     │
   ▼                     ▼                     ▼
keep msgs[0]      synthesise plan       findLastWriteFileBoundary
if Role=="system"  as user message       (扫到末尾找 write_file，
                  with InitialPlan        return 配对 tool_use 的索引)
   │                     │                     │
   └──────► out ◄────────┴──────► out          │
                                               │
                                ┌──────────────┘
                                │
                                ▼
                  for i := boundary..end:
                      filterMessage(msgs[i])
                      // 丢 ToolName 不在白名单的
                      // tool_use / tool_result，
                      // 严格配对
                  append filtered to out
                                │
                                ▼
                            return out
```

四个关键设计决策：

1. **纯函数 vs. 上游的有状态 agent**——上游 `ConciseMemoryAgent` 有 `last_write_file_detected` / `should_clear_memory_next` / `current_round` 等一堆 flag；s10 的 workflow 调用前要先 `record_tool_result`，调用时还要传 `files_implemented`。我们直接 `Compact(messages) → messages'`：写不出 state-leak bug，测试零 setup。代价是 Compact 每次都要重扫 `write_file` 边界（O(n) 一次），收益是无锁、无状态、能被任何上层逻辑安全调用。
2. **重新合成 initial plan，不复用原来的**——上游也是新建一条 user 消息，不去找 messages 里那一条原始 plan turn。原因是 plan 在 transcript 里可能已经被压缩过、被 tool_use 块插过、被某个测试 fixture 替换过；干脆每次都拼一条干净的 `{Role:"user", Content:[{Type:"text", Text:InitialPlan}]}` 出来，**结果的契约就是"包含 plan"**，不依赖输入是否包含。
3. **`Tokenizer` 接口而不是写死 `len(s)/4`**——research-notes anti-pattern #9 点名上游"tiktoken 不可用就退回 len(s)"是隐式预算。Go 没有惯用的 tiktoken 端口，我们干脆把这件事公开：`Tokenizer` 接口 + 默认 `ByteLengthTokenizer{}`（返回 `len(s)/4`，注释里写明偏差）。读者要换真 BPE 时只需要换接口实现，agent 一行不用改。测试只断言**单调** + **±20% 边界**，不断言精确数字——因为没有精确数字可断言。
4. **session-isolation：不 import s06**——s06 已经声明了一份 `Message` / `ContentBlock`，但 s09 自己的 `types.go` 又重新声明一份。每章独立 `go.mod`、独立运行、独立测试是项目纪律。两份 shape 字段名一致，`IsError` / `ToolName` / `ToolUseID` 全部对得上——一个 s06 的 Message 拷过来不用改一行就跑。

## How It Works

### 一、`types.go`——重新声明的最小 shape

20 多行：`Message` + `ContentBlock`。`ContentBlock.Type` 决定哪些字段被读：

```go
type ContentBlock struct {
    Type      string // "text" | "tool_use" | "tool_result"
    Text      string
    ToolUseID string
    ToolName  string  // tool_result 也带这个，方便 Compact 一次扫描
    Input     string
    Output    string
    IsError   bool
}
```

`tool_result` 块上也带 `ToolName` 是 s09 相对 s06 的**唯一冗余**——s06 的 tool_result 是从配对的 tool_use 上推出名字的；s09 想在 Compact 里 O(1) 决定该不该丢，干脆把名字也存到 result 块上。Trade-off：略微的数据冗余 vs. 一次线性扫描搞定过滤。

### 二、`tokens.go`——Tokenizer 接口

```go
type Tokenizer interface {
    CountTokens(s string) int
}

type ByteLengthTokenizer struct{}

func (ByteLengthTokenizer) CountTokens(s string) int {
    return len(s) / 4
}

func MessagesTokens(t Tokenizer, msgs []Message) int {
    if t == nil { t = ByteLengthTokenizer{} }
    total := 0
    for _, m := range msgs {
        for _, b := range m.Content {
            total += t.CountTokens(b.Text)
            total += t.CountTokens(b.Input)
            total += t.CountTokens(b.Output)
        }
    }
    return total
}
```

`MessagesTokens` 故意不算 Role / Type 框架开销——我们关心的是**载荷大小**，因为那才是真正占 context 的部分。

### 三、`essential.go`——上游白名单的 verbatim 端口

```go
var EssentialTools = map[string]bool{
    "read_file":             true, // upstream L1588
    "write_file":            true, // upstream L1589 (also boundary marker)
    "execute_python":        true, // upstream L1590
    "execute_bash":          true, // upstream L1591
    "search_code":           true, // upstream L1592
    "search_reference_code": true, // upstream L1593
    "get_file_structure":    true, // upstream L1594
    "read_code_mem":         true, // upstream L1587
}
```

每行注释写出对应的上游行号——加减一个名字就是行为变更，要在 chapter docs 里说。

### 四、`agent.go`——主算法

```go
type MemoryAgent struct {
    InitialPlan      string
    EssentialTools   map[string]bool
    Tokenizer        Tokenizer
    MaxContextTokens int  // default 200000
    TokenBuffer      int  // default 10000
}

func (a *MemoryAgent) Compact(messages []Message) []Message {
    a.defaults()
    out := make([]Message, 0, len(messages)+2)

    // 1. 保 system prompt
    if len(messages) > 0 && messages[0].Role == "system" {
        out = append(out, messages[0])
    }

    // 2. 永远合成一条 plan
    out = append(out, Message{
        Role: "user",
        Content: []ContentBlock{{Type: "text", Text: a.InitialPlan}},
    })

    // 3. 找最后的 write_file 边界（返回最早一半 = tool_use 那一条）
    boundary := findLastWriteFileBoundary(messages)
    if boundary < 0 {
        return out
    }

    // 4. 边界往后逐条过滤
    dropped := map[string]bool{}
    for i := boundary; i < len(messages); i++ {
        filtered := a.filterMessage(messages[i], dropped)
        if len(filtered.Content) == 0 {
            continue
        }
        out = append(out, filtered)
    }
    return out
}
```

`findLastWriteFileBoundary` 是这章最容易写错的地方：从尾向前扫，**找到 `write_file` 块时不能直接返回那条消息**——因为 transcript 里 tool_use 在前、tool_result 在后，从末尾倒扫第一个命中的可能是 tool_result（assistant 已经写完了文件、user 已经回灌了 result）。如果只返回 result 那条，前面那条 tool_use 会被截掉，pairing 立刻破。修法是：命中 result 时再往前一条找匹配 ToolUseID 的 tool_use，返回更早的索引。

`filterMessage` 用一个 running 的 `dropped` map 跟踪"哪些 tool_use 被丢过"。后面遇到对应 ToolUseID 的 tool_result 也丢——配对永不孤立。

### 4 个非显然点

1. **boundary 是"配对里最早一半"的索引，不是最末一条 write_file 出现的位置**。倒扫遇到 result 必须再往前找 tool_use；否则 tool_use 在结果里被截掉，配对破，下一轮模型直接被 provider 退回。
2. **空 `Content` 的消息要丢掉**。过滤完所有块都被剔了的话，剩一个 `{Role:"user", Content:[]}` 大多数 provider 会 422。我们在 Compact 里用 `if len(filtered.Content) == 0 { continue }` 整条扔。
3. **`Compact` 不读 `ShouldCompact`**——后者是上层决定调不调 Compact 用的；进入 Compact 内部就一律压缩。这避免了"压缩内又判断要不要压缩"的死循环嫌疑。s10 的 workflow 调用顺序固定：`if agent.ShouldCompact(msgs) { msgs = agent.Compact(msgs) }`。
4. **没有 `tool_use` 的 result 也会被白名单过滤掉**。如果有人构造了一条独立的 `tool_result` 块（没配对的 tool_use），且其 ToolName 不在白名单里——我们也丢。这是一致性：**ToolName 不在白名单的工具结果**绝不进结果。

### 五、`main.go`——demo

读 `testdata/long_conversation.json`（49 条消息、3 次 write_file、4 次 web_fetch），跑一次 Compact，打印 before/after：

```
input  messages:  49  est-tokens:    368
output messages:  17  est-tokens:    138  (kept system + plan + last write_file round)
compaction ratio: 37.5%
```

## What Changed (vs. s08)

```diff
+ types.go        重新声明 Message + ContentBlock（s09 不 import s06；
+                 ContentBlock 比 s06 多了 ToolName 在 tool_result 上的冗余存储）
+ tokens.go       Tokenizer 接口 + ByteLengthTokenizer + MessagesTokens
+ essential.go    EssentialTools 白名单（8 个名字，逐条注释上游行号）
+ agent.go        MemoryAgent + Compact（纯函数）+ ShouldCompact + 配对扫描
+ main.go         读 fixture 跑一次 Compact 打印 before/after
+ agent_test.go   5 个测试（system+plan 保留 / boundary / 非白名单丢弃 /
+                 配对不变量 / tokenizer 单调+±20%）
+ testdata/long_conversation.json  49 条消息含 3 次 write_file + 4 次 web_fetch
+ 引入"纯函数对消息切片"的范式——s10 会复用这个签名跑文件循环
- 不再有"per-file 的有状态 agent"——上游的 round counter / write_file flag
  全部退化为一次线性扫描
```

s08 是**纯逻辑安全网**（loop / timeout / stall），s09 是**纯逻辑数据变换**（messages → messages'）。两章正交：s08 决定"还要不要继续"，s09 决定"接下来送什么进去"。s10 会把它们装在同一个 per-file body 里。

## Try It

```bash
cd agents/s09-memory-compaction

# Demo：读 49 条 fixture 跑一次 Compact，无网络无 API key
go run .

# 跑测试（5 PASS，<1s）
go test -count=1 -v ./...
```

测试 5 个全部 PASS：

| # | 测试 | 验证 |
|---|---|---|
| 1 | `TestCompact_50MessagesPreservesSystemAndPlan` | result[0].Role=="system"，result[1] 是合成 plan |
| 2 | `TestCompact_LastWriteFileBoundaryPreserved` | 最后一次 write_file 的 tool_use + tool_result 双双保留 |
| 3 | `TestCompact_NonEssentialToolsDropped` | `web_fetch` 配对完整删除 |
| 4 | `TestCompact_ToolPairingInvariant` | 结果里每个 tool_use 都有匹配的 tool_result，反之亦然 |
| 5 | `TestTokenizer_MonotonicAndBounded` | `len(a)<=len(b)` 蕴含 `count(a)<=count(b)`，且都在 `len(s)/4 ±20%` 内 |

## Upstream Source Reading

```upstream:workflows/agents/memory_agent_concise.py#L1567-L1605
def record_tool_result(self, tool_name, tool_input, tool_result):
    # Detect write_file calls to trigger memory clearing
    if tool_name == "write_file":
        self.last_write_file_detected = True
        self.should_clear_memory_next = True

    # Only record specific tools that provide essential information
    essential_tools = [
        "read_code_mem",          # Read code summary from implement_code_summary.md
        "read_file",              # Read file contents
        "write_file",             # Write file contents (important for tracking implementations)
        "execute_python",         # Execute Python code (for testing/validation)
        "execute_bash",           # Execute bash commands (for build/execution)
        "search_code",            # Search code patterns
        "search_reference_code",  # Search reference code (if available)
        "get_file_structure",     # Get file structure (for understanding project layout)
    ]

    if tool_name in essential_tools:
        tool_record = {
            "tool_name": tool_name,
            "tool_input": tool_input,
            "tool_result": tool_result,
            "timestamp": time.time(),
        }
        self.current_round_tool_results.append(tool_record)
```

```upstream:workflows/agents/memory_agent_concise.py#L1616-L1700
def create_concise_messages(self, system_prompt, messages, files_implemented):
    if not self.last_write_file_detected:
        return messages

    concise_messages = []

    # 1. Add initial plan message (always preserved)
    initial_plan_message = {
        "role": "user",
        "content": f"""**Task: Implement code based on the following reproduction plan**

**Code Reproduction Plan:**
{self.initial_plan}
...""",
    }
    concise_messages.append(initial_plan_message)

    # 2. Add knowledge base + current tool results (omitted in s09)
    ...
    return concise_messages
```

**阅读笔记**：

- **`last_write_file_detected` 在上游是个 boolean flag**，需要外部按顺序调 `record_tool_result` 维护；s09 把它换成"每次 Compact 都重新扫一遍 messages 找最后的 write_file 边界"。**计算 vs. 缓存**的取舍：上游为速度选了缓存，付出了 state-leak 的代价；我们为正确性选了重算，付出了 O(n) 的代价（但 n 永远是几十条）。
- **白名单是上游一个 local 变量**，不是类属性——意味着每次 `record_tool_result` 都会重建一份 list。Go 端我们提到模块级 `var EssentialTools`，省掉重建，且让"扩展白名单"成为一个公开的修改点。
- **上游的"知识库"消息**（最近一个文件的总结）我们没有移植——它需要再调一次 LLM 做总结，会让 Compact 不再是纯函数。s10 想要类似效果可以在 workflow 层面单独跑一个 summarisation 步骤，再把结果当作 user 消息插进 Compact 之前的 transcript。
- **token 预算的位置**——上游用 `summary_trigger_tokens = max_context_tokens - token_buffer` 决定何时触发 concise 模式；s09 同样用这个公式，但只在 `ShouldCompact` 里读，**不影响 Compact 内部逻辑**。意味着 Compact 总是按"完整压缩"做，调度问题留给上层。

**继续读**：从 `core/agent_runtime/runner.py:393-540` 看上游的 `_microcompact` 和 `_snip_history`——那是另一个层级的"压缩"（在 runner 里随每轮 LLM 调用做）。s09 的清空式压缩和 microcompact 是互补的：前者粗粒度（每文件一次），后者细粒度（每轮一次）。注解版：[`upstream-readings/s09-memory.py`](../../upstream-readings/s09-memory.py)。

---

**下一章**：s10 把 s06 的 Runner、s07 的 PlanningRuntime、s08 的 LoopDetector、s09 的 MemoryAgent 一起装进 `CodeImplementationWorkflow`——读一份 plan，逐文件生成代码，每次 `write_file` 后调一次 Compact。这是整本书的架构高潮。
