---
title: "s_full · End-to-end integration"
chapter: full
slug: s_full-integration
est_read_min: 18
---

# s_full · End-to-end integration

> Ten chapters in, you have hand-written a provider abstraction, a registry, a runner, a loop detector, a memory agent, and a workflow — but each is its own island. This chapter writes zero new Go code. It welds them into a single mental model: how one user request crosses five architectural bands and lands as a directory tree of generated source on disk.

---

## 全栈架构图 / Full-stack architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│ L1  Workflow                                            s10              │
│     CodeImplementationWorkflow.Run(ctx, planPath, taskDir)               │
│     ── reads plan → per-file loop → JSONL row → atomic RunReport         │
└──────────────────────────────────────────────────────────────────────────┘
            │ build RunSpec per file                    ▲ RunReport
            ▼                                           │
┌──────────────────────────────────────────────────────────────────────────┐
│ L2  Runner + Registry                                   s06 + s02        │
│     Runner.Run(ctx, RunSpec) → for ChatRequest → dispatch tool → repeat  │
│     Registry: name→Tool map + schema cache + Close lifecycle             │
└──────────────────────────────────────────────────────────────────────────┘
            │ ChatRequest{Messages, Tools, Model}        ▲ ChatResponse
            ▼                                            │
┌──────────────────────────────────────────────────────────────────────────┐
│ L3  Provider                                            s04              │
│     Provider.Chat(ctx, ChatRequest) (ChatResponse, error)                │
│     AnthropicProvider | OpenAIProvider — translate per-vendor wire       │
└──────────────────────────────────────────────────────────────────────────┘
            │ HTTPS                                      ▲ JSON
            ▼                                            │
┌──────────────────────────────────────────────────────────────────────────┐
│ L4  Config + LoopDetector + Memory                  s03 + s08 + s09     │
│     Config: single JSON + ${ENV} interpolation + phase merge → Settings  │
│     LoopDetector: CheckTool / NoteLLMWait / RecordError                  │
│     MemoryAgent: Compact(messages) → messages' (clean-slate strategy)    │
└──────────────────────────────────────────────────────────────────────────┘
            │ inject into Provider/Runner/Workflow      ▲ feedback
            ▼                                            │
