---
title: "s10 · 文件级代码实现工作流"
chapter: 10
slug: s10-code-impl-workflow
est_read_min: 22
---

# s10 · 文件级代码实现工作流

> 整本书的架构高潮。一行 `Workflow.Run(ctx, planPath, taskDir) (RunReport, error)` 就把 s06 的 Runner、s08 的 LoopDetector、s09 的 MemoryAgent 和 s07 的 AtomicWriteJSON / AppendJSONL 写盘原语全部组合起来，把 YAML plan 编译成一个目录的代码文件。~800 LOC、11 个源文件——这是全书最长的一章，因为四套机制要在一个 body 里融合，而 session-isolation 规则禁止跨章 import。

---

## Problem

之前每章都只解决一个机制。s06 有 runner，没有安全网；s07 有 checkpoint，没有 agent；s08 有 loop detector，没有循环可以监控；s09 有记忆压缩，没有 run 可以压缩。**它们没有一个能独立跑成"工作的 agent 系统"**——读者读到 s09 末尾应该有权问："这堆东西真的能拼起来吗？"

s10 就是答案。它是唯一一章会同时调用多个机制的测试用例；它也是唯一一章，CLI 跑起来就能看到一个真正（用 replay 驱动）的端到端 agent 循环把生成代码写到磁盘。它也是最长的一章，因为 session-isolation 规则要求 s10 只能**重新声明**别人的最小子集，不能 import。

上游对应物是 `workflows/code_implementation_workflow.py`（1,184 行）。教学切片只保留 per-file 编排循环——大概 200 行 Python——把 MCP server 启动、文档分段、retry-shrink token 策略、progress-callback 都丢掉。剩下来的是核心：一个 per-file body，一次问模型一个工具调用，每个调用都过 loop detector，dispatch 工具，每次 `write_file` 后压缩记忆，每个文件一行 JSONL。

输出是一个有类型的 `RunReport`：

```go
type RunReport struct {
    Status             string  // "completed" | "aborted" | "max_iterations" | "max_time" | "error"
    Reason             string
    FilesCompleted     int
    Total              int
    Iterations         int
    Elapsed            time.Duration
    UnimplementedFiles []string
}
```

跟上游 `_last_run_state` dict 字段一一对应——但是有类型、按值返回、没有那 1000 行 glue。

## Solution

```ascii-anim frames=1
                  ┌─────────────────────────────────────────────────────┐
                  │  Workflow.Run(ctx, planPath, taskDir)               │
                  └────────────────────────┬────────────────────────────┘
                                           │
                       LoadPlan(planPath) ─┼─ MemoryAgent{InitialPlan}
                                           │  NewLoopDetector()
                                           │  NewRunner(provider, detector)
                                           │
                                           ▼
                  ┌────────────────────────────────────────────────────┐
                  │  for each file in plan.Files:                      │
                  │                                                    │
                  │    1. 已在磁盘 → skip（resume 支持）                 │
                  │    2. reg = NewRegistry();                          │
                  │       registerFileScopedTools(reg, codeDir)        │
                  │    3. messages = [system, plan, "implement <f>"]   │
                  │    4. runner.Run(ctx, RunSpec{...})                │
                  │         │                                          │
                  │         │   每次内层迭代：                            │
                  │         │     a. provider.Chat(req)                │
                  │         │     b. 每个 call: detector.CheckTool      │
                  │         │        若 ShouldStop → StopAborted        │
                  │         │     c. dispatch 每个 call，append result │
                  │         │     d. OnToolResult → write_file 时        │
                  │         │        memAgent.Compact(messages)        │
                  │         │     e. final-text → StopDone             │
                  │         │     f. 迭代用尽 → StopMaxIterations       │
                  │         ▼                                          │
                  │    5. AppendJSONL(attempts.jsonl, fileAttempt)     │
                  │    6. aborted/max_iter/error → 跳出外层             │
                  └────────────────────────────────────────────────────┘
                                           │
                                           ▼
                  ┌────────────────────────────────────────────────────┐
                  │  RunReport{Status, FilesCompleted, Total,          │
                  │   Iterations, Elapsed, UnimplementedFiles}         │
                  │  AtomicWriteJSON(implementation_report.json)       │
                  └────────────────────────────────────────────────────┘
```

五个值得说明的设计决定：

