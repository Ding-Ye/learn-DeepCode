---
title: "附录 A · 多智能体编排哲学与论文到代码的可复现性"
chapter: "appendix-a"
slug: appendix-a-multi-agent-philosophy
est_read_min: 18
---

# 附录 A · 多智能体编排哲学与论文到代码的可复现性

> 这是一篇 *为什么* 的章节，不写一行 Go 代码。前面 10 章把 DeepCode 的核心机制一个一个搬到了 Go 里——s01 是最小请求-响应回路，s10 是文件级实现工作流。但读完十章你仍然欠自己一个问题：上游为什么这么设计？为什么不是一根聊天链？为什么把 context 做成不可变？为什么所有 I/O 都要走 MCP？为什么从一篇论文到一份能跑的代码，有些事情根本不能完全自动化？这一章就是答这五个问题。

---

## 显式智能体协议 vs. 聊天链 / Explicit agent protocols vs. chat chains

DeepCode 的最大架构选择，写在 `workflows/agent_orchestration_engine.py`（2,312 行）的 phase 枚举里：0=env，1=input-norm，2=doc-seg，3=req-analysis，4=planning，5=plan-review，6-10=impl，11=finalize。每个 phase 都有自己的 prompt（在 `prompts/code_prompts.py`），自己的输入契约（来自上一个 phase 的产物），自己的输出契约（写到磁盘的某个 JSON 或 YAML）。这不是一根聊天链；是一份 *协议*。

为什么不做成聊天链？因为聊天链有一个无法解决的失败模式：当 LLM 在第 3 轮搞错时，第 4 轮看不见错，因为它不知道"正确"长什么样。聊天链的状态全在历史记录里，错误一旦累积，没有锚点能让你切回来。显式协议的解法是：每一段输出都被立刻验证 (`workflows/planning_runtime.py` 中的 `validate_plan_text` 检查 5 个必需 section)，如果 schema 没过，整段 phase 重跑而不是把历史塞进下一个 prompt。学习版的 s07 把这个 5-section 验证完整还原过去——不是因为它好玩，而是因为没有 schema 的 LLM 输出就是一团雾。

```
聊天链:        显式协议 (DeepCode):
┌─────┐       ┌──────────┐
│ Q1  │       │ phase 0  │ env  → ctx.WorkspaceRoot
│ A1  │       └────┬─────┘
│ Q2  │            ▼
│ A2  │       ┌──────────┐
│ Q3  │       │ phase 1  │ input → ctx.InputKind
│ A3  │       └────┬─────┘
│ ... │            ▼
└─────┘       ┌──────────┐
              │ phase 2  │ doc-seg → segments[]
              └────┬─────┘
              ...
              ┌──────────┐
              │ phase 4  │ planning → plan.yaml (validated)
              └──────────┘
```

显式协议还有第二个好处：每个 phase 可以独立替换 LLM。`core/llm_runtime.py` 的 `get_workflow_provider(phase=..., provider_name=..., model=...)` 让你给 phase 4 用 GPT-5、给 phase 6-10 用 Claude Sonnet——因为每个 phase 的 I/O 是契约不是历史，模型可以混搭。聊天链做不到这件事，因为聊天链的历史是单一身份的累积。

学习版里 s10 是这种思路的浓缩版：plan 是 phase 4 的产物，s10 只读 plan，不读 plan 是怎么生成的。它甚至能消化一个手写的 `plan_minimal.yaml`——因为 s10 只信契约。这就是显式协议的最终好处：上下游解耦得彻底，下游甚至不需要知道上游存在。

参考阅读：
- `workflows/agent_orchestration_engine.py:80-500` —— phase 调度器的入口
- `prompts/code_prompts.py` —— 每个 phase 的 system prompt
- 学习版 s10 (`docs/zh/s10-code-impl-workflow.md`) —— phase 6-10 的 Go 重写

## 不可变上下文是契约 / Immutable context is a contract

Phase 与 phase 之间用什么传数据？上游给的答案是 `workflows/workflow_context.py:62-120` 的 `WorkflowContext`——一个 `@dataclass(slots=True)` 的不可变结构。task_id、input_source、input_kind、workspace_root、task_dir、paper_path、standardized_text 全部是字段，所有路径都是 `pathlib.Path`（绝对路径）。没有 `set_*` 方法；没有人能 mutate 它。