┌──────────────────────────────────────────────────────────────────────────┐
│ L5  Context + Planning                                  s05 + s07        │
│     WorkflowContext{TaskID, InputSource, InputKind, TaskDir} (immutable) │
│     PlanningRuntime: ValidatePlanText + AtomicWriteJSON + AppendJSONL    │
└──────────────────────────────────────────────────────────────────────────┘
```

**Path of one user request**: user types at the CLI → `WorkflowContext.Prepare` decides `InputKind` (s05) → planning runtime writes the plan to `taskDir/planning_*.json` (s07) → workflow reads the plan, builds per-file messages (s10) → runner calls the provider, dispatches tools (s06+s02) → provider translates to Anthropic or OpenAI wire format and POSTs (s04) → at every step the LoopDetector gates calls and after every successful `write_file` the MemoryAgent compacts history (s08+s09) → the entire chain reads from a single Config (s03). The return path runs in reverse: JSON → ChatResponse → tool dispatch → file written → JSONL appended → RunReport.

**What each band owns**:

- **L1 Workflow (s10)** is the **only** layer that knows the *shape of the task*. It reads `plan.yaml`, decides which files to write, in which order, and what to report when done. L1 never calls an LLM directly — it wraps each file in a `RunSpec` and hands it down to L2.
- **L2 Runner+Registry (s06+s02)** runs the loop for one agent task. The runner does not care whether the task is "write a file" or "answer a question"; it cares only about the rhythm "prompt → tool_use → dispatch → result back → repeat → final text". The registry is the directory of tools; the runner is the dispatcher.
- **L3 Provider (s04)** translates the canonical `ChatRequest` into Anthropic's `messages[]` or OpenAI's `messages[]+tool_calls[]` and translates the reply back into the canonical `ChatResponse`. L3 is the only layer that touches HTTP.
- **L4 Config + LoopDetector + Memory (s03+s08+s09)** is the **cross-cutting concerns** band. Config injects at startup; the loop detector gates before each tool dispatch; the memory agent compacts after each successful `write_file`. None of the three is part of the main flow — but without them the main flow runs away, blows the token budget, or reads from the wrong model.
- **L5 Context + Planning (s05+s07)** is the **state band**. WorkflowContext is the task's "who am I" — an immutable value every phase reads but never writes. PlanningRuntime is the task's "what have I done" — atomic write + JSONL append guarantee that any restart can pick up from a known checkpoint.

Dependencies between bands point one way: upper bands reference lower bands, lower bands have no idea the upper ones exist. That is exactly why s05 and s07 can be tested without an LLM, why s06 can be tested without a task shape, and why s10 is the chapter that "must drag everyone into one room before it can run".

**Contrast with upstream**: upstream's `workflows/agent_orchestration_engine.py` fuses L1 + L4 + L5 into one 2,312-line conductor — the upside is "it's all in one place"; the downside is that tweaking the LoopDetector's stall threshold means first decoding 11 phases of callback chains. learn-DeepCode separates "mechanism" from "orchestration": each band is one mechanism, and s10 is the only "orchestration" site. That boundary is exactly why Appendix B's stretch exercises (add a plugin hook, swap in a real BPE tokenizer, wire real MCP) can land without touching s10.

**Contrast with the anti-patterns**: the 10 upstream anti-patterns called out in research-notes map one-to-one onto the bands — `global state` (#1) is dissolved at L4 by explicit Config injection; `mixed async/sync` (#2) is unified across the book by `context.Context`; `stringly-typed config` (#5) is solved at L5 by the value-type `WorkflowContext`; `callback overload` (#6) is replaced at L1 by a typed `RunReport` return value; `hardcoded LoopDetector thresholds` (#10) become configurable fields at L4. Internalising this diagram is the same as inoculating yourself against all 10 of upstream's footguns.

**Why five bands, not seven**: earlier drafts gave LoopDetector its own band and Memory its own band, but by the time s10 was written it became clear those two play the same role in orchestration — "cross-cutting concerns that hook into the main flow". Folding them into L4 makes s10's code read like a 5-layer sandwich instead of a 7-layer onion. That "split by mechanism first, merge by dependency second" pattern is exactly what learn-DeepCode does throughout.

## 16 步执行轨迹 / 16-step execution trace

Take the README A3 Text2Backend example as input:

> User: **"Build a REST API for a todo app with FastAPI, PostgreSQL, and JWT auth."**

Below are the 16 steps this request takes through learn-DeepCode. Each step labels the **actor** (who acts), the **action** (what they do), and the **Go implementation site** (which chapter of learn-DeepCode owns the corresponding code).

| # | actor | action | learn-DeepCode site |
|---|-------|--------|---------------------|
| 1 | User | types the requirement (or pastes a paper) at the CLI | `cmd/learn-deepcode/main.go` (doc stub) |
| 2 | CLI | parses argv, decides `taskDir` and `planPath` | `cmd/learn-deepcode/main.go` (doc stub) |
| 3 | CLI | calls `WorkflowContext.Prepare(input, opts)` to build the immutable context | [`s05-workflow-context`](./s05-workflow-context.md) |
| 4 | CLI | loads `deepcode_config.json`, expands env refs, resolves the phase | [`s03-config-loader`](./s03-config-loader.md) |
| 5 | Orchestrator | constructs the planner provider via `NewProviderFromConfig(cfg, "planning")` | [`s04-provider-abstraction`](./s04-provider-abstraction.md) |
| 6 | Orchestrator | builds the planner system prompt + user message (todo-app requirement) | [`s05-workflow-context`](./s05-workflow-context.md) + [`s07-planning-runtime`](./s07-planning-runtime.md) |
| 7 | Planner LLM | one `Provider.Chat` returns a YAML plan (with the 5 required sections) | [`s04-provider-abstraction`](./s04-provider-abstraction.md) |
| 8 | Orchestrator | `ValidatePlanText(text)` checks all 5 required sections are present | [`s07-planning-runtime`](./s07-planning-runtime.md) |
| 9 | Orchestrator | `AtomicWriteJSON(taskDir/planning_checkpoint.json)` + `AppendJSONL(planning_attempts.jsonl)` | [`s07-planning-runtime`](./s07-planning-runtime.md) |
| 10 | Orchestrator | persists the plan to `taskDir/plan.yaml` (s10's input) | [`s07-planning-runtime`](./s07-planning-runtime.md) + [`s10-code-impl-workflow`](./s10-code-impl-workflow.md) |
| 11 | Workflow | `CodeImplementationWorkflow.Run(ctx, planPath, taskDir)` entry point | [`s10-code-impl-workflow`](./s10-code-impl-workflow.md) |
| 12 | Workflow | reads the plan, iterates `["main.py", "models.py", "routes.py", "auth.py", ...]` | [`s10-code-impl-workflow`](./s10-code-impl-workflow.md) + [`s07-planning-runtime`](./s07-planning-runtime.md) |
| 13 | Runner | per file, builds a `RunSpec` and calls `Runner.Run(ctx, spec)` | [`s06-tool-capable-runner`](./s06-tool-capable-runner.md) |
| 14 | Runner | inner loop: `Provider.Chat` → tool_calls → `LoopDetector.CheckTool` gate → `Registry.Get(name).Run` dispatch → result fed back | [`s06-tool-capable-runner`](./s06-tool-capable-runner.md) + [`s02-tool-registry`](./s02-tool-registry.md) + [`s08-loop-detector`](./s08-loop-detector.md) |
| 15 | Runner | after a successful `write_file` the `OnToolResult` hook fires `MemoryAgent.Compact(messages)` | [`s09-memory-compaction`](./s09-memory-compaction.md) |
| 16 | Workflow | writes `implementation_attempts.jsonl` + atomic-writes `implementation_report.json`, returns `RunReport` | [`s10-code-impl-workflow`](./s10-code-impl-workflow.md) + [`s07-planning-runtime`](./s07-planning-runtime.md) |

**Steps 1-4** are the entry-point glue — argv parsing, context construction, config loading. Three chapters cover them: s05 owns "who am I", s03 owns "which model do I use", and the remaining argv parsing lives in `cmd/learn-deepcode/main.go`, a doc stub of ~30 lines that wires s05 + s10 together.

**Steps 5-10** are the **planning phase** — one synchronous LLM call yields the entire YAML plan, then two atomic artefacts (checkpoint + attempts log) hit disk. This stretch is s05 + s07 territory. `ValidatePlanText` checks that all five sections (`file_structure / implementation_components / validation_approach / environment_setup / implementation_strategy`) are present; missing one is a fail.

**Steps 11-16** are the **implementation phase** — s10's stage. One `Runner.Run` per file; inside the runner loop, s02 (registry dispatch) + s06 (loop skeleton) + s08 (CheckTool gate) + s09 (Compact after write_file) cooperate. After each file we append one JSONL line; when the iteration finishes we atomically write the final RunReport.

Observations:

- Planning is **one** LLM call producing the entire plan; implementation is **one runner loop per file**.
- All tool calls go through the registry — the registry is the single dispatch site, which is exactly why the LoopDetector only needs to instrument one location.
- All state lives on disk: `planning_checkpoint.json` + `planning_attempts.jsonl` + `implementation_attempts.jsonl` + `implementation_report.json`. SIGKILL at any moment recovers from disk.
- **None** of the 16 steps crosses a process boundary. The whole chain is one Go process with a single `context.Context` weaving through it. Upstream uses async/await to make the same chain *look* distributed, but it is also single-process — the learn version just drops that pretence.
- The planner LLM call at step 7 and the runner LLM calls at step 14 **both go through the same s04 `Provider.Chat`**; the only difference is the phase (`planning` vs `implementation`), and s03's phase merge lets them choose different models. Switching models is a one-line JSON change.
- Step 14's inner loop runs **2-4 iterations per file** on average — one `read_file` to gather context, one `write_file` to lay down code, optionally one `execute_python` to smoke-test. The runner only cares about "are there tool_calls left?"; the per-file iteration count is emergent.
- Step 15's `OnToolResult` is the hook that pulls in all of s09 — upstream wires it through three callbacks (`record_tool_result + should_trigger_memory_optimization + apply_memory_optimization`); the learn version uses a single closure that captures the file-scoped `messages` local and does a read-modify-write inline.
- Step 16's `RunReport` is the **only** return value — upstream stores the same fields on `_last_run_state: dict[str, Any]` on the workflow instance; the learn version returns a value so the caller can `switch` on `Status`. That is the fix for anti-pattern #5 (stringly-typed `dict`).

## 一条命令跑通 / One command to run

```bash
go run ./agents/s10-code-impl-workflow -plan testdata/plan_minimal.yaml -task-dir /tmp/learn-deepcode-task
```

This single command exercises at least one path through every chapter's code. Unrolling it:

1. **argv parsing** — `cmd/learn-deepcode/main.go`-style entry (s10 ships its own `main.go`), reads `-plan` and `-task-dir`.
2. **Build a ReplayProvider** — the demo defaults to replaying `testdata/replay_three_files.json` so no real API key is needed. If you want to hit a live API, swap in s04's `AnthropicProvider`.
3. **Load the plan** — `LoadPlan(planPath)` reads `{"files": ["main.go", "config.go", "handler.go"]}` (s10 + the spirit of s07's ValidatePlanText).
4. **Construct the Workflow** — `Workflow{Provider, Tokenizer, ...}` already has s06's Runner, s08's LoopDetector, and s09's MemoryAgent embedded.
5. **Run `Workflow.Run(ctx, planPath, taskDir)`** — enters s10's per-file loop: build messages → runner.Run → inner s06 loop → registry dispatches read_file/write_file (s02 in spirit) → each tool_call passes through s08's CheckTool → after a successful write_file s09 Compact runs → one JSONL line is appended (s07 primitive).
6. **Write the RunReport** — atomic write of `implementation_report.json` (s07 primitive); return `RunReport{Status:"completed", FilesCompleted:3, Total:3, ...}`.

Expected stdout (about 8 lines):

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

Expected artefacts under `task_dir`:

```
/tmp/learn-deepcode-task/
├── generate_code/
│   ├── main.go
│   ├── config.go
│   └── handler.go
├── implementation_attempts.jsonl   # 3 JSON lines, one per file
└── implementation_report.json      # final RunReport, atomic
```

Each JSONL line looks roughly like:

```json
{"file":"main.go","timestamp":"2026-05-10T10:00:00Z","stop_reason":"done","iterations":2}
```

That single command verifies, all at once: s10's workflow orchestration is correct; s07's atomic writes do not corrupt existing files; s06's loop terminates deterministically under a ReplayProvider; s02's registry dispatches read_file/write_file to the right Tool; s08's LoopDetector does not fire false positives on a clean run; s09's Compact runs exactly once per write_file.

**Hermetic-first**: the command above defaults to a ReplayProvider — zero network, zero API key, zero token billing. That is the testing discipline of the entire book: every chapter's `go test ./...` runs on a plane, on a subway, behind a corporate firewall. To switch to a real Anthropic provider, replace `&ReplayProvider{...}` in `main.go` with `&AnthropicProvider{APIKey: os.Getenv("ANTHROPIC_API_KEY")}`; the runner / workflow code does not change a line — that single substitution is the entire payoff of s04's `Provider` interface.

**Second command (live)**:

```bash
ANTHROPIC_API_KEY=sk-ant-... go run ./agents/s10-code-impl-workflow \
    -plan testdata/plan_minimal.yaml \
    -task-dir /tmp/learn-deepcode-live \
    -live