1. **Session 隔离强制重新声明。**别的会话的类型（Runner / LoopDetector / MemoryAgent / AtomicWriteJSON / AppendJSONL）字节兼容但全部独立声明。s10 自己有一个 `Runner`（~150 LOC）、`LoopDetector`（~80 LOC）、`MemoryAgent.Compact`（~100 LOC）、`AtomicWriteJSON`（~30 LOC）、`AppendJSONL`（~30 LOC）。代价是代码重复；好处是 `cd agents/s10-code-impl-workflow && go test ./...` 自给自足，磁盘上没有别的章节源码也能跑过。
2. **Runner 是集成点，不是 workflow。**workflow 主循环是直线代码；所有跨机制 glue 在 `runner.Run` 内部发生。具体来说：runner 在每次 dispatch 之前调用 `detector.CheckTool`（s08 集成）；每次 dispatch 后通过 `RunSpec.OnToolResult` 触发 workflow 的回调（s09 集成）。workflow 自己只负责"按文件迭代+计分"。
3. **Resume 看磁盘，不看 flag。**`os.Stat(filepath.Join(codeDir, file))` 是"这个文件做没做完"的唯一真理——不是 memory-agent 的 flag，不是 JSONL 重放。磁盘上已存在的文件直接 skip；新文件完整跑一遍。测试预先创建 `a.go`，然后验证 workflow 只发了 4 次 provider 调用，对应 `b.go` 和 `c.go`（如果重做了 `a.go` 就要 6 次）。
4. **每次 write_file 都 Compact。**上游用 `should_trigger_memory_optimization(messages, files_implemented_count)` 把 Compact 包起来；教学切片每次 write_file 之后都无条件 Compact。逻辑更简单，token 上略激进。挂钩点是 `RunSpec.OnToolResult`，每次 tool 结果都会 fire 一次 `(name, args, result, isError)`。
5. **`RunReport` 是类型化的。**上游返回 `dict[str, Any]`——research-notes 反模式 #5 直接点名这是隐患。Go 端是一个 struct，五值的 Status 枚举调用方直接 switch 就行，没有 string-key lookup，没有 KeyError。

## How It Works

### 1. `types.go` — 重新声明的最小形状

约 80 行。和 s06 同样的形状：`Provider`、`Tool`、`Message`、`ContentBlock`、`ChatRequest`、`ChatResponse`、`ToolCallRequest`、`ToolSchema`、`Usage`。`Input` 是 `json.RawMessage`，让 runner 可以零拷贝 dispatch 任意工具参数。

### 2. `registry.go` — 最小 `Registry`

30 行。`NewRegistry / Register / Get / List`。没有 `Close()` 生命周期（s02 管那个），没有 builtin-vs-MCP 排序，没有 schema 缓存失效。重复注册同名直接覆盖——last write wins。

### 3. `loop_detector.go` — 5 个状态码

80 行。`CheckTool(name) Status` 返回 `ok | loop_detected | timeout | stall | max_errors` 中的一个。没有 `Clock` 接口（s08 有，为了 hermetic 测试；s10 的测试用 replay provider，wall-clock 预算还没到 replay 就走完了）。runner 集成处：

```go
for _, call := range resp.ToolCalls {
    if status := detector.CheckTool(call.Name); status.ShouldStop {
        return RunResult{
            StopReason:  StopAborted,
            AbortReason: status.Message,
            Iterations:  i + 1,
        }, nil
    }
}
```

注意 AbortReason 字段——这就是 workflow 把 detector 的 message 透传到 `RunReport.Reason` 的方式，不需要二次查询。

### 4. `memory.go` — 最小 `MemoryAgent.Compact`

100 行。算法和 s09 一样：保留 [system, 合成 plan, ...上次 write_file 边界之后的消息]，把不在白名单的工具块丢掉。同样的 8 工具白名单（`read_file` / `write_file` / `execute_python` / `execute_bash` / `search_code` / `search_reference_code` / `get_file_structure` / `read_code_mem`），同样的边界配对扫描。

相比 s09 多了什么：s10 加了 `MessagesTokens` 辅助函数，workflow 在每次 Compact 后调用它一次。这一次调用就把配置好的 Tokenizer 跑了一遍，于是把一个计数版 Tokenizer mock 塞进 `Workflow.MemoryTokenizer`，测试就能证明 workflow 真的跑了它声称跑过的那次压缩。

### 5. `runner.go` — 集成循环

150 行。精简版 s06，多了两件事：

- `RunSpec.Detector *LoopDetector`——每次 dispatch 前的 pre-tool gate。
- `RunSpec.OnToolResult func(name, args, result, isError)`——每次 dispatch 之后的遥测钩子。workflow 用它检测成功的 write_file，触发 Compact。

