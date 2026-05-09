---
title: "s06 · 可调用工具的 Runner"
chapter: 06
slug: s06-tool-capable-runner
est_read_min: 16
---

# s06 · 可调用工具的 Runner

> 一个 `Provider`，一个 `Registry`，一个 `for` 循环。模型说话；runner 检测到 `tool_calls`；registry 派发；结果回灌；重复——直到模型给出最终文本，或 `MaxIterations` 用尽。这就是把 s01 / s02 / s04 三章拼起来的"心跳"。

---

## Problem

s01 是 **一次性** 的：一个 prompt → 一个 response → 打印。三十秒能写完，但它解决不了任何真实任务，因为 agent 的特性就是 **来回**——

- 模型说："我要调用 `read_file('/etc/hosts')`"
- 我们：执行工具，把输出塞回去
- 模型：拿到内容，说："好的，下一步调 `grep`"
- 又执行，又回灌……
- 终于：模型说"完成"，给一段最终文本。

这条循环就是 `core/agent_runtime/runner.py` 整整 1065 行做的事。但绝大部分代码不是循环本身，而是 **循环之外的硬话题**：

- 模型返回空文本怎么办？（`_MAX_EMPTY_RETRIES`）
- 模型 `finish_reason="length"` 被截断怎么办？（`_MAX_LENGTH_RECOVERIES`）
- 工具结果太大怎么办？（`max_tool_result_chars` + 截断）
- 工具调用 ID 配对错了怎么办？（`_drop_orphan_tool_results`）
- 工具自己抛异常怎么办？（包成 `is_error: true` 的 tool_result）
- 上下文超长怎么办？（`_microcompact` + `_snip_history`）

s06 把 **循环骨架** 单独抽出来做一章。骨架本身只有三个分支：tool_use → 派发 + continue；最终文本 → 退出；触顶 → 道歉模板。其他全部当作"未来章节的事"或"上游 stretch goal"——读者一次只读 ~150 行 Go，就能在脑里复现 agent 的核心节拍。

## Solution

```ascii-anim frames=1
        InitialMessages (拷贝一份)
                │
                ▼
        ┌────────────────────────────┐
        │  for i := 0..MaxIterations │
        └─────────────┬──────────────┘
                      │
                      ▼
        ┌────────────────────────────┐
        │ Provider.Chat(ctx, req)    │
        └─────────────┬──────────────┘
                      │
        ┌─────────────┴──────────────┐
        │                            │
   len(ToolCalls)>0?            else (final text)
        │                            │
        ▼                            ▼
  dispatchToolCall              append assistant
   (registry.Get + tool.Run     return RunResult{
    + truncate + is_error)         StopReason: StopDone
        │                          }
   append tool_use blocks
   append tool_result blocks
        │
        └──────► continue
                      │
        (loop exhausts)│
                      ▼
        return RunResult{
            StopReason:   StopMaxIterations
            FinalMessage: 道歉模板
        }
```

四个关键设计决策：

1. **三分支循环 vs. 上游的二十分支状态机**——上游的 `run()` 嵌套了 empty-retry / length-recovery / injection-cycle / orphan-repair / micro-compact / hook-callback 等 7+ 套并行控制流；每一套都正确，每一套都让初读者迷失。s06 砍到只剩 `if len(ToolCalls)>0`、`else (final text)`、`for ... else (max_iterations)` 三条路，读者第一次能看清骨架，再加层。
2. **`tool_result.IsError` 让循环不会因为工具失败而中止**——工具自己抛 error 是常事（路径不存在、HTTP 502、解析失败）；上游用 `tool_result` 块 + `is_error: true` 让模型自己看到出错原因然后想办法。s06 完整继承这个语义：`dispatchToolCall` 把 `tool.Run` 返回的 error 包成 `ContentBlock{Type:"tool_result", IsError:true, Output: errMsg}`，循环继续。**只有 `Provider.Chat` 失败才返回 `StopError`**——那是基础设施的问题，不是模型能修的。
3. **截断在 dispatcher 里做，不在 tool 里做**——上游 `_normalize_tool_result` 在 runner 这一层裁字符串。s06 同样：`tool.Run` 返回完整字符串；runner 看 `MaxToolBytes` 决定是不是切。意味着 **tool 的代码不需要知道任何上下文预算**，runner 是唯一管"塞得下 / 塞不下"的地方。
4. **session-isolation：s06 不 import s02 / s04**——按项目纪律，每章独立 `go.mod`，自己重新声明 `Provider` / `Tool` / `Message` / `ContentBlock` 等最小子集。这些 shape 和 s02 / s04 字节兼容（一个 s02 的 `Tool` 拷过来不用改一行就能跑），但每章 grep-friendly、能独立测试、读者跳着读不会撞包路径。