```

This actually calls `claude-sonnet-4-5` three times (once per file) at a cost of cents. While it runs, `tail -f /tmp/learn-deepcode-live/implementation_attempts.jsonl` shows attempt rows scrolling past — the lowest-cost way to make "code generation" stop feeling abstract and start feeling observable.

## Deliberate omissions

learn-DeepCode deliberately does not port several upstream features. The table below puts on the record **what is not taught** — so when readers reuse the book's code in production, they know which puzzle pieces are missing and where to add them.

| Feature | Where it lives upstream | Why omitted from learn-DeepCode | Where it could be added |
|---------|------------------------|--------------------------------|-------------------------|
| Streaming SSE responses | the `stream=True` branch in `core/providers/anthropic.py` | s01/s04 chose one-shot `messages.create` to keep wire format inspectable; SSE parsing (`event:`/`data:`) would double s01's size | add a `ChatStream` method to s04's `AnthropicProvider` with a separate test suite |
| Prompt caching | `cache_control` fields in `core/providers/anthropic.py` | the learn version re-sends the system prompt every time — keeps wire-format review trivial; upstream caches system + plan with `ephemeral` | extend s04's ChatRequest with `CacheBreakpoints []int`, AnthropicProvider emits `cache_control: {"type":"ephemeral"}` |
| Multi-process orchestration | `workflows/agent_orchestration_engine.py` (2,312 lines) | half of that 11-phase pipeline is glue code; s10 picks the hardest phase (implementation) and that is enough to convey the model | add s11 "orchestration" on top of s10, threading doc-segmentation → req-analysis → planning → impl |
| Plugin hooks (BEFORE/AFTER) | `InteractionPoint` in `workflows/plugins/base.py` | the hook system is another "Pythonic anti-pattern" (callback overload); the Go-shaped equivalent is channels + structs, left to Appendix B exercises | s10's Workflow gains a `Hooks []func(Phase, *State)` slice |
| Document segmentation | `workflows/agents/` segmentation logic | learn version only ingests text/JSON plans; PDF chunking (>50K chars) is not on the critical path | a separate s12, perhaps using `gopkg.in/yaml.v3` for plans and `unidoc/unipdf` for PDFs |
| OpenRouter routing | `core/providers/registry.py` plus `~/.deepcode/cache/openrouter_models.json` | learn teaches only the two canonical paths (native Anthropic + OpenAI-compat); third-party routing is a config concern, not a mechanism | add an `openrouter` keyword branch to s04's factory |
| MCP stdio framing | `AsyncExitStack` lifecycle in `core/agent_runtime/tools/registry.py` | real MCP needs JSON-RPC 2.0 framing + `os/exec` subprocesses; s02 simulates with `io.Closer`, which is enough to teach lifecycle | Appendix B exercise #5: write an MCP stdio client with `os/exec` + `bufio.Scanner` |
| Retries with shrinking budget | `chat_with_retry` in `core/providers/base.py` (87.5% → 95% → 98%) | adaptive token budgeting is production hardening, not a teachable mechanism; s06 marks it "out of scope" | wrap s04's `AnthropicProvider` in a `RetryProvider{Inner Provider, Budget []float64}` |
| Code reference indexer (B11) | `FileRelationship` in `tools/code_indexer.py` | LLM-driven reference-repo similarity scoring is orthogonal preprocessing, not on the paper2code main path | a separate appendix or s13; emit JSON consumed by s10's plan |
| Observability / tracing | the entire `core/observability/` directory | the learn version is fine with stdlib `log/slog`; upstream's LLM call tracing + event bus is enterprise deployment territory | s10's Workflow gains `Logger *slog.Logger`; runner emits a record before each ChatRequest |
| Session resumption | `core/sessions/` JSONL session store | s10 already does per-file resume via `os.Stat` — covers the most common crash-recovery case | layer `SessionStore.Load(taskID)` on top of s07, replaying JSONL |
| WebSocket progress streaming | `new_ui/backend/services/workflow_service.py` | the learn version is CLI; pushing progress to a frontend is out of scope | s10's Workflow exposes `Progress chan ProgressEvent`, the CLI consumes and prints |

**How to read this table**: each row is a "puddle you may step in if you deploy the book's code straight to production". Column 2 shows how upstream handles it, column 3 explains why the learn version dropped it, and column 4 gives the smallest entry point at which to **add it back** — down to the file and the field to add. In other words, the table is both a disclaimer and a v2 roadmap. The two highest-priority items are usually streaming (UX) + retries (production stability); the rest depend on the deployment context.

**Relationship to upstream anti-patterns**: of the 12 rows, four (streaming / caching / retries / observability) are "upstream got these right and the learn version skipped them" — those are production hardening; another four (multi-process orchestration / plugin hooks / OpenRouter / WebSocket) are "upstream has them but in a shape research-notes flags as an anti-pattern"; the remaining four (document segmentation / MCP stdio / code indexer / session resumption) are "orthogonal features that deserve their own chapter". Use that three-way split to decide which to add back first.

## 跨章节回顾 / Cross-chapter recap

One sentence per chapter; reading these 12 bullets in order is the same as restating the book's mental model. Each bullet gives three things: **mechanism** (what the chapter teaches), **upstream file** (where the corresponding Python lives), and **you can now reason about** (the new inference ability you gain by reading it).

- **Appendix A · multi-agent orchestration philosophy** — teaches "why explicit agent protocols beat chatbot chains", "how immutable context becomes a contract", "why MCP is the I/O boundary", and "the physical limits of paper-to-code reproducibility". Maps to the five "Mental-model topics" in research-notes. **You can now reason about** the design philosophy underlying the 5-band architecture.
- **s01 · minimum-loop** — teaches the wire-format heartbeat: one HTTP POST, one JSON request, one JSON response. Maps to a stripped `core/providers/anthropic.py:26-150`. **You can now reason about** the byte stream of an Anthropic Messages API call without an SDK.
- **s02 · tool-registry** — teaches the `name → Tool` directory + schema cache + `Close()` lifecycle. Maps to `core/agent_runtime/tools/registry.py:11-130`. **You can now reason about** why the registry must own MCP subprocess teardown.
- **s03 · config-loader** — teaches single-JSON config + `${ENV}` interpolation + per-phase merge. Maps to `core/config.py:1-250`. **You can now reason about** why "one config file + phase merge" is more maintainable than 12 environment variables.
- **s04 · provider-abstraction** — teaches the `Provider` interface + Anthropic/OpenAI dual implementation + content-block translation. Maps to `core/providers/base.py + anthropic.py + openai_compat.py`. **You can now reason about** why a canonical `ChatResponse` must normalise finish-reason to `stop|tool_calls|length|error`.
- **s05 · workflow-context** — teaches an immutable task-state value + path-deriving methods. Maps to `workflows/workflow_context.py:1-168`. **You can now reason about** why threading one `WorkflowContext` value through 11 phases is safer than passing dicts.
- **s06 · tool-capable-runner** — teaches the three-branch loop (tool_use / final-text / max_iterations) + `IsError` tool_result + truncation policy. Maps to `core/agent_runtime/runner.py:69-400`. **You can now reason about** why a tool failure should not become a runner failure.
- **s07 · planning-runtime** — teaches atomic write (tmp+rename) + JSONL append + 5-section plan validation. Maps to `workflows/planning_runtime.py:1-263`. **You can now reason about** why atomic write is the cheapest crash-safety primitive.
- **s08 · loop-detector** — teaches repeat detection + wall-clock timeout + `NoteLLMWait` offset. Maps to `utils/loop_detector.py:12-253`. **You can now reason about** why stall detection must subtract LLM network wait time, otherwise false positives explode.
- **s09 · memory-compaction** — teaches clean-slate compaction: keep system prompt + initial plan + the essential-tool blocks since the last `write_file`. Maps to `workflows/agents/memory_agent_concise.py:27-300`. **You can now reason about** why "truncate in the middle" breaks tool_use/tool_result pairing while "clean-slate" does not.
- **s10 · code-impl-workflow** — teaches how to compose s02+s06+s07+s08+s09 into a file-by-file workflow + RunReport. Maps to `workflows/code_implementation_workflow.py:41-500`. **You can now reason about** why all five mechanisms must be in place to materialise a directory of generated code.
- **s_full · integration (this chapter)** — teaches the 5-band architecture + the 16-step trace + 8-12 deliberate omissions. No new code. **You can now reason about** every chapter's mapping back to the upstream artefact, and what is still missing for production deployment.

**Self-check questions (answer them before moving on)**:

1. One `Workflow.Run` over 3 files: how many times does `Provider.Chat` get called? Which chapter has the answer?
2. If you forget to call `LoopDetector.NoteLLMWait(d)`, what false positives appear and which band does that affect?
3. If you skip s09's Compact entirely, after how many files does the runner blow the context window?
4. `WorkflowContext` is a value type, not a pointer — why is that a design choice, not a style preference?
5. s07's atomic write uses tmp+rename; could you replace it with `os.WriteFile` overwriting in place? Why not?

Each question pins down a skill the prior 12 bullets gave you — if you cannot answer one, re-read the "How It Works" section of the corresponding chapter.

## 阅读延伸 / Further reading

Five highest-priority next steps after finishing the book:

1. **DeepCode upstream README + README_ZH** — `https://github.com/HKUDS/DeepCode/blob/main/README.md` (English) and `README_ZH.md` (Chinese). The book deliberately did not paraphrase these — they are already well written; prioritise the "Architecture" and "Quickstart" sections and compare them to the 5-band diagram in this chapter.
2. **`/Users/yeding/learn-DeepCode/.learn/research-notes.md`** — the original evidence dossier for every chapter. Every upstream line-number reference, every anti-pattern case, every mechanism's location lives here. If you plan to add a chapter (say s11 orchestration), read this first.
3. **`/Users/yeding/learn-DeepCode/.learn/plan.md`** — the curriculum blueprint. Includes the shared types catalogue (the 10 canonical Go interfaces and where they reappear), the per-chapter dependency graph, and risks/open questions. **Especially recommended**: re-read the "Per-session detail" section — you will see the "why this slice and not that one" arguments fresh.
4. **Anthropic Messages API reference** — `https://docs.anthropic.com/en/api/messages`. The wire format that s01 + s04 + s06 lean on, and the only authoritative spec. Pay particular attention to the `tool_use` and `tool_result` block schemas, the values of `stop_reason`, and the semantics of `cache_control` — these are the details the learn version sidesteps when it skips streaming/caching.
5. **MCP (Model Context Protocol) specification** — `https://modelcontextprotocol.io/specification`. s02's `io.Closer` simulation of an MCP subprocess is shorthand for JSON-RPC 2.0 over stdio. Appendix B's stretch exercise #5 builds a real MCP client straight from this spec.