外加一个新的 `StopAborted` 原因，把 detector 的 message 直接透传给上层，不需要重建 dispatch path。其他（截断、错误 → IsError tool_result、MaxIterations apology）和 s06 一致。

### 6. `tools_filesystem.go` — 三个小工具

`read_file`、`write_file`、`execute_python`。前两个是真的（按 workflow 注册时设置的工作区目录读写文件）；`execute_python` 是一行 stub，永远返回 `"OK [stub]"`。重点不是真执行——重点是 runner 通过同一条 dispatch path 调用一个非文件系统工具，证明 dispatch 循环是通用的。

### 7. `workflow.go` — 编排器

200 行。主体是一个 for 循环遍历 `plan.Files`。每次迭代：skip-if-exists、新建 registry、构建初始消息、调 `runner.Run`、append JSONL、按 `StopReason` switch。任何非 `StopDone` 的结果都会把外层循环跳出，并写入对应的 `Status`。

`OnToolResult` 回调是 s09 的接入点：

```go
OnToolResult: func(name string, args json.RawMessage, result string, isError bool) {
    if isError || name != "write_file" {
        return
    }
    messages = memAgent.Compact(messages)
    _ = MessagesTokens(memAgent.Tokenizer, messages)
    if w.OnCompact != nil {
        w.OnCompact()
    }
},
```

`messages` 是 `implementOneFile` 闭包捕获的局部——Compact 返回新 slice，局部覆盖，下次迭代 `runner.Run` 在它的下一次 provider 调用上看到压缩后的状态。

### 8. `plan.go` — 名字是 .yaml 但内容是 JSON

`LoadPlan(path)` 从磁盘读 `{"files": ["a.go", ...]}`。fixture 文件叫 `plan_minimal.yaml` 是因为读者预期上游就是 YAML。未来工作：用 `gopkg.in/yaml.v3` 换上真 YAML reader。我们选 JSON-now-YAML-later 是为了让 s10 的 `go.mod` 保持零依赖。

### 9. `atomic.go` 和 `jsonl.go` — 持久化原语

形状跟 s07 一样。AtomicWriteJSON 用 tmp+sync+rename 写最终的 RunReport 快照（`taskDir/implementation_report.json`）。AppendJSONL 把每个文件一行 JSON 序列化到 `taskDir/implementation_attempts.jsonl`，每个绝对路径有独立的 sync.Mutex 防止同进程内并发写交错。

## What Changed (vs. s09)

```diff
+ types.go        ~80 LOC    重新声明 Provider/Tool/Message/ContentBlock/ChatRequest/
+                            ChatResponse/Usage/ToolCallRequest（Input 用 json.RawMessage
+                            让 runner 零字符串往返地 dispatch）
+ registry.go     ~30 LOC    最小 Registry（无 Close，无 MCP-vs-builtin 顺序）
+ loop_detector.go ~80 LOC   5 个状态码（无 Clock——replay provider 不消耗 wall-clock）
+ memory.go       ~100 LOC   Compact + EssentialTools + Tokenizer + MessagesTokens
+ runner.go       ~150 LOC   精简版 s06 + LoopDetector pre-tool gate + OnToolResult
+                            + StopAborted 原因
+ tools_filesystem.go ~80 LOC  真 read_file/write_file + stub execute_python
+ plan.go         ~50 LOC    JSON Plan 加载器（YAML 推后）
+ atomic.go       ~50 LOC    AtomicWriteJSON
+ jsonl.go        ~50 LOC    AppendJSONL
+ report.go       ~30 LOC    RunReport struct + Status 枚举
+ workflow.go     ~200 LOC   编排器：per-file 循环、Compact-on-write_file、JSONL append、
+                            RunReport 装配
+ main.go         ~80 LOC    CLI 演示 + ReplayProvider
+ workflow_test.go            5 个 hermetic 测试，全部 t.TempDir() + ReplayProvider
- 不 import s02/s04/s06/s07/s08/s09 中的任何一个。Session 隔离强制执行。
```

s09 是纯函数（`messages → messages'`）——无 I/O，无 goroutine，单测试文件 5 个 case。s10 正相反：11 个源文件、真磁盘写入、JSONL 并发追加、跨整个 stack 的集成测试。这一章长，恰恰是因为"组合而不耦合"成本高——而那个成本就是过去 9 章拼得起来的证明。

## Try It