## How It Works

### 一、`types.go`——重新声明的最小契约

s06 不 import 任何兄弟 session。`Provider` / `Tool` / `Message` / `ContentBlock` / `ToolCallRequest` / `ChatRequest` / `ChatResponse` / `Usage` 全部就在本目录下。形状和 s04 完全一致：

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

type Tool interface {
    Name() string
    Schema() ToolSchema
    Run(ctx context.Context, args json.RawMessage) (string, error)
}

type ContentBlock struct {
    Type      string // "text" | "tool_use" | "tool_result"
    Text      string
    ToolUseID string
    ToolName  string
    Input     json.RawMessage
    Output    string
    IsError   bool   // 仅 tool_result 用
}
```

`IsError` 字段是 s06 相对 s04 的**唯一新增**——s04 那一章不需要在 ContentBlock 里区分"成功的 tool_result"和"失败的 tool_result"，因为那一章不调工具。s06 一调起来，立刻需要这个开关。

### 二、`registry.go`——精简版

s02 的 Registry 有 `sync.Mutex`、`Close()` 生命周期、builtins-vs-mcp 排序——那些是 registry 自己的功夫。s06 只需要 `Get(name)` 派发用、`List()` 搜集 schemas 喂给模型用：

```go
type Registry struct {
    tools map[string]Tool
}

func (r *Registry) Register(t Tool) { r.tools[t.Name()] = t }
func (r *Registry) Get(name string) (Tool, bool) { t, ok := r.tools[name]; return t, ok }
func (r *Registry) List() []ToolSchema { /* sort by name */ }
```

40 行整。重新发明 registry 是为了 **保持章节聚焦**——s06 的卖点是循环，不是注册表的并发安全。读者要看完整版，去翻 s02。

### 三、`spec.go`——RunSpec / RunResult / StopReason

```go
const (
    StopDone          = "done"           // 模型给了最终文本
    StopMaxIterations = "max_iterations" // 触顶，发道歉模板
    StopError         = "error"          // Provider.Chat 失败，工具失败不算
)

type RunSpec struct {
    InitialMessages []Message
    Tools           *Registry  // nil 表示无工具
    Model           string
    MaxIterations   int
    MaxToolBytes    int        // ≤0 表示不截
    MaxTokens       int
    Temperature     float64
}

type RunResult struct {
    FinalMessage Message     // 模型最后一轮的 assistant 消息
    AllMessages  []Message   // 完整 transcript
    StopReason   string      // Stop* 三选一
    Iterations   int         // 实际跑了几轮
}
```

`StopReason` 是 s10 那一章会扩展的 **控制流枢纽**——s10 的 workflow 还会加 `loop_detected`（来自 s08）、`max_time` 等，但都从 `StopDone` / `StopMaxIterations` / `StopError` 这三个基本盘扩展。

### 四、`dispatch.go`——单次工具调用的所有规则

```go
const truncationMarker = "… [truncated]"

func dispatchToolCall(ctx context.Context, reg *Registry, call ToolCallRequest, maxBytes int) ContentBlock {
    if reg == nil { /* IsError=true, "no registry configured" */ }

    tool, ok := reg.Get(call.Name)
    if !ok { /* IsError=true, "tool X not found" */ }

    out, err := tool.Run(ctx, call.Args)
    if err != nil { /* IsError=true, "tool X failed: <err>" */ }

    return ContentBlock{
        Type:      "tool_result",
        ToolUseID: call.ID,
        Output:    truncate(out, maxBytes),
    }
}