Bonus: upstream's `workflows/agent_orchestration_engine.py` (2,312 lines) is the layer the book deliberately **did not** teach. If you intend to extend learn-DeepCode into s01 + ... + s10 + s11(orchestration), it is the next required reading; the annotated extract at [`upstream-readings/s10-code-impl-workflow.py`](../../upstream-readings/s10-code-impl-workflow.py) is a good entry point — start at the parts outside `_check_file_tree_exists`.

**Side-by-side reading habit**: every time you read a snippet of upstream Python, open the corresponding Go chapter alongside it — the same `for iteration in range(spec.max_iterations)` is `for i := 0; i < spec.MaxIterations; i++` in s06; the same `if response["tool_calls"]` is `if len(resp.ToolCalls) > 0` in s06; the same `loop_status["should_stop"]` is `if status.ShouldStop` in s10. Thirty minutes of side-by-side grep beats two hours of one-sided linear reading.

**One last note**: every chapter's code is MIT-licensed; fork it, vendor selected chapters into your codebase, rewrite to match your team's idiom — the point of learn-DeepCode is to teach the reader to build an agent platform "small enough to fully reason about", not to drop into any production system unchanged.

---

That closes the 12-chapter arc of learn-DeepCode — 10 Go code chapters + 1 integration chapter (this one) + Appendices A/B. Recommended next moves: run `go test ./...` to confirm every chapter is green; or open `agents/s10-code-impl-workflow/main.go`, swap in a different `replay_three_files.json`, and watch the workflow take a different path through the same code.

If you want to take this book "one step further", the three highest-signal directions are: (1) add streaming to s04 (closest to user experience); (2) add retries-with-shrinking-budget to s06 (closest to production stability); (3) build s11 orchestration on top of s10, threading doc-segmentation → req-analysis → planning → impl to recover upstream's 11-phase pipeline in Go. After those three, learn-DeepCode is one streaming UI away from being a drop-in upstream replacement.

**Closing thought**: every chapter, every diagram, every step of the 16-step trace exists to do one thing — make sure that the next time you write an agent system, **the model in your head is no longer a black box**. You have read, changed, and tested every line of Go in this book; you know where every JSON file on disk comes from and where it goes. That openness is what "open agentic coding" actually means.

Happy hacking.
