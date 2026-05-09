---
title: "附录 B · 上游源码导读地图"
chapter: "appendix-b"
slug: appendix-b-upstream-map
est_read_min: 22
---

# 附录 B · 上游源码导读地图

> 学习版只移植了 10 个机制；上游有 200+ 个 Python 文件。这一章是从 *学习版* 出发回到 *上游* 的索引——告诉你按什么顺序读、每个文件对应哪一章、读完一章还想往深里走该读什么、五个练习把骨架长成肌肉、还有顶层目录速览让你不至于在 88 MB 的 repo 里迷路。

---

## 阅读顺序 / Reading order

学习版的 10 章是按 *依赖拓扑* 排的（s01 → s10），上游源码也建议按同样顺序读——先读叶子（registry、provider、context），再读组合 (runner、planning、memory)，最后读顶层 (agent_orchestration_engine)。下面 12 个文件覆盖学习版的每一章 + 2 个延伸文件：

| 阅读顺序 | 上游文件 | 对应章节 | 一句话提示 |
|---|---|---|---|
| 1 | `core/agent_runtime/tools/registry.py` | s02 | `ToolRegistry` 用 `AsyncExitStack` 管 MCP 子进程生命周期；注意 `_cached_definitions` 怎么因 register/unregister 失效。 |
| 2 | `core/config.py` | s03 | `DeepCodeConfig`（pydantic-settings）+ `${ENV_VAR}` 插值 + `get_agent_settings(phase)` 的 fallback 合并。 |
| 3 | `core/providers/base.py` | s04 | `LLMProvider` ABC、`ProviderSpec` 元数据、`LLMResponse` 规范化字段——后两个 provider 都讲一种语言的源头。 |
| 4 | `core/providers/anthropic.py` | s04 | 第一个 backend 实现：messages API、content blocks 转 `ToolCallRequest`、retry-on-overload。 |
| 5 | `core/providers/openai_compat.py` | s04 | 第二个 backend：chat completions API、`tool_calls` 数组转同一个 `ToolCallRequest`、`finish_reason` 重命名。 |
| 6 | `workflows/workflow_context.py` | s05 | `@dataclass(slots=True)` 的不可变契约；`to_dir_info()` 是给老 phase 留的桥（你会想 *不* 抄它）。 |
| 7 | `core/agent_runtime/runner.py` | s06 | `AgentRunSpec` + `AgentRunner.run` 主循环；学习版只取 1-400 行，剩余 400-1065 是延伸阅读。 |
| 8 | `workflows/planning_runtime.py` | s07 | 5 个必需 section、atomic-write 三件套（checkpoint / attempts / meta）、`is_existing_plan_usable`。 |
| 9 | `utils/loop_detector.py` | s08 | `note_llm_wait()` 是关键创新——把 LLM 网络等待从 stall 计算里减出去。 |
| 10 | `workflows/agents/memory_agent_concise.py` | s09 | `_COMPACTABLE_TOOLS` 白名单 + `should_trigger_memory_optimization` gate + `apply_memory_optimization` 实施。 |
| 11 | `workflows/code_implementation_workflow.py` | s10 | 文件级实现循环把上面 6 个机制全装进一个 for 循环；`_last_run_state` 是上游版的 `RunReport`。 |
| 12 | `workflows/agent_orchestration_engine.py` | (s_full 之外) | 2,312 行的总指挥；学习版故意不移植它——读完前 11 个文件再读它，否则会被吓退。 |

**怎么读**：每个文件用一个上午（90 分钟）。先通读一遍把作者的脉络抓住；再回头跟学习版的 Go 重写并排对照——Python 哪一行变成了 Go 哪一行；最后看 git blame 上的 commit message，理解作者当时在解决什么。学习版的 10 章 README 都给了 `Upstream ref` 行号，照着定位。

## 文件→章节映射 / File-to-session map

下面这张表是 `research-notes.md` 的 file-to-session map 加上 5 个 stretch 文件的延展。每个路径都在 `/Users/yeding/learn-DeepCode/.learn/upstream/` 下验证过存在。