func truncate(s string, maxBytes int) string {
    if maxBytes <= 0 || len(s) <= maxBytes { return s }
    keep := maxBytes - len(truncationMarker)
    return s[:keep] + truncationMarker
}
```

为什么把这段单独成一个文件？因为 **工具失败的处理策略** 是 s06 容易被读者轻视的地方——三种失败（registry nil、tool 不存在、tool.Run 报错）共享同一个模式：包成 `IsError=true` 的 tool_result，**循环继续**。把它放在 runner.go 里读者会和主循环逻辑混淆，独立成 dispatch.go 后只剩 70 行，一目了然。

### 五、`runner.go`——主循环

抠掉文档注释只有 ~70 行：

```go
func (r *Runner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
    messages := append([]Message(nil), spec.InitialMessages...)  // 拷贝
    schemas := []ToolSchema(nil)
    if spec.Tools != nil { schemas = spec.Tools.List() }

    for i := 0; i < spec.MaxIterations; i++ {
        resp, err := r.Provider.Chat(ctx, ChatRequest{...})
        if err != nil {
            return RunResult{StopReason: StopError, ...}, err
        }

        // 分支 1：模型要调工具
        if len(resp.ToolCalls) > 0 {
            assistant := Message{Role: "assistant", Content: resp.Content}
            assistant.Content = ensureToolUseBlocks(assistant.Content, resp.ToolCalls)
            messages = append(messages, assistant)

            results := make([]ContentBlock, 0, len(resp.ToolCalls))
            for _, call := range resp.ToolCalls {
                results = append(results, dispatchToolCall(ctx, spec.Tools, call, spec.MaxToolBytes))
            }
            messages = append(messages, Message{Role: "user", Content: results})
            continue
        }

        // 分支 2：最终文本
        final := Message{Role: "assistant", Content: resp.Content}
        messages = append(messages, final)
        return RunResult{FinalMessage: final, StopReason: StopDone, ...}, nil
    }

    // 分支 3：触顶
    apology := Message{Role: "assistant", Content: []ContentBlock{{
        Type: "text",
        Text: fmt.Sprintf(defaultMaxIterationsMessage, spec.MaxIterations),
    }}}
    return RunResult{FinalMessage: apology, StopReason: StopMaxIterations, ...}, nil
}
```

`ensureToolUseBlocks` 是一个小修补：Anthropic 的 response 已经把 tool_use 平铺在 `Content` 里；OpenAI 的 response 把它们挂在 `ToolCalls` 字段上但 **不在** `Content` 里。runner 想要的 transcript 必须两份合并——读者看到 assistant 消息时能直接看到 tool_use 块，否则下一步派发就和上一步看不到的"决定"对不上。

### 六、`replay.go`——测试用的假 Provider

```go
type ReplayProvider struct {
    Responses []ChatResponse
    calls     int
}