这不是 Python 风格洁癖；这是为了消灭一类现实中的 bug。DeepCode 早期的 commit 里，phase 之间传的是 `dir_info: dict[str, Any]`——一个 string-keyed dict，每个 phase 想加什么 key 就加什么 key，想读什么 key 就读什么 key。结果是：phase 5 读了一个 phase 3 没写过的 key，KeyError 在生产里随机出现；某个 callback 把 `paper_path` 改成了 `None`，下游所有 phase 一起崩溃。research-notes 的反模式 #5 直接点名了这个：stringly-typed config 是定时炸弹。

```
可变 dict[str, Any]:                 不可变 WorkflowContext:
┌──────────────────┐                ┌────────────────────┐
│ phase 1: ctx[X]=1│                │ phase 1: derive ctx│
│ phase 2: ctx[X]=2│ ←── mutation   │   pass to phase 2  │
│ phase 3: del X   │     race       │ phase 2: derive    │
│ phase 4: KeyError│                │   new value, NOT   │
└──────────────────┘                │   mutate parent    │
                                    └────────────────────┘
```

为什么是契约？因为不可变性等于"我承诺我看到的就是上游产生的，没人在中间改过"。phase 4（planning）拿到 ctx，写下 plan.yaml；phase 5（plan-review）拿到 *同一个* ctx，读 plan.yaml；如果 ctx 在中间被 mutate 了，那两个 phase 看到的就不是同一个世界。不可变性把这个不变量写进类型系统。

学习版的 s05 把这个机制完整搬过来了：`WorkflowContext` 是 Go struct，按值传递，没有指针接收者方法，没有 setter。Go 没有 `frozen=True` 关键字，但是按值传递 + 字段未导出 + 派生路径方法，就是同样的 guarantee。研究 notes 里的反模式 #4（string 路径混 `pathlib.Path`）也一并修复了：所有路径在 s05 里都是 `string`，但是 *经过 `filepath.Join` 派生*，并且永远是绝对路径。

参考阅读：
- `workflows/workflow_context.py:1-168` —— 整个文件
- 学习版 s05 (`docs/zh/s05-workflow-context.md`) —— Go 端的不可变 struct
- s07 (`docs/zh/s07-planning-runtime.md`) —— 第一个真正消费 `ctx.TaskDir` 的下游

## MCP 作为 I/O 边界 / MCP as the I/O boundary

DeepCode 不让 LLM 直接调 `os.write` 或 `subprocess.run`。所有副作用都走 MCP（Model Context Protocol）：一个 stdio 子进程，按 JSON-RPC 框架对话。`tools/code_implementation_server.py` 是其中最重要的一个——它暴露 `read_file` / `write_file` / `execute_python` / `execute_bash` / `search_code` / `get_file_structure` 等工具；`core/agent_runtime/tools/registry.py` 通过 `AsyncExitStack` 管理这些子进程的生命周期，schema 缓存做到性能 OK。

为什么要套这一层？三个理由。

第一是 *安全*。LLM 偶尔会自信地说"让我 `rm -rf /`"。如果 LLM 直接 shell out，你只能祈祷它不犯错。MCP 边界让你能在 server 里做白名单——`tools/command_executor.py` 就是干这个的：跨平台的命令执行 + 路径白名单 + 超时。Server 拒绝执行的事情，agent runner 永远看不到结果差异，它只看到一个 `is_error: true` 的 tool_result。

第二是 *协议解耦*。一个工具的实现可以是 Python，可以是 Go，可以是 Node——只要它讲 MCP 协议。这意味着团队 A 写一个 RAG server，团队 B 写一个浏览器自动化 server，它们都能挂到同一个 agent runner 上，谁都不需要知道对方的语言。学习版没有走到 MCP 这一步（s02 用 `io.Closer` 模拟生命周期），但是 Appendix B 的 exercise #5 留了一个练习：用 `os/exec` + JSON-RPC 框架 wrap 一个真 MCP stdio。

第三是 *可观测性*。每个 MCP 调用有 (name, args, result) 三元组——`core/observability/` 把这些写进 `task_dir/logs/mcp.jsonl`。生产事故复盘的时候，这个 JSONL 是单一真相源：LLM 想干什么，server 给了什么回复，下一步 LLM 怎么响应，全都记下来。学习版的 s10 用 `AppendJSONL` 把这种习惯还原成 `implementation_attempts.jsonl`——粒度比上游粗（每个文件一行而不是每个工具一行），但思路一致。