| 上游文件 | 章节 | 关键行 | 为什么这文件重要 |
|---|---|---|---|
| `core/agent_runtime/tools/registry.py` | s02 | 11-130 | `ToolRegistry` 全类——所有 agent 调任何工具的入口 |
| `core/config.py` | s03 | 1-250 | `DeepCodeConfig` + 整个配置树 + `get_agent_settings(phase)` |
| `deepcode_config.json.example` | s03 | 1-130 | 配置 schema 的真实样例；测试 fixture 的来源 |
| `core/providers/base.py` | s04 | 100-250 | `LLMProvider` ABC + `ProviderSpec` + `LLMResponse` |
| `core/providers/anthropic.py` | s04 | 26-200 | 第一个 backend：Anthropic Messages API |
| `core/providers/openai_compat.py` | s04 | 1-250 | 第二个 backend：OpenAI Chat Completions |
| `core/providers/registry.py` | s04 (stretch) | 1-150 | 多 provider 注册 + 名字到 backend 的关键词路由 |
| `workflows/workflow_context.py` | s05 | 62-120 | 不可变 `WorkflowContext` 全 dataclass |
| `core/agent_runtime/runner.py` | s06 | 69-400 | `AgentRunSpec` + `AgentRunner.run` 主循环 |
| `core/agent_runtime/runner.py` | s06 (stretch) | 400-1065 | length-recovery、injection、tool-result compaction——课程外但很真实 |
| `workflows/planning_runtime.py` | s07 | 1-263 | 整个文件——validation + atomic write + JSONL append |
| `utils/loop_detector.py` | s08 | 12-253 | 整个文件——loop / timeout / stall / max-errors |
| `workflows/agents/memory_agent_concise.py` | s09 | 27-300 | 白名单 + 触发逻辑 + 应用逻辑 |
| `workflows/code_implementation_workflow.py` | s10 | 41-560 | 主体编排循环 + `_last_run_state` 状态机 |
| `workflows/agent_orchestration_engine.py` | (s_full 之外) | 80-500 | 顶层 phase 调度器；读到这里说明你已经吃透前 11 个文件 |
| `tools/code_implementation_server.py` | s10 (stretch) | 全文件 | 上游 MCP server 的真实 schema：`read_file`、`write_file`、`execute_python` 等 |
| `core/llm_runtime.py` | s04 (stretch) | 全文件 | `get_workflow_provider(phase, provider_name, model)` 把 config 解析跟 provider 实例化粘起来 |
| `prompts/code_prompts.py` | (Appendix A) | 全文件 | 每个 phase 的 system prompt——读完才知道 phase 协议长啥样 |
| `tools/command_executor.py` | s10 (stretch) | 全文件 | 跨平台命令执行 + 路径白名单 + 超时——MCP 边界的安全砖 |

## 章节延伸阅读 / Per-session deep dives

每一章学完之后，下面给一两个延伸文件——课程外，但能让你看到上游做了哪些学习版省略的事情。

**s01 — minimum-loop**
- `core/providers/anthropic.py:200-end` —— retry-on-overload + adaptive max_tokens shrink (87.5% → 95% → 98%)。学习版的 s01 只展示 happy path；这一段告诉你真实生产里第一个失败模式是 429。
- `core/providers/anthropic.py` 整体 (~600 行) —— streaming 支持、cache_control、prompt caching token 计数。学习版默认不流式；想加流式从这里开始。

**s02 — tool-registry**
- `tools/code_implementation_server.py` —— 上游 MCP server 的真实长相。`@mcp.tool()` 装饰器登记一组工具；`async def main()` 用 `stdio_server()` 在 stdin/stdout 上跑 JSON-RPC。学习版 s02 用 `io.Closer` 模拟生命周期；这个文件告诉你真 MCP 跑起来是啥样的。
- `core/agent_runtime/mcp_client.py` (如果存在) —— 客户端侧怎么 connect、handshake、call、close。

**s03 — config-loader**
- `core/llm_runtime.py` —— phase-aware provider 解析的真实入口：`get_workflow_provider(phase=..., provider_name=..., model=...)` 把 config 树读完之后 *实例化* 一个具体 provider。学习版 s03 只解析 config 不实例化；`llm_runtime.py` 是缺的那一半。
- `core/providers/registry.py` —— `ProviderSpec` 的定义和 keyword 路由（`"claude"` → Anthropic backend，`"gpt"` → OpenAI backend，等等）。