func (p *ReplayProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
    if p.calls >= len(p.Responses) {
        return ChatResponse{}, errors.New("ReplayProvider: queue empty")
    }
    r := p.Responses[p.calls]
    p.calls++
    return r, nil
}
```

30 行解决"我想测一个多轮对话但不想用 httptest"。每个测试就把 N 个 ChatResponse 排好队，Runner 调一次，pop 一个，确定性极高。s10 那一章会复用同样的模式跑三文件 workflow 集成测试。

### 4 个非显然点

1. **工具失败 ≠ runner 失败**。这是上游和我们的核心契约：tool.Run 报错被包进 tool_result+IsError=true，循环继续，模型自己看到"我的 read_file 拿到了 ENOENT"然后重新尝试。**只有 Provider.Chat 失败才返回 `StopError`**——网络/认证/模型坏了不能让模型自救。读者经常以为"任何 error 都该 propagate"，s06 是反例。
2. **截断的字节数不是"不超过"，是"恰好等于"**。`truncate(s, 200)` 返回的字符串 **永远** 长度 200（marker 占 13 字节，payload 占 187 字节）——这样上下文预算可以精确累加。如果只是"不超过"，每次都浪费一点，10 轮工具下来差几百字节。测试 `TestRun_Truncation` 直接断言 `len(found.Output) == 200`，不是 `<=`。
3. **`MaxIterations=N` 实际能跑 N 轮**——上游用 `for iteration in range(spec.max_iterations)` 是 0..N-1，N 轮过完进入 `for/else` 分支发道歉。Go 的 `for i := 0; i < N; i++` 行为完全相同；测试 `TestRun_MaxIterations` 设 `MaxIterations=1` 时 provider 只被调用 **一次**，循环结束直接发道歉。读者经常误以为"还能再多跑一轮收尾"，没有——道歉模板就是收尾本身。
4. **assistant 消息的 `Content` 必须包含 tool_use 块，否则 transcript 错位**。Anthropic 自己回的 Content 里就有；OpenAI 不一定有。`ensureToolUseBlocks` 是个 8 行的 idempotent 补丁——已经有就不重复加，没有就照 ToolCalls 补。读者跑 OpenAI 后端时如果跳过这步，下一轮模型会看到"上一轮我说要调 echo 但 transcript 里没记这件事"，然后大概率重复调一次。

## What Changed (vs. s05)

```diff
+ types.go        重新声明 Provider / Tool / Message / ContentBlock / ToolCallRequest /
+                 ChatRequest / ChatResponse / Usage（含 IsError 字段——s04 没有）
+ registry.go     精简版 Registry（仅 Register/Get/List，无 Close）
+ spec.go         RunSpec + RunResult + StopReason 三常量
+ dispatch.go     dispatchToolCall + truncate（IsError 包装、… [truncated] 标记）
+ runner.go       Runner.Run（三分支主循环 + ensureToolUseBlocks 补丁）
+ replay.go       ReplayProvider（队列驱动的假 Provider，给测试和 demo 用）
+ main.go         3 轮 replay demo（请求 echo → 结果 → 请求第二次 echo → 结果 → 最终文本）
+ runner_test.go  5 个测试（one-round / two-round / max_iterations / truncation / tool_error）
+ 引入 StopReason 控制流枢纽——s10 会再扩 loop_detected / max_time 两个值
- 不再有"裸 Provider 调用"——s01 的单次 Chat 退役为 runner 内部一次迭代
```

s05 是 **不可变 task 状态**；s06 是 **可变会话状态 + 控制流**。两章正交，一个管"我是谁、在哪儿"，一个管"接下来调谁"。s10 会同时引用两者。

## Try It

```bash
cd agents/s06-tool-capable-runner

# Demo：3 轮 replay，无网络无 API key
go run .

# 跑测试（5 PASS，<1s）
go test -count=1 -v ./...
```

测试 5 个全部 PASS：

| # | 测试 | 验证 |
|---|---|---|
| 1 | `TestRun_OneRound` | provider 直接返回文本 → `StopDone`，未派发工具 |
| 2 | `TestRun_TwoRound` | tool_use → 文本；`tool.Run` 调用 1 次；最终文本匹配 |
| 3 | `TestRun_MaxIterations` | `MaxIterations=1` + 模型一直要调工具 → `StopMaxIterations` + 道歉模板含 `(1)` |
| 4 | `TestRun_Truncation` | 5000-byte 输出 + `MaxToolBytes=200` → 含 marker，长度 == 200 |
| 5 | `TestRun_ToolError` | `tool.Run` 报错 → `tool_result.IsError=true`，循环继续到下一轮 |

## Upstream Source Reading

```upstream:core/agent_runtime/runner.py#L69-L110
@dataclass(slots=True)
class AgentRunSpec:
    """Configuration for a single agent execution."""

    initial_messages: list[dict[str, Any]]
    tools: ToolRegistry
    model: str
    max_iterations: int
    max_tool_result_chars: int
    temperature: float | None = None
    max_tokens: int | None = None
    reasoning_effort: str | None = None
    hook: AgentHook | None = None
    error_message: str | None = _DEFAULT_ERROR_MESSAGE
    max_iterations_message: str | None = None
    concurrent_tools: bool = False
    fail_on_tool_error: bool = False
    workspace: Path | None = None
    session_key: str | None = None
    ...