```
没有 MCP 边界 (聊天 + tool=os):    有 MCP 边界 (DeepCode):
┌──────────┐                      ┌──────────┐    ┌─────────────┐
│  LLM     │                      │  LLM     │    │ MCP server  │
│   ↓      │                      │   ↓      │    │  ┌────────┐ │
│  os.exec │ ← LLM 直接拿到 OS    │ tool_use │ →  │  │ allow- │ │
│  os.open │   全部能力            │ JSON-RPC │    │  │ list,  │ │
└──────────┘                      └──────────┘    │  │ timeout│ │
                                                  │  └────────┘ │
                                                  │   ↓ os.*    │
                                                  └─────────────┘
```

学习版的 s06 Runner 是这套思路的简化体现。Runner 没有跟 OS 直接打交道；它把 tool dispatch 委托给 `Registry`，registry 里的 tool 实现可以是内存里的纯函数（`echo`、`now`），可以是文件系统访问（s10 的 `read_file`/`write_file`），未来甚至可以是真 MCP——边界不变，实现自由。这就是抽象的好处。

参考阅读：
- `core/agent_runtime/tools/registry.py:11-130` —— 注册表 + AsyncExitStack
- `tools/code_implementation_server.py` —— 主力 MCP server
- `tools/command_executor.py` —— 命令执行白名单
- 学习版 s02 (`docs/zh/s02-tool-registry.md`)、s06 (`docs/zh/s06-tool-capable-runner.md`) —— Go 端 registry + dispatch

## 论文到代码的可复现性极限 / Paper-to-code reproducibility limits

DeepCode 在 PaperBench 上拿了 75.9% 对人类博士的胜率（README L326-373）——这是一个真实的、被论文承认的数字。但这个数字的存在不等于 *所有* 论文都能被自动复现。这一节列出 DeepCode 在工程上承认、并主动处理的几个不可复现性来源——读完之后你会知道哪些任务让它跑会成功，哪些任务你最好自己来。

**第一个极限：图。** `docling` 解析 PDF 时不做图 OCR；图里画的算法流程图、模型架构图、loss 曲线，全都是 raster 像素。如果论文核心贡献画在图里（典型例子：一个 GAN 架构图，文字里只说"如图 3 所示"），LLM 看到的是一段空文本+一句"如图 3 所示"，再聪明也补不出来。`workflows/agents/document_segmentation_agent.py` 给文本分段提取，但跨过了图。学习版没碰这一层（s05 把 `InputKind` 抽象出来但不解析 PDF），不过这是上游的真实限制。

**第二个极限：数据集。** 论文说"我们用 ImageNet"——好的，ImageNet 在哪？账号谁的？带宽多少？磁盘多大？DeepCode 不解决这个，它生成的代码会有占位符 `# TODO: download ImageNet here`，由人来填。这是 *设计选择*：让 agent 默默下数据集是"自动驾驶"，不是辅助驾驶；上游显式把这个边界画在 LLM 之外。

**第三个极限：超参数与随机种子。** 论文给了 lr=3e-4、batch=64，但是隐含了一千个没写出来的小决定（warmup 多少步？β1 是不是默认 0.9？随机种子是不是 42？）。DeepCode 的代码生成会做出 *一个* 合理选择，但不保证和原论文跑出来同一个数。这个限制是任何 paper-to-code 工具都逃不掉的，不是 bug。

**第四个极限：算力。** 你不能让 agent 系统跑一遍它生成的代码来验证——那要 100 张 H100、72 小时。DeepCode 的 phase 11（finalize）不跑训练，只跑单元测试 + 静态检查 + 一些小规模 sanity check。`tools/code_implementation_server.py` 的 `execute_python` 工具有 timeout，超过就 abort。这意味着 agent 拿到的是 *局部* 信号（"代码能 import 吗？"），不是端到端信号（"模型在 ImageNet 上能到 85%？"）。

