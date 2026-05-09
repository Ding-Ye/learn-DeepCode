---
title: "s_full · 端到端集成"
chapter: full
slug: s_full-integration
est_read_min: 18
---

# s_full · 端到端集成

> 十章读完，你已经亲手写了一个 provider 抽象、一个 registry、一个 runner、一个 loop detector、一个 memory agent、一个 workflow——但每章都是孤岛。本章不写一行新 Go 代码，把它们焊成一个完整的脑图：一条用户输入如何穿过五层架构，最后落成磁盘上的一棵代码目录树。

---

## 全栈架构图 / Full-stack architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│ L1  Workflow                                            s10              │
│     CodeImplementationWorkflow.Run(ctx, planPath, taskDir)               │
│     ── 读 plan → 逐文件循环 → JSONL 行 → RunReport (atomic write)         │
└──────────────────────────────────────────────────────────────────────────┘
            │ 每文件构造 RunSpec                       ▲ RunReport
            ▼                                          │
┌──────────────────────────────────────────────────────────────────────────┐
│ L2  Runner + Registry                                   s06 + s02        │
│     Runner.Run(ctx, RunSpec) → for ChatRequest → 派发工具 → 重复          │
│     Registry: name→Tool 映射 + schema 缓存 + Close 生命周期                │
└──────────────────────────────────────────────────────────────────────────┘
            │ ChatRequest{Messages, Tools, Model}        ▲ ChatResponse
            ▼                                            │
┌──────────────────────────────────────────────────────────────────────────┐
│ L3  Provider                                            s04              │
│     Provider.Chat(ctx, ChatRequest) (ChatResponse, error)                │
│     AnthropicProvider | OpenAIProvider —— 翻译各家 wire 格式               │
└──────────────────────────────────────────────────────────────────────────┘
            │ HTTPS                                      ▲ JSON
            ▼                                            │
┌──────────────────────────────────────────────────────────────────────────┐
│ L4  Config + LoopDetector + Memory                  s03 + s08 + s09     │
│     Config: 单 JSON + ${ENV} 替换 + phase 覆盖 → AgentSettings           │
│     LoopDetector: CheckTool / NoteLLMWait / RecordError                  │
│     MemoryAgent: Compact(messages) → messages'（清空式压缩）              │
└──────────────────────────────────────────────────────────────────────────┘
            │ 注入 Provider/Runner/Workflow              ▲ 决策反馈
            ▼                                            │