**s04 — provider-abstraction**
- `core/providers/registry.py` —— 学习版 s04 写死了两个 provider；这个文件告诉你怎么注册 N 个 provider，关键字怎么解析，env key 怎么找。
- `core/llm_runtime.py` —— 把 phase + provider_name + model 三个参数 resolve 成一个具体 provider 实例的胶水。Phase G 的多模型支持就靠这个。

**s05 — workflow-context**
- `workflows/agent_orchestration_engine.py:80-500` —— ctx 怎么 *构造* 出来的（phase 0+1）。学习版 s05 给了 `Prepare`，上游真实构造涉及 input source 解析、kind 探测、workspace 创建、log dir mkdir 一连串。
- `workflows/agent_orchestration_engine.py` 中所有 `ctx.` 出现的地方 —— 看 ctx 是 *只读* 的活生生证据：搜出来的所有引用全是读，没有写。

**s06 — tool-capable-runner**
- `core/agent_runtime/runner.py:400-1065` —— 学习版只搬了 1-400。剩余 600 行是真实生产里的关键：length-recovery（响应被 max_tokens 截断时怎么续）、injection（怎么在中间插一条用户消息）、tool-result compaction（单个工具结果太大时怎么截）、并行 tool-call 的处理。
- `core/agent_runtime/messages.py` (如果存在) —— message validation 的具体规则。学习版假设输入 well-formed；上游有一套 validation 处理 LLM 偶尔产出的畸形 message。

**s07 — planning-runtime**
- `workflows/plan_review_runtime.py` —— phase 5 (plan-review) 的运行时。学习版只做了 phase 4 (planning)；plan-review 是把 plan 拿给另一个 LLM 检视，确认 5 个 section 之间一致后才放行。
- `workflows/agent_orchestration_engine.py` 中 phase 4 → 5 的过渡逻辑 —— 包括什么时候跳过 review、什么时候强制 review、用户介入怎么进来。

**s08 — loop-detector**
- `utils/progress_tracker.py` —— 学习版 s08 把 ProgressTracker 一并讲了；上游里它是独立文件，跟 LoopDetector 互补：detector 防卡死，tracker 记录推进。
- `tests/phase9_progress_test.py` —— 上游对 ProgressTracker 的单元测试。看一下他们怎么用 mock 时间戳模拟"完成 1 个文件 → 完成 2 个文件"的转移。

**s09 — memory-compaction**
- `workflows/agents/memory_agent_concise_index.py` —— 索引版本的记忆 agent。学习版 s09 是 concise 基础版；index 版本把 *已生成代码的索引* 也保留进 memory，让后续文件能引用前面文件的 API。
- `workflows/agents/memory_agent_concise_multi.py` —— 多任务并行版本。学习版假设单任务；这个文件展示怎么在多个并行任务之间共享/隔离 memory。

**s10 — code-impl-workflow**
- `workflows/agent_orchestration_engine.py:500-1500` —— 学习版 s10 等价于上游的 phase 6-10；`agent_orchestration_engine.py` 在中间这一段把 phase 6-10 调起来，外加重试、错误恢复、用户取消处理。
- `tools/code_implementation_server.py` —— 学习版的 s10 用了三个内嵌工具 (`read_file`/`write_file`/`execute_python`)，上游的 MCP server 提供更长的 schema：`get_file_structure`、`search_code`、`search_reference_code`、`read_code_mem` 等等。把它读一遍能告诉你下游 LLM 真实可用的 tool surface 长什么样。

## 扩展练习 / Extension exercises

学习版没做、但你可以做。每个练习都对应学习版的一章，难度从易到难。

**练习 1：给 s04 加第三个 provider（Gemini 或 Deepseek）。**
难度：中。把 `agents/s04-provider-abstraction/openai.go` 复制成 `gemini.go`，把 endpoint、auth header、JSON schema 全换成 Gemini 的 generateContent API。难点是 Gemini 的 `tool_use` 用 `functionCall` 字段不是 `tool_calls`，得在 `decode()` 里写翻译层。验收标准：第三个 provider 通过和前两个相同的 5 个测试。上游对照：`core/providers/gemini.py`（如果存在）或者 `core/providers/openai_compat.py` 当模板（很多 OSS LLM 走 OpenAI-compatible）。