```
论文 (PDF)
   │
   │ docling 解析     ← 图丢了 (极限 1)
   ▼
标准化文本
   │
   │ planning         ← 数据集占位 (极限 2)
   │                  ← 超参猜值 (极限 3)
   ▼
plan.yaml
   │
   │ implementation
   ▼
generated_code/
   │
   │ phase 11 验证    ← 只能跑小测试 (极限 4)
   ▼
RunReport
```

**为什么这一节重要？** 因为如果你期待 DeepCode 是"贴一篇 NeurIPS 2024 paper PDF 进去，按一下 run，明天醒来 ICLR 复现成功"，你会失望。但如果你期待它是 "把一篇有完整 pseudocode 的论文 + 一个数据集占位符，转换成一个有结构、有测试、能跑通单元测试的 Python 项目骨架"，你会发现它的 75.9% 胜率非常好用——*因为它有意识地在自己能做的事和做不到的事之间画了一条清楚的线*。

学习版没有重复这条线（s_full 的 demo 是从 plan.yaml 到代码目录，不从 PDF 开始），但是这条线是上游设计哲学的核心——agent 知道自己的边界，agent 不冒充博士生。

参考阅读：
- `workflows/agents/document_segmentation_agent.py` —— 长 PDF 切片
- `workflows/agents/requirement_analysis_agent.py` —— 把"做什么"提炼成 spec
- `tools/code_implementation_server.py` —— `execute_python` 的 timeout
- README L326-373 —— PaperBench 数字

## 与 Claude Code / Cursor / Aider 的对比 / Comparison vs. Claude Code, Cursor, Aider

学这本书的读者多半也用 Claude Code、Cursor、Aider。它们和 DeepCode 都自称 "AI coding agent"，但它们解决的问题不一样。这一节把差别讲清楚——既给你校准 DeepCode 的位置，也帮你想清楚自己的项目该用哪个。

**Claude Code（你正在用的这个工具）。** 工作模式：人 + agent 在 *现存* 代码库上交互。强项是 understand-modify 循环——读代码、改代码、跑测试、commit。它有自己的 tool 系统（Bash、Read、Edit、Grep、Glob），有 session memory，有 hook。它的 tool 是给 *agent* 用的，不是给 LLM 直接用——LLM 的 tool_use 经过 Claude Code 的 wrapper 才落到 OS 上。本质上 Claude Code 是 DeepCode 的 phase 6-10（implementation phase），但是更通用、更交互、不依赖前置的 plan。

**Cursor。** 工作模式：IDE + LLM 增强。Cursor 是一个 fork 的 VS Code，它的 LLM 集成在编辑器里——Cmd+K 改一段、Cmd+L 问 codebase 问题、Tab 补全。它没有 phase；没有 plan；没有跨文件的工作流。它强在 *单文件* 体验：把光标停在一个地方，告诉它你想要什么，它改这个地方。和 DeepCode 比，Cursor 不会从论文生成项目骨架，但你已经有项目骨架的时候，Cursor 比 DeepCode 顺手得多。

**Aider。** 工作模式：CLI + git-aware diff。Aider 把每次修改做成 git commit，让你能 review、revert。它跟 Claude Code 在产品定位上最近，区别是 Aider 更"shell 原教旨主义"——不试图做 IDE，不试图做 Web UI，就一个 REPL。它支持的 LLM 比 Claude Code 多（OpenAI、Anthropic、Gemini、Ollama），但 tool 系统比 Claude Code 弱。

**DeepCode**。和上面三个不一样：DeepCode 不在 *现存* 代码库上工作。它从 *零* 出发——一篇论文 / 一段需求 / 一个 spec 进，一个完整的项目目录出。它有显式的 phase（不是聊天链），它有 plan-then-implement 分离（不是边聊边改），它有 paper-to-code 的特殊机制（document segmentation、requirement analysis）。

```
            从零生成   |   修改现存   |  IDE 集成  | 单文件
DeepCode      ✓      |     △       |    ×       |   ×
Claude Code   △      |     ✓       |    ×       |   △
Cursor        ×      |     ✓       |    ✓       |   ✓
Aider         ×      |     ✓       |    ×       |   △
```

**为什么这件事对你有用？** 三个判断：

1. 你要从论文 / 需求 / spec 起一个新项目 → DeepCode 设计就是为这个。
2. 你要在已有项目上加 feature / 修 bug → Claude Code 或 Cursor。
3. 你要混合（先 DeepCode 起骨架，再 Claude Code 迭代）—— 这是上游团队自己也建议的工作流（README L838 之后的快速开始）。