┌──────────────────────────────────────────────────────────────────────────┐
│ L5  Context + Planning                                  s05 + s07        │
│     WorkflowContext{TaskID, InputSource, InputKind, TaskDir}（不可变）   │
│     PlanningRuntime: ValidatePlanText + AtomicWriteJSON + AppendJSONL    │
└──────────────────────────────────────────────────────────────────────────┘
```

**用户请求的流向**：用户在 CLI 输入 → `WorkflowContext.Prepare` 决定 `InputKind` (s05) → planning runtime 把 plan 写到 `taskDir/planning_*.json` (s07) → workflow 读 plan、逐文件构造 messages (s10) → runner 调 provider、派发工具 (s06+s02) → provider 翻译为 Anthropic/OpenAI wire 格式 POST 出去 (s04) → 中间每一步都受 LoopDetector 保护、每一次 `write_file` 之后 MemoryAgent 压缩历史 (s08+s09) → 整条链路读的同一份 Config (s03)。返回路径反向走：JSON → ChatResponse → tool dispatch → 文件落盘 → JSONL append → RunReport。

**每一带的责任**：

- **L1 Workflow（s10）** 是 **唯一** 知道"任务的形状"的层。它读 `plan.yaml`，决定哪些文件要写、按什么顺序、写完之后报告什么。L1 不直接调 LLM——它把每个文件包成一个 `RunSpec` 扔给 L2。
- **L2 Runner+Registry（s06+s02）** 负责单个 agent 任务的循环。Runner 不关心任务是"写一个文件"还是"回答一个问题"，只关心"prompt → tool_use → 派发 → 结果回灌 → 重复 → 最终文本"。Registry 是工具的目录，Runner 是工具的派发器。
- **L3 Provider（s04）** 把 canonical 的 `ChatRequest` 翻译成 Anthropic 的 `messages[]` 或 OpenAI 的 `messages[]+tool_calls[]`，再把回包翻译回 canonical 的 `ChatResponse`。L3 是唯一接触 HTTP 的层。
- **L4 Config + LoopDetector + Memory（s03+s08+s09）** 是 **横切关注点**。配置在启动时注入；loop detector 在每次工具派发前 gate；memory agent 在每次成功 `write_file` 后 compact。三者都不属于主流程，但如果没有它们，主流程会跑飞、爆 token、读错 model。
- **L5 Context + Planning（s05+s07）** 是 **状态层**。WorkflowContext 是任务的"我是谁"——一个不可变的值，每个 phase 读但不写。PlanningRuntime 是任务的"我做了什么"——atomic write + JSONL append 让每次重启都能从断点恢复。

L1 → L5 的依赖是单向的：上层引用下层，下层不知道上层存在。这就是为什么 s05 和 s07 可以单独测试（不需要 LLM），s06 可以单独测试（不需要任务形状），s10 才是"必须把所有人都拉进来才能跑"的章节。

**与上游的对照**：上游的 `workflows/agent_orchestration_engine.py` 把 L1 + L4 + L5 揉在一份 2,312 行的 conductor 里——优点是一处看全，缺点是改 LoopDetector 的 stall 阈值要先读懂 11 个 phase 的 callback 链。learn-DeepCode 把"机制"和"编排"切开：每一带是一种机制，s10 是唯一的"编排"。这条边界让附录 B 的扩展练习（加 plugin 钩子、换 BPE tokenizer、接真 MCP）能在不动 s10 的前提下落地。

**与 anti-pattern 的对照**：research-notes 列出的 10 条上游 anti-pattern 在五带里有一一对应——`global state` (#1) 在 L4 用"显式 Config 注入"消解；`mixed async/sync` (#2) 在整本书用 `context.Context` 统一；`stringly-typed config` (#5) 在 L5 用 `WorkflowContext` 值类型解决；`callback overload` (#6) 在 L1 用 `RunReport` 返回值替代；`hardcoded LoopDetector thresholds` (#10) 在 L4 变成可配置字段。读完这张图，等于把上游的 10 条踩坑经验全部"抗体化"了。

**为什么是五带不是七带**：早期草案曾把 LoopDetector 单独成一带、Memory 单独成一带，但写到 s10 才发现这俩在编排里是同一种角色——"在 main flow 上挂钩子的横切关注点"。把它们合进 L4 让 s10 的代码读起来不像 7 层洋葱，而是 5 层夹心。这种"先按机制拆，再按依赖合"是 learn-DeepCode 全程在做的事。

## 16 步执行轨迹 / 16-step execution trace

以 README A3 的 Text2Backend 例子为输入：

> 用户：**"Build a REST API for a todo app with FastAPI, PostgreSQL, and JWT auth."**

下面是这条请求穿过 learn-DeepCode 的 16 步。每一步标注：**actor**（谁在做）、**action**（做什么）、**Go 实现位置**（在 learn-DeepCode 里由哪一章对应）。

| # | actor | action | learn-DeepCode 实现 |
|---|-------|--------|-------------------|
| 1 | User | 在 CLI 输入需求文本（或粘贴 paper） | `cmd/learn-deepcode/main.go`（doc stub） |
| 2 | CLI | 解析 argv，决定 `taskDir`、`planPath` | `cmd/learn-deepcode/main.go`（doc stub） |
| 3 | CLI | 调用 `WorkflowContext.Prepare(input, opts)` 构造不可变上下文 | [`s05-workflow-context`](./s05-workflow-context.md) |
| 4 | CLI | 加载 `deepcode_config.json`，env 替换，phase 解析 | [`s03-config-loader`](./s03-config-loader.md) |
| 5 | Orchestrator | 通过 `NewProviderFromConfig(cfg, "planning")` 构造 planner 用的 Provider | [`s04-provider-abstraction`](./s04-provider-abstraction.md) |
| 6 | Orchestrator | 构造 planner 的 system prompt + user message（含 todo-app 需求） | [`s05-workflow-context`](./s05-workflow-context.md) + [`s07-planning-runtime`](./s07-planning-runtime.md) |
| 7 | Planner LLM | 一次 `Provider.Chat` 返回 YAML 形式的 plan（含 5 个必需 section） | [`s04-provider-abstraction`](./s04-provider-abstraction.md) |
| 8 | Orchestrator | `ValidatePlanText(text)` 检查 5 必需 section 全在 | [`s07-planning-runtime`](./s07-planning-runtime.md) |
| 9 | Orchestrator | `AtomicWriteJSON(taskDir/planning_checkpoint.json)` + `AppendJSONL(planning_attempts.jsonl)` | [`s07-planning-runtime`](./s07-planning-runtime.md) |
| 10 | Orchestrator | 把 plan 持久化到 `taskDir/plan.yaml`（s10 的输入） | [`s07-planning-runtime`](./s07-planning-runtime.md) + [`s10-code-impl-workflow`](./s10-code-impl-workflow.md) |
| 11 | Workflow | `CodeImplementationWorkflow.Run(ctx, planPath, taskDir)` 入口 | [`s10-code-impl-workflow`](./s10-code-impl-workflow.md) |
| 12 | Workflow | 读 plan，迭代 `["main.py", "models.py", "routes.py", "auth.py", ...]` | [`s10-code-impl-workflow`](./s10-code-impl-workflow.md) + [`s07-planning-runtime`](./s07-planning-runtime.md) |
| 13 | Runner | 每文件构造 `RunSpec`，调 `Runner.Run(ctx, spec)` | [`s06-tool-capable-runner`](./s06-tool-capable-runner.md) |
| 14 | Runner | 内循环：`Provider.Chat` → tool_calls → `LoopDetector.CheckTool` 把关 → `Registry.Get(name).Run` 派发 → 结果回灌 | [`s06-tool-capable-runner`](./s06-tool-capable-runner.md) + [`s02-tool-registry`](./s02-tool-registry.md) + [`s08-loop-detector`](./s08-loop-detector.md) |
| 15 | Runner | 成功 `write_file` 之后 `OnToolResult` 触发 `MemoryAgent.Compact(messages)` | [`s09-memory-compaction`](./s09-memory-compaction.md) |
| 16 | Workflow | 写 `implementation_attempts.jsonl` + atomic 写 `implementation_report.json`，返回 `RunReport` | [`s10-code-impl-workflow`](./s10-code-impl-workflow.md) + [`s07-planning-runtime`](./s07-planning-runtime.md) |

**步骤 1-4** 是入口胶水（CLI parsing、上下文构造、配置加载），三章覆盖：s05 管"我是谁"，s03 管"我用什么模型"，剩下的 argv 解析放在 `cmd/learn-deepcode/main.go` 这个 doc stub 里——~30 行 Go 把 s05+s10 拼起来即可。

**步骤 5-10** 是 **planning phase**——一次同步 LLM 调用产生整个 YAML plan，然后两份 atomic 工件（checkpoint + attempts log）落盘。这一段是 s05 + s07 的主战场。`ValidatePlanText` 检查 `file_structure / implementation_components / validation_approach / environment_setup / implementation_strategy` 五段都在；缺一段就 fail。

**步骤 11-16** 是 **implementation phase**——s10 的舞台。每文件一次 `Runner.Run`，runner 内循环里 s02（registry 派发）+ s06（循环骨架）+ s08（CheckTool gate）+ s09（write_file 后 Compact）协同工作。每文件结束写一行 JSONL，最后 atomic 写最终 RunReport。

观察：

- Planning 是 **一次** LLM 调用产生整份 plan；implementation 是 **每文件一次** runner 循环。
- 工具调用全部走 registry，registry 是 dispatch 的唯一入口——这意味着 LoopDetector 只需要在一个地方插桩。
- 状态全在磁盘上：`planning_checkpoint.json` + `planning_attempts.jsonl` + `implementation_attempts.jsonl` + `implementation_report.json`。任何一刻 SIGKILL 都能从磁盘恢复。
- 16 步里 **没有任何一步** 跨进程通信。整条链路是一个 Go 进程内、一个 `context.Context` 串起来的 happy-path。上游用 async/await 让同样的链路看起来像分布式但其实也是单进程——learn 版用同步 + context 把这层伪装拿掉。
- 步骤 7 的 planner LLM 调用和步骤 14 的 runner LLM 调用 **都走 s04 的同一个 `Provider.Chat`**，差别只是 phase 不同（`planning` vs `implementation`），s03 的 phase merge 决定它们能用不同的 model。换 model 等于改一行 JSON。
- 步骤 14 的内循环 **每文件平均跑 2-4 轮**——一轮 `read_file` 看上下文，一轮 `write_file` 写代码，可选一轮 `execute_python` 跑 smoke test。Runner 只关心"还有 tool_calls 吗"，每文件几轮是 emergent 的。
- 步骤 15 的 `OnToolResult` 是 s09 整章的"勾子"——上游用 `record_tool_result + should_trigger_memory_optimization + apply_memory_optimization` 三个 callback 串起来；learn 版用一个 closure 抓住本文件的 `messages` 局部变量，read-modify-write 一行搞定。
- 步骤 16 的 `RunReport` 是 **唯一的返回值**——上游用 `_last_run_state: dict[str, Any]` 把同样的字段塞在 instance 上，learn 版用值返回让调用方做 switch。这是 anti-pattern #5（stringly-typed `dict`）的解法。

## 一条命令跑通 / One command to run

```bash
go run ./agents/s10-code-impl-workflow -plan testdata/plan_minimal.yaml -task-dir /tmp/learn-deepcode-task
```

这一条命令把整本书的代码至少行经一遍。展开：

1. **解析 argv** —— `cmd/learn-deepcode/main.go` 风格的入口（s10 自带 `main.go`），读 `-plan` 和 `-task-dir`。
2. **构造 ReplayProvider** —— demo 默认用 `testdata/replay_three_files.json` 的回放数据，不打真 API。如果想打真 API，换成 s04 的 `AnthropicProvider`。
3. **加载 plan** —— `LoadPlan(planPath)` 读 `{"files": ["main.go", "config.go", "handler.go"]}`（s10 + s07 的 ValidatePlanText 思想）。
4. **构造 Workflow** —— `Workflow{Provider, Tokenizer, ...}`，里面已经把 s06 的 Runner、s08 的 LoopDetector、s09 的 MemoryAgent 都装好。
5. **跑 `Workflow.Run(ctx, planPath, taskDir)`** —— 进入 s10 的逐文件循环：每文件构造 messages → runner.Run → 内部 s06 循环 → registry 派发 read_file/write_file（s02 风格）→ 每个 tool_call 经 s08 CheckTool 把关 → 成功 write_file 后 s09 Compact → 一行 JSONL appended（s07 primitive）。
6. **写 RunReport** —— atomic write `implementation_report.json`（s07 primitive），返回 `RunReport{Status:"completed", FilesCompleted:3, Total:3, ...}`。

预期 stdout（约 8 行）：

```
status:           completed
reason:
files_completed:  3/3
iterations:       6
elapsed:          2ms
task_dir:         /tmp/learn-deepcode-task
attempts_log:     /tmp/learn-deepcode-task/implementation_attempts.jsonl
report:           /tmp/learn-deepcode-task/implementation_report.json
```

预期 `task_dir` 下的产物：

```
/tmp/learn-deepcode-task/
├── generate_code/
│   ├── main.go
│   ├── config.go
│   └── handler.go
├── implementation_attempts.jsonl   # 3 行 JSON，每行 1 文件
└── implementation_report.json      # 最终 RunReport，atomic
```

每一行 JSONL 大致长这样：

```json
{"file":"main.go","timestamp":"2026-05-10T10:00:00Z","stop_reason":"done","iterations":2}
```

这一条命令同时验证了：s10 的 workflow 编排正确、s07 的 atomic 写不破坏现有文件、s06 的循环能在 ReplayProvider 下确定性退出、s02 的 registry 把 read_file/write_file 派发到正确的 Tool、s08 的 LoopDetector 在没有跑飞的场景下不误报、s09 的 Compact 在每次 write_file 后被调用一次。

**Hermetic 优先**：上面的命令默认走 ReplayProvider——零网络、零 API key、零 token 计费。这是 learn-DeepCode 整本书的测试纪律：每一章的 `go test ./...` 都能在飞机上、在地铁里、在公司防火墙后跑过。要切到真 Anthropic provider，把 `main.go` 里 `&ReplayProvider{...}` 换成 `&AnthropicProvider{APIKey: os.Getenv("ANTHROPIC_API_KEY")}` 即可，runner / workflow 的代码一行不改——这就是 s04 `Provider` interface 的全部价值。

**第二条命令 (live)**：

```bash
ANTHROPIC_API_KEY=sk-ant-... go run ./agents/s10-code-impl-workflow \
    -plan testdata/plan_minimal.yaml \
    -task-dir /tmp/learn-deepcode-live \
    -live