**练习 2：给 s09 实装真 BPE 分词。**
难度：难。学习版用 `len(s)/4` 当 token proxy；这个 proxy 在英文短句上准，在中文 / JSON / 代码上偏差很大。把 [tiktoken-go](https://github.com/pkoukk/tiktoken-go) 接进去，把 `Tokenizer` 接口的 `ByteLengthTokenizer` 实现换成 `TiktokenTokenizer`，跑 s09 的 5 个测试看 compaction 触发点漂移多少。验收标准：在长 JSON 输入上 byte-proxy 预测和 BPE 真实 token 数差异 > 30%。上游对照：`workflows/agents/memory_agent_concise.py:50-80`。

**练习 3：在 s10 里加 `BeforePlanning` hook。**
难度：中。学习版 s10 没有 plugin 系统（这是有意省略，把课程线缩短）。补一个最小 plugin 系统：定义 `Hook interface { Name() string; ShouldTrigger(ctx) bool; Run(ctx) error }`，让 `Workflow.Run` 在主循环开始前 fire `BeforePlanning` hook。补 1 个测试 hook 验证它被调到。这个练习会让你直观感受到上游 `workflows/plugins/` 为什么存在。上游对照：`workflows/plugins/base.py:34-150`、`workflows/plugins/integration.py:1-80`。

**练习 4：把 s07 的 `sync.Mutex` 换成 `flock` 做跨进程安全。**
难度：中。学习版 s07 的 `AppendJSONL` 用进程内 mutex；如果你在两个 Go 进程里同时往同一个 JSONL 文件写，mutex 不会互相看见。把 mutex 换成 `golang.org/x/sys/unix.Flock`（Linux/Mac）或 `LockFileEx`（Windows），跑两个 goroutine 模拟两个进程，验证写不会交错。这练习会让你看到上游为什么没用 flock（Python `asyncio` 单进程内本来就 cooperative）——但跨进程安全还是真问题，特别是在 daemon 部署里。上游对照：上游目前不做 flock；这是学习版可以*超过*上游的一个点。

**练习 5：在 s02 里接真 MCP stdio。**
难度：最难。这是 MCP 协议的真实集成：用 `os/exec.Command` 启动一个 MCP server 子进程（比如 `npx @modelcontextprotocol/server-filesystem /tmp`），跟它通过 stdin/stdout 走 JSON-RPC 2.0：`initialize` → `tools/list` → `tools/call`。把每个返回的 tool 注册到 s02 的 `Registry`。验收标准：在测试里启动 server，registry 能列出 server 暴露的工具，能调用一个工具看到响应。上游对照：`core/agent_runtime/tools/registry.py:50-90` 看 `AsyncExitStack` 怎么管子进程；`tools/code_implementation_server.py` 看 server 端 schema 长啥样。

## 上游目录速览 / Upstream directory tour

上游 `/Users/yeding/learn-DeepCode/.learn/upstream/` 一级目录速览。LOAD-BEARING = 跑起来缺它就崩；AUXILIARY = 跑起来没它也行。

```
DeepCode/
├── core/                      [LOAD-BEARING]
│   ├── providers/             多 LLM provider 抽象 + 注册 (s04 来源)
│   ├── agent_runtime/         tool-capable agent loop + tool registry + MCP (s02、s06 来源)
│   ├── sessions/              JSONL 持久化 session 存储 (Resume 支持)
│   ├── observability/         结构化 JSONL 日志 + LLM 调用 trace + event bus
│   ├── compat/                老 workflow → 新 agent runtime 的兼容层
│   ├── config.py              单 JSON 配置加载 (s03 来源)
│   └── llm_runtime.py         给 workflow 用的 LLM helper (phase 选择 + 日志)
│
├── workflows/                 [LOAD-BEARING]
│   ├── agent_orchestration_engine.py   总指挥 (~2,312 行；s_full 故意不移植它)
│   ├── agents/                          7 个专用 agent (s09 来源 + 延伸)
│   │   ├── memory_agent_concise.py      记忆压缩 (s09 来源)
│   │   ├── memory_agent_concise_index.py 带代码索引的记忆压缩
│   │   ├── memory_agent_concise_multi.py 多任务版记忆压缩
│   │   ├── code_implementation_agent.py  代码实现 agent
│   │   ├── document_segmentation_agent.py 长文档切片
│   │   └── requirement_analysis_agent.py  需求分析
│   ├── code_implementation_workflow.py  文件级实现工作流 (s10 来源)
│   ├── planning_runtime.py              规划运行时 + checkpoint (s07 来源)
│   ├── plan_review_runtime.py           plan review (phase 5)
│   ├── plugins/                          user-in-loop hook 系统 (Appendix B 练习 3 来源)
│   └── workflow_context.py              不可变 WorkflowContext (s05 来源)
│
├── tools/                     [LOAD-BEARING]
│   ├── code_implementation_server.py    主力 MCP server (read/write/exec/search)
│   ├── command_executor.py              安全跨平台命令执行
│   ├── code_reference_indexer.py        语义代码搜索 + 图构建
│   ├── code_indexer.py                  参考仓库相似度评分 (B11)
│   └── git_command.py                   Git 操作
│
├── prompts/                   [LOAD-BEARING] LLM system prompts (Appendix A 阅读源)
│   └── code_prompts.py        每个 phase 的 system prompt
│
├── cli/                       [AUXILIARY]
│   ├── main_cli.py            CLI 主入口 (--cli 模式)
│   ├── cli_app.py             交互式 CLI 应用
│   ├── cli_interface.py       CLI UI 接口
│   ├── cli_launcher.py        CLI 启动器
│   └── workflows/             CLI 使用的简化 workflow
│
├── new_ui/                    [AUXILIARY]
│   ├── backend/               FastAPI REST + WebSocket 后端 (uvicorn :8000)
│   ├── frontend/              React + Vite 前端 (npm run dev :5173)
│   └── scripts/               启动脚本
│
├── ui/                        [AUXILIARY] 老 Streamlit UI (--classic 模式)
│
├── schema/                    [AUXILIARY] Pydantic models for API requests/responses
│
├── nanobot/                   [AUXILIARY] Feishu/Telegram chatbot 集成
│
├── utils/                     [LOAD-BEARING]
│   ├── loop_detector.py       循环 / 超时 / 停滞探测 (s08 来源)
│   ├── progress_tracker.py    完成文件计数 (s08 延伸)
│   └── 其它 helper
│
├── tests/                     [AUXILIARY] 单测 (3 个文件——CI 不跑)
│   ├── phase9_progress_test.py     ProgressTracker + ConciseMemoryAgent 单测
│   └── ui_session_resume_test.py   SessionStore + WorkflowService resume 集成测试
│
├── config/                    [AUXILIARY] 静态配置文件
│
├── deepcode_docker/           [AUXILIARY] Docker 部署脚本 (run_docker.sh)
│
├── deepcode.py                [LOAD-BEARING] 顶层启动器 (--local / --classic / --cli)
├── deepcode_config.json.example  [LOAD-BEARING] 配置模板 (s03 fixture 来源)
├── README.md                  [AUXILIARY] 用户文档
├── requirements.txt           [LOAD-BEARING] Python 依赖清单
└── setup.py                   [LOAD-BEARING] PyPI 打包配置
```

**怎么用这张图**：

1. 想理解 *核心机制*——只看 `core/` 和 `workflows/` 和 `tools/`。这三个目录是学习版的全部输入。
2. 想理解 *用户接触面*——看 `cli/` 和 `new_ui/`。这是论文怎么从 PDF 流到 LLM 输入的实际通道。
3. 想理解 *运维*——看 `core/sessions/`、`core/observability/`、`deepcode_docker/`。这是生产部署的关键。
4. 想 *改一行代码就跑起来*——看 `deepcode.py`，里面 800 多行处理依赖检查、配置 sanity、子进程启停。
5. 想 *写一个论文里没写的功能*——看 `workflows/plugins/`，hook 系统是设计上唯一不用改 core 就能加功能的扩展点。

**关于阅读时长**：我们的体感是 LOAD-BEARING 文件全读一遍要 8-12 小时（一次集中坐下来 +1-2 次回看）。读完之后，学习版的 10 章 README 里的 "Upstream Source Reading" 一节会变得轻松——你已经知道作者在每个文件里在解决什么。这套地图就是为了把这 8-12 小时安排好，让你不至于在 88 MB 的 repo 里盲走。

参考阅读：
- 学习版 s_full (`docs/zh/s_full-integration.md`) —— 把 10 章拼起来跑一遍
- Appendix A (`docs/zh/appendix-a-multi-agent-philosophy.md`) —— 设计哲学
- 上游 README L838-861 —— 上游官方 quickstart
- 研究 notes (`/Users/yeding/learn-DeepCode/.learn/research-notes.md`) —— 12 个机制的详细 dossier

---

### 一些实用的 grep 路径

读上游源码的时候，下面这几条 grep 能立竿见影地节约时间：

```bash
# 找 ctx 的所有读点（应该全是读，没有写）
cd /Users/yeding/learn-DeepCode/.learn/upstream
grep -rn "ctx\." workflows/ | grep -v "ctx ="

# 找每个 phase 的入口（顶层调度器在哪里 fire 它们）
grep -n "phase" workflows/agent_orchestration_engine.py | head -30

# 找所有 MCP 工具名
grep -rn "@mcp.tool" tools/

# 找所有用 retry-on-overload 的 provider 路径
grep -rn "RateLimitError\|overloaded" core/providers/

# 找 plugin 系统所有 hook 点
grep -rn "InteractionPoint" workflows/plugins/

# 找所有 atomic write
grep -rn "tmp.*rename\|tempfile" workflows/
```

把这几条加到你的 shell history 里——第一次读上游源码时这是导航工具，第二次回头看代码时这是诊断工具。

### 建议的阅读节奏

如果你只有一个晚上：读阅读顺序的 1-3（registry + config + provider/base）。这 3 个文件覆盖学习版的 s02-s04，是上游"基础设施"的全部。

如果你有一个周末：1-7（加 workflow_context、anthropic、openai、runner）。这 7 个文件让你看完上游"骨架"——从配置到 provider 到 runner。

如果你有一周：1-11（全部 11 个核心文件）。读完之后再去碰 #12（agent_orchestration_engine）就有底了。

如果你有一个月：上面 12 个 + 每章 1-2 个延伸阅读 + 至少 2 个扩展练习。读到这一步你就具备给上游提 PR 的资格了。

### 上游路径的稳定性提示

DeepCode 是 v1.2.0（2026 年 5 月），还是 beta。`workflows/agent_orchestration_engine.py` 历史上拆过两次（v0.8 之前是 `workflow.py` 一个文件，v1.0 拆 `agent_orchestration_engine.py`，v1.1 又把 plugins 抽出去）。学习版 fix 在 sha `b9ece6035ea3f3582e6c503c517206b23c09ad09` —— 这是本附录所有路径的真相基准。如果你在 main 上读路径对不上，先 `git checkout b9ece60`，再回到本附录定位。

CHANGELOG.md 里有完整的 breaking change 列表。最近一次大动是 2026-04-17：`.env` 文件支持被砍掉，所有配置塞进单个 `deepcode_config.json`。这一改让 s03 的设计直接对应上了——单 JSON 是当下的真理，不是历史。

### 关于本附录之外的延伸

学习版只覆盖到 s10 + s_full。如果将来要加 s11，最自然的题目是 plugins (`workflows/plugins/base.py`)——hook 系统在 Go 里是 4-5 个 interface + 一个 registry，约 300 LOC，正好填进章节预算。s12 的候选是 sessions (`core/sessions/`)——JSONL session store + resume 逻辑，约 400 LOC。但这两章都不在 v1 课程里：本课程的弧线是 "从最小回路到 paper-to-code"，加进去会冲淡主题。如果你读完 v1 课程觉得意犹未尽，把这两个题目当 self-study 项目——本附录的扩展练习已经覆盖了一半。

最后，研究 notes (`/Users/yeding/learn-DeepCode/.learn/research-notes.md`) 里有完整的 12 个机制 dossier、glossary 15 词、anti-patterns 10 条——本附录把它们浓缩成了"哪些路径要读"，但那个完整版本里有更多 *为什么* 的细节。如果你打算把本附录当书签经常翻，那个研究 notes 是你应该读完的另一个补充。