学习版的位置是 *为了让你看懂* DeepCode 的内部，*不是* 让你用学习版替换 DeepCode 本身。学习版的 s10 跑出来的产物是 `plan_minimal.yaml`（3 个文件，每个文件就一个 stub），不是真的 ImageNet 训练脚本——但是 *机制* 是一样的：plan-then-iterate-with-loop-detection-and-memory-compaction。理解了这套机制，你就能看懂上游 2,300 行 `agent_orchestration_engine.py` 在干什么，而不是被它的体量吓退。

参考阅读：
- README L838-861 —— 上游官方 quickstart
- README L976-1123 —— Nanobot Feishu/Telegram 集成（一种 wrapping 方式）
- 学习版 s_full (`docs/zh/s_full-integration.md`) —— 把 10 章拼起来跑一个 plan
- Appendix B —— 上游源码导读地图，告诉你下一步该读哪个文件

---

### 跨节小结：五个选择如何相互支撑

回看本附录的五节内容会发现它们并不独立。显式协议（§1）只有在不可变上下文（§2）的支持下才能稳定——如果 phase 4 的输出能被 phase 5 偷偷改写，那协议就成了纸面承诺。MCP 边界（§3）让协议的产物可被审计：一行 JSONL 记录"phase 5 调了 read_file，参数是 plan.yaml，返回了 6.3 KB"，事后复盘是机械操作而非考古。而论文到代码的极限（§4）反过来塑造了协议本身：phase 11 不跑训练，是因为算力极限决定了它能验证的只有"代码能 import 吗"。最后，与 Claude Code / Cursor / Aider 的对比（§5）让你看到 DeepCode 的协议为什么不是 *唯一正确* 的设计——它是 *从零生成* 这个特定问题的最优解，而不是所有 AI coding 任务的最优解。

如果要把这五节压成一句话：DeepCode 把"agent 应该做什么"和"agent 怎么做"显式地分开了。前者是 phase 协议 + 不可变 ctx + MCP 边界；后者是 LLM + prompts + tool dispatch。这种分离让 agent 系统具备了软件工程的属性——可被 review、可被测试、可被替换组件。聊天链做不到这件事，因为它把"做什么"和"怎么做"混在同一根历史里。

### 给读完这一章的你

如果你打算用 DeepCode：去 Appendix B，从阅读顺序的第 1 个文件开始，按表读 11 个文件，差不多 8-12 小时——读完之后你不只是 *用* DeepCode，你 *理解* DeepCode。

如果你打算 *写* 一个类似 DeepCode 的系统：把这 5 节再读一遍，然后问自己——你的协议是什么？你的 ctx 是不可变的吗？你的 I/O 边界画在哪里？你的不可复现极限在哪里？你的目标用户和 Claude Code / Cursor / Aider 的用户重叠吗？这五个问题没答清楚之前不要写代码——不然你会在第三个月发现自己把聊天链装在了一堆 phase 名字下面。

如果你只是好奇：上游 README L326-373 的 PaperBench 数字（75.9%）是真的；CHANGELOG.md 里 2026-04-17 那次配置 break（从 .env 折成单 JSON）是真实的设计紧绷；`workflows/agent_orchestration_engine.py` 那 2,312 行是真实的复杂度。多智能体系统的浪漫，是它们真的在做软件工程师以前没法做的事——但浪漫的背后是这五节里讲的不浪漫的工程纪律。这两件事缺一不可。

### 不要把这一章当宣言

最后一句留给一个善意提醒：本附录写的是 *DeepCode 当前的设计选择*，不是 *未来所有 agent 系统的金科玉律*。LLM 能力还在演进——也许两年后 chat chain 也能稳定（因为模型 reasoning 强到不会累积错误）；也许 MCP 协议被另一个标准替换（agent 协议本身在快速演进）；也许 paper-to-code 的图限制会被多模态 LLM 解决。本附录只是告诉你 *这一刻* 一个最先进的 agent 系统在 *这种约束下* 做出的选择，以及那些选择的理由。读完它你会更聪明，但不要因此变得教条。三年后回来重读时，留心哪些选择还成立、哪些已经过时——那才是这一章真正想给你的东西。