```

这条会真的调 `claude-sonnet-4-5` 三次（每文件一次），花费几美分。期间能在 `tail -f /tmp/learn-deepcode-live/implementation_attempts.jsonl` 看到一行行 attempt 记录涌现——这是把"代码生成"这件事从抽象变成可观测的最低成本。

## Deliberate omissions

learn-DeepCode 故意没有移植上游的若干特性。下表把 **不教什么** 摆到台面上——这样读者在生产环境复用本书代码之前，知道自己缺少哪些拼图，以及该去哪里补。

| Feature | 在上游的位置 | 为什么 learn-DeepCode 不教 | 可以在哪里加 |
|---------|------------|--------------------------|------------|
| Streaming SSE responses | `core/providers/anthropic.py` 的 `stream=True` 分支 | s01/s04 选了一次性 `messages.create` 让 wire 格式可读；流式 SSE 的解析（`event:`/`data:`）会让 s01 翻倍 | s04 的 `AnthropicProvider` 加一个 `ChatStream` 方法，分两套测试 |
| Prompt caching | `core/providers/anthropic.py` 的 `cache_control` 字段 | learn 版每次都重新发 system prompt——简化 wire 格式审查；上游通过 `ephemeral` 缓存 system + plan | s04 的 ChatRequest 加 `CacheBreakpoints []int`，AnthropicProvider 加 `cache_control: {"type":"ephemeral"}` |
| Multi-process orchestration | `workflows/agent_orchestration_engine.py` 2,312 行 | 那条 11-phase pipeline 是 50% 的胶水代码；s10 抽出最难的 implementation phase 即可教会读者 | 在 s10 之上加 s11"orchestration"，串 doc-segmentation → req-analysis → planning → impl |
| Plugin hooks (BEFORE/AFTER) | `workflows/plugins/base.py` 的 `InteractionPoint` | 钩子系统是另一种"Pythonic 反模式"（callback overload）；Go 等价物是 channel + struct，留给读者作为附录 B 练习 | s10 的 Workflow 加 `Hooks []func(Phase, *State)` slice |
| Document segmentation | `workflows/agents/document_segmentation_agent.py` | learn 版输入只支持文本/JSON plan，不需要切 PDF（>50K chars）；上游用 docling 拆 | 单独成 s12，使用 `gopkg.in/yaml.v3` 或 `unidoc/unipdf` |
| OpenRouter routing | `core/providers/openrouter.py` 加 `~/.deepcode/cache/openrouter_models.json` | learn 只教 native Anthropic + OpenAI-compat 两条路，第三方路由层是配置问题不是机制问题 | s04 的 factory 里加一个 keyword `openrouter` 分支 |
| MCP stdio framing | `core/agent_runtime/tools/registry.py` 的 `AsyncExitStack` 管理子进程 | 真 MCP 需要 JSON-RPC 2.0 framing + `os/exec` 子进程；s02 用 `io.Closer` 模拟即可教完生命周期管理 | 附录 B 练习 #5：用 `os/exec` + bufio.Scanner 实现 MCP stdio client |
| Retries with shrinking budget | `core/providers/base.py` 的 `chat_with_retry`（87.5% → 95% → 98%） | 自适应 token 预算是 production hardening 不是 mechanism；s06 留它在"out of scope" | s04 的 `AnthropicProvider` 包一层 `RetryProvider{Inner Provider, Budget []float64}` |
| Code reference indexer (B11) | `tools/code_indexer.py` 的 `FileRelationship` | LLM 驱动的"参考仓库相似度评分"是正交的预处理，不在 paper2code 主流程的关键路径上 | 单独成附录章节或 s13；输出 JSON 喂给 s10 的 plan |
| Observability / tracing | `core/observability/` 整个目录 | learn 版用 Go 标准 `log/slog` 即可；上游的 LLM call tracing + event bus 是企业部署需求 | s10 加 `Workflow.Logger *slog.Logger` 字段，runner 在每个 ChatRequest 前 emit 一条 |
| Session resumption | `core/sessions/` 的 JSONL session store | s10 通过 `os.Stat` 文件存在性做 per-file resume——已经覆盖最常见的 crash-recovery 场景 | 在 s07 之上加 `SessionStore.Load(taskID)` 用 JSONL 重放 |
| WebSocket progress streaming | `new_ui/backend/services/workflow_service.py` 的 WebSocket | learn 版是 CLI，不需要 push 进度到前端 | s10 的 Workflow 加 `Progress chan ProgressEvent`，CLI 消费打印 |

**怎么读这张表**：每一行代表一份"如果你照本书代码部署上线，可能踩的坑"。第二列指出上游怎么做，第三列说明 learn 版为什么做了减法，第四列给出在 learn 版里 **加回来** 的最小入口点——具体到改哪个文件、加什么字段。换句话说这张表既是免责声明，也是 v2 路线图。最高优先级的两条通常是：streaming（用户体验）+ retries（生产稳定性）；其余的视场景而定。

**与上游 anti-pattern 的关系**：表格里的 12 项里，有 4 项（streaming / caching / retries / observability）属于"上游做对了 learn 版省了"——这些是 production hardening；另外 4 项（multi-process orchestration / plugin hooks / OpenRouter / WebSocket）属于"上游有但形态欠佳"——research-notes 把它们标记为 anti-pattern；剩下 4 项（document segmentation / MCP stdio / code indexer / session resumption）属于"正交特性"——可以独立成单章。读者按这个三分法判断要把哪几项最先补回去。

## 跨章节回顾 / Cross-chapter recap

每章一句话回顾，按顺序读完这 12 个 bullet 等于把全书脑图复述一遍。每条 bullet 给三个东西：**机制**（这章教什么）、**上游文件**（对应的 Python 在哪里）、**你现在能**（读完之后多了哪一项推理能力）。

- **附录 A · 多智能体编排哲学**——教"为什么显式 agent 协议比 chatbot chain 好"、"不可变 context 如何当契约"、"MCP 为何是 I/O 边界"、"paper-to-code 可复现性的物理上限"。映射到 research-notes 的"Mental-model topics"5 节。**你现在能** 解释 learn-DeepCode 五带架构背后的设计哲学。
- **s01 · minimum-loop**——教 wire 格式心跳：一次 HTTP POST，一份 JSON 请求，一份 JSON 响应。上游 `core/providers/anthropic.py:26-150` 的简化版。**你现在能** 解释 Anthropic Messages API 的请求/响应字节流，无需 SDK。
- **s02 · tool-registry**——教 `name → Tool` 目录 + schema 缓存 + `Close()` 生命周期。上游 `core/agent_runtime/tools/registry.py:11-130`。**你现在能** 解释为什么 registry 必须管 MCP 子进程的 teardown。
- **s03 · config-loader**——教单 JSON 配置 + `${ENV}` 替换 + per-phase 覆盖。上游 `core/config.py:1-250`。**你现在能** 解释为什么"一份配置文件 + phase merge"比 12 个环境变量更可维护。
- **s04 · provider-abstraction**——教 `Provider` interface + Anthropic/OpenAI 双实现 + content-block 翻译。上游 `core/providers/base.py + anthropic.py + openai_compat.py`。**你现在能** 解释为什么 canonical `ChatResponse` 必须把 finish-reason 标准化为 `stop|tool_calls|length|error`。
- **s05 · workflow-context**——教不可变 task 状态 + 路径派生方法。上游 `workflows/workflow_context.py:1-168`。**你现在能** 解释为什么 11 个 phase 共享同一个 `WorkflowContext` 值比传 dict 安全。
- **s06 · tool-capable-runner**——教三分支主循环（tool_use / final-text / max_iterations）+ `IsError` tool_result + 截断策略。上游 `core/agent_runtime/runner.py:69-400`。**你现在能** 解释为什么 tool 失败不应该让 runner 失败。
- **s07 · planning-runtime**——教 atomic write（tmp+rename）+ JSONL append + 5-section plan validation。上游 `workflows/planning_runtime.py:1-263`。**你现在能** 解释为什么 atomic write 是 crash-safe 的最小成本。
- **s08 · loop-detector**——教重复检测 + wall-clock timeout + `NoteLLMWait` 偏移。上游 `utils/loop_detector.py:12-253`。**你现在能** 解释为什么 stall 检测必须扣掉 LLM 网络等待时间，否则误报率爆表。
- **s09 · memory-compaction**——教清空式压缩：保 system prompt + 初始 plan + 上次 write_file 之后的 essential tool 块。上游 `workflows/agents/memory_agent_concise.py:27-300`。**你现在能** 解释为什么"截断中间"会破坏 tool_use/tool_result 配对，而"清空式"不会。
- **s10 · code-impl-workflow**——教把 s02+s06+s07+s08+s09 编排成 file-by-file workflow + RunReport。上游 `workflows/code_implementation_workflow.py:41-500`。**你现在能** 解释为什么 5 种 mechanism 都得到位才能跑出一棵代码目录。
- **s_full · 集成（本章）**——教 5 带架构图 + 16 步执行轨迹 + 8-12 项 deliberate omission。无新代码。**你现在能** 把本书读到的每一章映射到上游的真实文件，并知道还差什么才能在生产环境落地。

**自检题（自己答完再看下一章）**：

1. 一次 `Workflow.Run` 跑 3 个文件，`Provider.Chat` 一共会被调用几次？答案在哪一章读到？
2. `LoopDetector.NoteLLMWait(d)` 不调会发生什么误报？影响哪一带？
3. 如果跳过 s09 的 Compact，runner 在第几个文件后开始爆 context？
4. `WorkflowContext` 是值类型而非指针，为什么这是设计选择不是代码风格？
5. s07 的 atomic write 用 tmp+rename，能否用 `os.WriteFile` 直接覆盖？为什么不？

每个问题对应的"必备技能"都在前面 12 个 bullet 里——如果某题答不上，回到对应章节重读 "How It Works" 一节即可。

## 阅读延伸 / Further reading

读完本书想继续深入，下面 5 条是优先级最高的下一步资料：

1. **DeepCode 上游 README + README_ZH** —— `https://github.com/HKUDS/DeepCode/blob/main/README.md`（英文）和 `README_ZH.md`（中文）。本书故意没复述这两份，因为它们已经写得很好；优先读它们的"Architecture"和"Quickstart"两节，对照本书的 5 带架构图。
2. **`/Users/yeding/learn-DeepCode/.learn/research-notes.md`** —— 本书所有章节的"原始证据档"。每个上游文件的行号引用、每个 anti-pattern 的 case、每个 mechanism 的定位都在那里。如果你想给本书加章（比如 s11 orchestration），先读这份。
3. **`/Users/yeding/learn-DeepCode/.learn/plan.md`** —— 课程蓝图。包括 shared types catalog（10 个 canonical Go interface 在哪里被使用）、每章的依赖图、风险与开放问题。**特别推荐** 看 "Per-session detail" 一节，会重新理解每章的"为什么这样切"。
4. **Anthropic Messages API 官方文档** —— `https://docs.anthropic.com/en/api/messages`。s01 + s04 + s06 反复用到的 wire 格式，唯一的"权威 spec"。注意 `tool_use` block 和 `tool_result` block 的字段定义、`stop_reason` 的取值、`cache_control` 的语义——这些是 learn 版省略 streaming/caching 时绕开的细节。
5. **MCP（Model Context Protocol）规范** —— `https://modelcontextprotocol.io/specification`。s02 用 `io.Closer` 模拟的 MCP 子进程，真实形态是 JSON-RPC 2.0 over stdio。附录 B 的 stretch 练习 #5 就是基于这份 spec 写一个真 MCP client。