```

```upstream:core/agent_runtime/runner.py#L239-L320
async def run(self, spec: AgentRunSpec) -> AgentRunResult:
    hook = spec.hook or AgentHook()
    messages = list(spec.initial_messages)
    final_content: str | None = None
    ...

    for iteration in range(spec.max_iterations):
        # context governance: orphan repair, microcompact, snip ...
        response = await self._request_model(spec, messages_for_model, hook, context)

        if response.should_execute_tools:
            assistant_message = build_assistant_message(...)
            messages.append(assistant_message)
            results, new_events, fatal_error = await self._execute_tools(
                spec, response.tool_calls, external_lookup_counts,
            )
            for tool_call, result in zip(response.tool_calls, results):
                tool_message = {
                    "role": "tool",
                    "tool_call_id": tool_call.id,
                    "name": tool_call.name,
                    "content": self._normalize_tool_result(...),
                }
                messages.append(tool_message)
            if fatal_error is not None:
                ...
            continue
        # final-text branch ...
```

```upstream:core/agent_runtime/runner.py#L548-L575
        else:
            stop_reason = "max_iterations"
            template = spec.max_iterations_message or _DEFAULT_MAX_ITERATIONS_MESSAGE
            final_content = template.format(max_iterations=spec.max_iterations)
            self._append_final_message(messages, final_content)
            ...

        return AgentRunResult(
            final_content=final_content,
            messages=messages,
            tools_used=tools_used,
            usage=usage,
            stop_reason=stop_reason,
            error=error,
            tool_events=tool_events,
            had_injections=had_injections,
        )
```

**阅读笔记**：

- **`AgentRunSpec` 上有 ~20 个字段，s06 的 `RunSpec` 只有 7 个**——剔掉的是 hook / injection_callback / progress_callback / checkpoint_callback / retry_wait_callback / session_key / workspace 这一整套 orchestration 钩子。它们不是循环本身的责任，是 s10 的 workflow 责任。把它们留在 runner 里会让"循环骨架"看起来像一颗洋葱，剥不到核心。
- **`should_execute_tools` 是 `LLMResponse` 上的属性**，包含 `finish_reason == "tool_calls"` 且 `len(tool_calls) > 0` 双条件。Go 里我们简化成 `len(resp.ToolCalls) > 0`；如果某个 provider 在 finish_reason 不是 tool_calls 但又给了 tool_calls，s06 仍然会执行——比上游更宽松。在我们的 fixture 里这种情况不会出现，但生产环境可以加一个 `&& resp.FinishReason == FinishToolCalls` 保险。
- **`_normalize_tool_result` 内部还做了"如果工具返回空字符串就替换成 'OK'"** 这种小修补；我们没有移植——空字符串可以 propagate，模型自己看到"我调了，啥也没回来"通常会自己处理。这是上游的 anti-pattern #5（"用魔法值代替显式 None"）。
- **`for/else` 是 Python 的语法**——for 跑完没 break 就进 else。Go 没有这个糖，要在 for 循环退出后 fall through 到道歉那段；逻辑等价但读者容易在初读时漏掉那一支。s06 在 README 里专门列出来。

**继续读**：从 `core/agent_runtime/runner.py:393-540` 开始读完空文本重试（empty_content_retries）和 length-recovery 两段——会看到 s06 故意没移植的那部分有多复杂。注解版：[`upstream-readings/s06-runner.py`](../../upstream-readings/s06-runner.py)。

---

**下一章**：s07 转回"持久化"——一个 plan 跑完之后写三份 on-disk 工件（atomic JSON checkpoint、JSONL attempts、result meta）。s06 的 Runner 在 s10 那一章会和 s07 的 PlanningRuntime + s08 的 LoopDetector + s09 的 MemoryAgent 一起被装进 CodeImplementationWorkflow。