```bash
cd agents/s10-code-impl-workflow

# 演示：3 个文件的对话回放，写到临时目录
go run . -plan testdata/plan_minimal.yaml -replay testdata/replay_three_files.json

# 测试（5 PASS，<1s）
go test -count=1 -v ./...
```

演示输出：

```
status:           completed
reason:
files_completed:  3/3
iterations:       6
elapsed:          2ms
task_dir:         /tmp/s10-demo-XXXXXXXX
```

看一眼 task dir：

```
$task_dir/
├── generate_code/                     # 物化后的 plan
│   ├── main.go
│   ├── config.go
│   └── handler.go
├── implementation_attempts.jsonl      # 每文件一行 JSON
└── implementation_report.json         # 最终 RunReport，原子写
```

每行 attempt：

```json
{"file":"main.go","timestamp":"2026-05-09T...","stop_reason":"done","iterations":2}
```

## Upstream Source Reading

```upstream:workflows/code_implementation_workflow.py#L41-L80
class CodeImplementationWorkflow:
    def __init__(self) -> None:
        self.default_models = get_default_models()
        self.logger = self._create_logger()
        self.mcp_agent = None
        self.enable_read_tools = True
        self.loop_detector = LoopDetector()
        self.progress_tracker = ProgressTracker()
        self._last_run_state: Dict[str, Any] = {
            "status": "unknown",
            "reason": None,
            "iterations": 0,
            "elapsed_seconds": 0.0,
            "files_completed": 0,
            "total_files": 0,
            "unimplemented_files": [],
        }
```

```upstream:workflows/code_implementation_workflow.py#L506-L560
if response.get("tool_calls"):
    aborted_in_tool_check = False
    for tool_call in response["tool_calls"]:
        loop_status = self.loop_detector.check_tool_call(tool_call["name"])
        if loop_status["should_stop"]:
            run_state = {"status": "aborted", "reason": ...}
            aborted_in_tool_check = True
            break
    if aborted_in_tool_check:
        break

    tool_results = await code_agent.execute_tool_calls(response["tool_calls"])

    for tool_call, tool_result in zip(response["tool_calls"], tool_results):
        is_error = tool_result.get("isError", False)
        if not is_error:
            self.loop_detector.record_success()
            if tool_call["name"] == "write_file":
                filename = tool_call["input"].get("file_path", "unknown")
                completed_first_time = self.progress_tracker.complete_file(
                    memory_agent.normalize_file_path(filename))
        else:
            self.loop_detector.record_error(...)
        memory_agent.record_tool_result(...)

    if memory_agent.should_trigger_memory_optimization(
            messages, code_agent.get_files_implemented_count()):
        messages = memory_agent.apply_memory_optimization(
            current_system_message, messages, files_implemented_count)
```

**阅读注释**：

- **Loop detector 的调用位置 Go 端和 Python 端完全一致**——都在 dispatch 之前，不在之后。"dispatch 之后再 fire 的循环检测"已经晚了：失控的工具已经跑过了。
- **Memory compaction 上游有 gate，Go 端无条件跑。**上游的 `should_trigger_memory_optimization(messages, files_implemented_count)` 同时检查消息条数和 token 预算；教学切片每次 write_file 都 Compact。代价是 token 用得略激进，回报是少一台状态机要推理——教学场景里简单版赢。
- **状态枚举完全一致。**`completed | aborted | max_iterations | max_time | incomplete` 上游 → `completed | aborted | max_iterations | max_time | error` Go。唯一改名是 `incomplete` → `error`，配合 Go 把 incomplete 留给"非致命部分成功"的惯例。
- **Resume 支持上游用 `_check_file_tree_exists`**；Go 端在外层循环里逐文件 `os.Stat`。上游在 workflow 入口就检（跳过整个 `create_file_structure`）；Go 端在文件迭代入口检（跳过单个文件）。逐文件 resume 粒度更细——跑 30 个文件的 plan 只成功了一半时特别有用。

**继续阅读**：`workflows/agent_orchestration_engine.py`（2,312 行）——上游的总指挥，把 `CodeImplementationWorkflow` 当 11 个 phase 中的一个调用。**故意不做章节**。带注释提取版：[`upstream-readings/s10-code-impl-workflow.py`](../../upstream-readings/s10-code-impl-workflow.py)。

---

**下一站**：没有 s11 了。课程的代码章节到这里就停了；后面 s_full 和 Appendix 全部是文档章节。如果你从 s01 一路读到 s10，你已经从"对 Anthropic API 发出最小 HTTP 请求"走到了"一个能把 plan 编译成代码目录的多机制 agent 系统"。这条弧线就是这本书。