延伸：上游 `workflows/agent_orchestration_engine.py`（2,312 行）是本书故意 **没有** 教的那一层，但如果你打算把 learn-DeepCode 扩成 s01 + ... + s10 + s11(orchestration)，这是必读的下一份；带注解的简化版可以参考 [`upstream-readings/s10-code-impl-workflow.py`](../../upstream-readings/s10-code-impl-workflow.py) 里 `_check_file_tree_exists` 之外的部分作为切入点。

**对照阅读建议**：每读上游一段 Python 都同时打开本书对应章节的 Go 代码——同样的 `for iteration in range(spec.max_iterations)` 在 s06 是 `for i := 0; i < spec.MaxIterations; i++`；同样的 `if response["tool_calls"]` 在 s06 是 `if len(resp.ToolCalls) > 0`；同样的 `loop_status["should_stop"]` 在 s10 是 `if status.ShouldStop`。两边 grep 跳着读 30 分钟，比单边线性读 2 小时学到的多。

**最后一条建议**：本书所有章节的代码都在 MIT 许可下；fork 它、在你的代码库里 vendor 部分章节、按你团队的 idiom 重写——learn-DeepCode 的目的是教会读者搭一个"足够小、自己能完全推理"的 agent 平台，而不是替任何生产系统打补丁。

---

至此 learn-DeepCode 的 12 章读完——10 章 Go 代码 + 1 章集成（本章）+ 附录 A/B。下一步推荐：跑一遍 `go test ./...` 验证所有章节绿；或者直接打开 `agents/s10-code-impl-workflow/main.go` 改一下 `replay_three_files.json`，体会"换一份回放数据，整个 workflow 跑出不同结果"的感觉。

如果你想把这本书"再向前推一步"，三个最有信号的方向：(1) 在 s04 里加 streaming（最贴近用户体验）；(2) 在 s06 里加 retries-with-shrinking-budget（最贴近生产稳定性）；(3) 在 s10 之上加 s11 orchestration，串 doc-segmentation → req-analysis → planning → impl 把上游 11-phase pipeline 还原成 Go 版本。这三步做完，learn-DeepCode 离"上游平替"只剩一份 streaming UI 的距离。

**最后一句话**：本书所有章节、所有图、所有 16 步执行轨迹的目的都是同一件事——让你在写下一个 agent 系统时，**脑子里的模型不再是黑盒**。每一行 Go 代码你都读过、改过、测过；每一份磁盘上的 JSON 你都知道从哪里来、要去哪里。这就是"open agentic coding"的开放性。

祝读完愉快。
