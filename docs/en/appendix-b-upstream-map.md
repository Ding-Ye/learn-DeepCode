---
title: "Appendix B · Upstream source-reading map"
chapter: "appendix-b"
slug: appendix-b-upstream-map
est_read_min: 22
---

# Appendix B · Upstream source-reading map

> The learn-version ports 10 mechanisms; upstream has 200+ Python files. This appendix is the index that points back from the *learn-version* to *upstream* — what to read in what order, which file goes with which session, what to read deeper after each session, five exercises that grow the skeleton into muscle, and a top-level directory tour so you don't drown in an 88 MB repo.

---

## 阅读顺序 / Reading order

The learn-version's 10 sessions are sorted in *dependency topological* order (s01 → s10), and the upstream source is best read in the same order — leaves first (registry, providers, context), then composites (runner, planning, memory), then the top-level conductor (agent_orchestration_engine). The 12 files below cover every learn-version chapter plus 2 stretch files:

| # | Upstream file | Maps to | One-line hint |
|---|---|---|---|
| 1 | `core/agent_runtime/tools/registry.py` | s02 | `ToolRegistry` manages MCP-subprocess lifetimes via `AsyncExitStack`; watch how `_cached_definitions` invalidates on register/unregister. |
| 2 | `core/config.py` | s03 | `DeepCodeConfig` (pydantic-settings) + `${ENV_VAR}` interpolation + the fallback merge in `get_agent_settings(phase)`. |
| 3 | `core/providers/base.py` | s04 | `LLMProvider` ABC, `ProviderSpec` metadata, the canonical `LLMResponse` — the source for every backend's normalization. |
| 4 | `core/providers/anthropic.py` | s04 | First backend implementation: messages API, content blocks → `ToolCallRequest`, retry-on-overload. |
| 5 | `core/providers/openai_compat.py` | s04 | Second backend: chat completions API, `tool_calls` array → the same `ToolCallRequest`, `finish_reason` rename. |
| 6 | `workflows/workflow_context.py` | s05 | The `@dataclass(slots=True)` immutable contract; `to_dir_info()` is a legacy bridge — you'll want to *not* port it. |
| 7 | `core/agent_runtime/runner.py` | s06 | `AgentRunSpec` + `AgentRunner.run` main loop; the learn-version takes only lines 1-400, lines 400-1065 are stretch. |
| 8 | `workflows/planning_runtime.py` | s07 | The five required sections, the atomic-write triad (checkpoint / attempts / meta), `is_existing_plan_usable`. |
| 9 | `utils/loop_detector.py` | s08 | `note_llm_wait()` is the key innovation — subtracts LLM network wait from stall accounting. |
| 10 | `workflows/agents/memory_agent_concise.py` | s09 | The `_COMPACTABLE_TOOLS` allowlist + `should_trigger_memory_optimization` gate + `apply_memory_optimization` execution. |
| 11 | `workflows/code_implementation_workflow.py` | s10 | The file-level implementation loop that fuses the previous six mechanisms into one for-loop; `_last_run_state` is the upstream version of `RunReport`. |
| 12 | `workflows/agent_orchestration_engine.py` | (beyond s_full) | The 2,312-line top-level conductor; the learn-version intentionally does not port this — read it after the 11 files above, otherwise it will scare you off. |

**How to read**: 90 minutes per file. Read once for the author's narrative; read again side-by-side with the learn-version's Go rewrite — Python line maps to Go line; finally read the git blame to understand what the author was actually solving. Each learn-version session README has an `Upstream ref` line — use it to anchor.

## 文件→章节映射 / File-to-session map

The table below extends `research-notes.md`'s file-to-session map with five stretch files. Every path was verified to exist under `/Users/yeding/learn-DeepCode/.learn/upstream/`.

| Upstream file | Session(s) | Key lines | Why this file matters |
|---|---|---|---|
| `core/agent_runtime/tools/registry.py` | s02 | 11-130 | The full `ToolRegistry` class — every tool every agent calls flows through here |
| `core/config.py` | s03 | 1-250 | `DeepCodeConfig` + the entire config tree + `get_agent_settings(phase)` |
| `deepcode_config.json.example` | s03 | 1-130 | Real-world example of the config schema; source of test fixtures |
| `core/providers/base.py` | s04 | 100-250 | `LLMProvider` ABC + `ProviderSpec` + `LLMResponse` |
| `core/providers/anthropic.py` | s04 | 26-200 | First backend: Anthropic Messages API |
| `core/providers/openai_compat.py` | s04 | 1-250 | Second backend: OpenAI Chat Completions |
| `core/providers/registry.py` | s04 (stretch) | 1-150 | Multi-provider registration + keyword-based backend routing |
| `workflows/workflow_context.py` | s05 | 62-120 | The full immutable `WorkflowContext` dataclass |
| `core/agent_runtime/runner.py` | s06 | 69-400 | `AgentRunSpec` + `AgentRunner.run` main loop |
| `core/agent_runtime/runner.py` | s06 (stretch) | 400-1065 | length-recovery, injection, tool-result compaction — out of curriculum scope but very real |
| `workflows/planning_runtime.py` | s07 | 1-263 | The entire file — validation + atomic write + JSONL append |
| `utils/loop_detector.py` | s08 | 12-253 | The entire file — loop / timeout / stall / max-errors |
| `workflows/agents/memory_agent_concise.py` | s09 | 27-300 | Allowlist + trigger logic + apply logic |
| `workflows/code_implementation_workflow.py` | s10 | 41-560 | The orchestration loop body + `_last_run_state` state machine |
| `workflows/agent_orchestration_engine.py` | (beyond s_full) | 80-500 | The top-level phase scheduler — by the time you read this you have absorbed the previous 11 files |
| `tools/code_implementation_server.py` | s10 (stretch) | full file | The real schema of upstream's MCP server: `read_file`, `write_file`, `execute_python`, etc. |
| `core/llm_runtime.py` | s04 (stretch) | full file | `get_workflow_provider(phase, provider_name, model)` glues config resolution to provider instantiation |
| `prompts/code_prompts.py` | (Appendix A) | full file | Each phase's system prompt — read this to see what the phase protocol looks like |
| `tools/command_executor.py` | s10 (stretch) | full file | Cross-platform command execution + path allowlist + timeout — the security brick of the MCP boundary |

## 章节延伸阅读 / Per-session deep dives

After finishing each chapter, here are one or two extension files — out of curriculum scope, but they show what upstream does that the learn-version skipped.

**s01 — minimum-loop**
- `core/providers/anthropic.py:200-end` — retry-on-overload + adaptive max_tokens shrink (87.5% → 95% → 98%). The learn-version's s01 only shows the happy path; this section tells you the first real-production failure mode is 429.
- `core/providers/anthropic.py` overall (~600 lines) — streaming support, `cache_control`, prompt-caching token counting. The learn-version is non-streaming by default; this is where you start if you want to add streaming.

**s02 — tool-registry**
- `tools/code_implementation_server.py` — what an upstream MCP server actually looks like. The `@mcp.tool()` decorator registers a set of tools; `async def main()` runs JSON-RPC over stdin/stdout via `stdio_server()`. Learn-version s02 simulates lifecycle with `io.Closer`; this file shows what real MCP looks like running.
- `core/agent_runtime/mcp_client.py` (if present) — how the client side connects, handshakes, calls, and closes.

**s03 — config-loader**
- `core/llm_runtime.py` — the real entry point for phase-aware provider resolution: `get_workflow_provider(phase=..., provider_name=..., model=...)` reads the config tree and *instantiates* a concrete provider. Learn-version s03 only parses; `llm_runtime.py` is the missing other half.
- `core/providers/registry.py` — the `ProviderSpec` definition and keyword routing (`"claude"` → Anthropic backend, `"gpt"` → OpenAI backend, etc.).

**s04 — provider-abstraction**
- `core/providers/registry.py` — learn-version s04 hard-codes two providers; this file shows how to register N providers, parse keywords, and find env keys.
- `core/llm_runtime.py` — the glue that resolves (phase + provider_name + model) into a concrete provider instance. This is what powers Phase G's multi-model support.

**s05 — workflow-context**
- `workflows/agent_orchestration_engine.py:80-500` — how ctx is *constructed* (phases 0+1). Learn-version s05 has `Prepare`; the upstream construction involves input-source parsing, kind detection, workspace creation, log dir mkdir — a chain.
- Every `ctx.` reference inside `workflows/agent_orchestration_engine.py` — see ctx is *read-only* in vivid evidence: every grep result is a read, never a write.

**s06 — tool-capable-runner**
- `core/agent_runtime/runner.py:400-1065` — the learn-version only ports lines 1-400. The remaining 600 lines are real-production essentials: length-recovery (continuing after `max_tokens` truncation), injection (slipping a user message mid-loop), tool-result compaction (when a single tool result is too big), parallel tool-call handling.
- `core/agent_runtime/messages.py` (if present) — concrete message-validation rules. The learn-version assumes well-formed input; upstream has a validation layer to handle the LLM's occasional malformed output.

**s07 — planning-runtime**
- `workflows/plan_review_runtime.py` — the runtime for phase 5 (plan-review). The learn-version only does phase 4 (planning); plan-review hands the plan to a second LLM to vet, confirming all five sections are mutually consistent before passing.
- The phase 4 → 5 transition logic in `workflows/agent_orchestration_engine.py` — including when review is skipped, when it's forced, and how user intervention enters the loop.

**s08 — loop-detector**
- `utils/progress_tracker.py` — the learn-version's s08 covers ProgressTracker too; in upstream it is its own file complementary to LoopDetector: detector prevents stalls, tracker logs progress.
- `tests/phase9_progress_test.py` — upstream's unit tests for ProgressTracker. Watch how they mock timestamps to simulate the "1 file done → 2 files done" transition.

**s09 — memory-compaction**
- `workflows/agents/memory_agent_concise_index.py` — the index variant of the memory agent. Learn-version s09 is the concise base; the index version also keeps the *generated-code index* in memory so later files can reference earlier files' APIs.
- `workflows/agents/memory_agent_concise_multi.py` — the multi-task parallel variant. Learn-version assumes single task; this file shows how to share/isolate memory across parallel tasks.

**s10 — code-impl-workflow**
- `workflows/agent_orchestration_engine.py:500-1500` — the learn-version's s10 is the equivalent of upstream's phase 6-10; this section of `agent_orchestration_engine.py` is what calls phase 6-10, plus retry, error recovery, and user cancellation.
- `tools/code_implementation_server.py` — the learn-version's s10 uses three inline tools (`read_file`/`write_file`/`execute_python`); upstream's MCP server provides a longer schema: `get_file_structure`, `search_code`, `search_reference_code`, `read_code_mem`, etc. Reading it tells you what the actual tool surface available to the downstream LLM looks like.

## 扩展练习 / Extension exercises

Things the learn-version did not do but you can. Each maps to one chapter, ordered easy → hard.

**Exercise 1: add a third provider (Gemini or Deepseek) to s04.**
Difficulty: medium. Copy `agents/s04-provider-abstraction/openai.go` to `gemini.go` and swap the endpoint, auth header, and JSON schema for Gemini's generateContent API. The hard part is that Gemini uses `functionCall` for tool_use, not `tool_calls` — you need a translation layer in `decode()`. Acceptance: the third provider passes the same five tests as the previous two. Upstream cross-ref: `core/providers/gemini.py` (if present) or `core/providers/openai_compat.py` as a template (many OSS LLMs go OpenAI-compatible).

**Exercise 2: real BPE tokenization for s09.**
Difficulty: hard. The learn-version uses `len(s)/4` as a token proxy; this proxy is accurate on short English but drifts heavily on Chinese / JSON / code. Wire in [tiktoken-go](https://github.com/pkoukk/tiktoken-go), replace the `Tokenizer` interface's `ByteLengthTokenizer` with a `TiktokenTokenizer`, run s09's five tests and measure the compaction-trigger drift. Acceptance: on long JSON input the byte-proxy estimate vs. real BPE token count differs by > 30%. Upstream cross-ref: `workflows/agents/memory_agent_concise.py:50-80`.

**Exercise 3: add a `BeforePlanning` hook in s10.**
Difficulty: medium. Learn-version s10 has no plugin system (deliberately omitted to keep curriculum length down). Add a minimal one: define `Hook interface { Name() string; ShouldTrigger(ctx) bool; Run(ctx) error }`, have `Workflow.Run` fire `BeforePlanning` hooks before the main loop starts. Add one test hook to verify it gets called. The exercise will give you a visceral feel for why upstream's `workflows/plugins/` exists. Upstream cross-ref: `workflows/plugins/base.py:34-150`, `workflows/plugins/integration.py:1-80`.

**Exercise 4: replace s07's `sync.Mutex` with `flock` for cross-process safety.**
Difficulty: medium. The learn-version's s07 `AppendJSONL` uses an in-process mutex; if you ran two Go processes writing to the same JSONL, mutexes would not see each other. Swap mutex for `golang.org/x/sys/unix.Flock` (Linux/Mac) or `LockFileEx` (Windows), run two goroutines simulating two processes, verify the writes do not interleave. This exercise shows you why upstream did not use flock (Python `asyncio` is already cooperative inside one process) — but cross-process safety is still a real problem, especially in daemon deployments. Upstream cross-ref: upstream does not currently use flock; this is a place where the learn-version can *exceed* upstream.

**Exercise 5: wire real MCP stdio in s02.**
Difficulty: hardest. This is real MCP protocol integration: spawn an MCP server subprocess (e.g. `npx @modelcontextprotocol/server-filesystem /tmp`) via `os/exec.Command`, talk to it through stdin/stdout using JSON-RPC 2.0: `initialize` → `tools/list` → `tools/call`. Register every returned tool into s02's `Registry`. Acceptance: in a test you can spawn a server, the registry lists the server-exposed tools, and you can invoke one tool and see the response. Upstream cross-ref: `core/agent_runtime/tools/registry.py:50-90` for how `AsyncExitStack` manages subprocesses; `tools/code_implementation_server.py` for what the server-side schema looks like.

## 上游目录速览 / Upstream directory tour

Top-level tour of `/Users/yeding/learn-DeepCode/.learn/upstream/`. LOAD-BEARING = removing it breaks the system; AUXILIARY = removing it leaves the system functional.

```
DeepCode/
├── core/                      [LOAD-BEARING]
│   ├── providers/             multi-LLM-provider abstraction + registry (source for s04)
│   ├── agent_runtime/         tool-capable agent loop + tool registry + MCP (source for s02, s06)
│   ├── sessions/              JSONL persistent session store (resume support)
│   ├── observability/         structured JSONL logging + LLM-call tracing + event bus
│   ├── compat/                legacy-workflow → modern-agent-runtime compat layer
│   ├── config.py              single-JSON config loader (source for s03)
│   └── llm_runtime.py         workflow-facing LLM helper (phase selection + logging)
│
├── workflows/                 [LOAD-BEARING]
│   ├── agent_orchestration_engine.py   the top-level conductor (~2,312 LOC; s_full intentionally does not port)
│   ├── agents/                          7 specialized agents (source for s09 + extensions)
│   │   ├── memory_agent_concise.py      memory compaction (source for s09)
│   │   ├── memory_agent_concise_index.py memory compaction with code index
│   │   ├── memory_agent_concise_multi.py memory compaction multi-task variant
│   │   ├── code_implementation_agent.py  code implementation agent
│   │   ├── document_segmentation_agent.py long-doc segmentation
│   │   └── requirement_analysis_agent.py  requirement analysis
│   ├── code_implementation_workflow.py  file-level implementation workflow (source for s10)
│   ├── planning_runtime.py              planning runtime + checkpoint (source for s07)
│   ├── plan_review_runtime.py           plan review (phase 5)
│   ├── plugins/                          user-in-loop hook system (source for Appendix B Exercise 3)
│   └── workflow_context.py              immutable WorkflowContext (source for s05)
│
├── tools/                     [LOAD-BEARING]
│   ├── code_implementation_server.py    workhorse MCP server (read/write/exec/search)
│   ├── command_executor.py              safe cross-platform command execution
│   ├── code_reference_indexer.py        semantic code search + graph building
│   ├── code_indexer.py                  reference-repo similarity scoring (B11)
│   └── git_command.py                   git operations
│
├── prompts/                   [LOAD-BEARING] LLM system prompts (Appendix A reading source)
│   └── code_prompts.py        system prompt for each phase
│
├── cli/                       [AUXILIARY]
│   ├── main_cli.py            CLI main entry (--cli mode)
│   ├── cli_app.py             interactive CLI app
│   ├── cli_interface.py       CLI UI interface
│   ├── cli_launcher.py        CLI launcher
│   └── workflows/             simplified workflow used by the CLI
│
├── new_ui/                    [AUXILIARY]
│   ├── backend/               FastAPI REST + WebSocket backend (uvicorn :8000)
│   ├── frontend/              React + Vite frontend (npm run dev :5173)
│   └── scripts/               startup scripts
│
├── ui/                        [AUXILIARY] legacy Streamlit UI (--classic mode)
│
├── schema/                    [AUXILIARY] Pydantic models for API requests/responses
│
├── nanobot/                   [AUXILIARY] Feishu/Telegram chatbot integration
│
├── utils/                     [LOAD-BEARING]
│   ├── loop_detector.py       loop / timeout / stall detection (source for s08)
│   ├── progress_tracker.py    completed-files counter (s08 extension)
│   └── miscellaneous helpers
│
├── tests/                     [AUXILIARY] unit tests (3 files — CI does not run them)
│   ├── phase9_progress_test.py     ProgressTracker + ConciseMemoryAgent unit tests
│   └── ui_session_resume_test.py   SessionStore + WorkflowService resume integration test
│
├── config/                    [AUXILIARY] static config files
│
├── deepcode_docker/           [AUXILIARY] Docker deployment scripts (run_docker.sh)
│
├── deepcode.py                [LOAD-BEARING] top-level launcher (--local / --classic / --cli)
├── deepcode_config.json.example  [LOAD-BEARING] config template (source for s03 fixtures)
├── README.md                  [AUXILIARY] user docs
├── requirements.txt           [LOAD-BEARING] Python dependency list
└── setup.py                   [LOAD-BEARING] PyPI packaging configuration
```

**How to use this map**:

1. To understand *core mechanisms* — only `core/`, `workflows/`, `tools/` matter. These three directories are the entire input to the learn-version.
2. To understand the *user-facing surface* — read `cli/` and `new_ui/`. This is the actual pipeline that takes a paper from PDF to LLM input.
3. To understand *operations* — read `core/sessions/`, `core/observability/`, `deepcode_docker/`. These are key to production deployment.
4. To *change one line and run it* — read `deepcode.py`. Its 800+ lines handle dependency checks, config sanity, and subprocess lifecycle.
5. To *write a feature not in the paper* — read `workflows/plugins/`. The hook system is the only design-blessed extension point that does not require modifying core.

**On reading time**: our experience is that reading every LOAD-BEARING file once takes 8-12 hours (one focused sit-down + 1-2 follow-up passes). After that, the "Upstream Source Reading" section of every learn-version chapter README will feel easy — you'll already know what the author is solving in each file. This map exists to budget those 8-12 hours so you don't blunder around in an 88 MB repo.

Further reading:
- learn-version s_full (`docs/en/s_full-integration.md`) — wire all 10 chapters into a single run
- Appendix A (`docs/en/appendix-a-multi-agent-philosophy.md`) — design philosophy
- Upstream README L838-861 — official upstream quickstart
- Research notes (`/Users/yeding/learn-DeepCode/.learn/research-notes.md`) — detailed dossier of all 12 mechanisms

---

### Useful grep recipes

While reading upstream source, the following grep recipes save time immediately:

```bash
# find every ctx read site (should all be reads, never writes)
cd /Users/yeding/learn-DeepCode/.learn/upstream
grep -rn "ctx\." workflows/ | grep -v "ctx ="

# find each phase's entry point (where the top-level scheduler fires them)
grep -n "phase" workflows/agent_orchestration_engine.py | head -30

# find every MCP tool name
grep -rn "@mcp.tool" tools/

# find every retry-on-overload path on providers
grep -rn "RateLimitError\|overloaded" core/providers/

# find every hook point in the plugin system
grep -rn "InteractionPoint" workflows/plugins/

# find every atomic write
grep -rn "tmp.*rename\|tempfile" workflows/
```

Drop these into your shell history — first read they navigate, second read they diagnose.

### Suggested reading cadence

If you only have one evening: read items 1-3 from the reading order (registry + config + provider/base). These three files cover learn-version s02-s04 and are upstream's entire "infrastructure" tier.

If you have a weekend: 1-7 (add workflow_context, anthropic, openai, runner). These seven files give you upstream's "skeleton" — from config to provider to runner.

If you have a week: 1-11 (all eleven core files). After that you can take on file #12 (agent_orchestration_engine) without being intimidated.

If you have a month: those 12 + 1-2 deep-dive files per chapter + at least two extension exercises. By that point you have the credibility to file a PR upstream.

### A note on path stability

DeepCode is v1.2.0 (May 2026), still beta. `workflows/agent_orchestration_engine.py` has been split twice (pre-v0.8 it was a single `workflow.py`; v1.0 extracted `agent_orchestration_engine.py`; v1.1 extracted `plugins/`). The learn-version is pinned at sha `b9ece6035ea3f3582e6c503c517206b23c09ad09` — that is the source of truth for every path in this appendix. If you read on `main` and a path does not match, first `git checkout b9ece60`, then return here to locate.

CHANGELOG.md has the full list of breaking changes. The most recent large move was 2026-04-17: `.env` file support was removed, all configuration was collapsed into a single `deepcode_config.json`. That change made s03's design map cleanly — single-JSON is the present truth, not a historical accident.

### Extensions outside this appendix

The learn-version covers up to s10 + s_full. If a future s11 were added, the natural topic would be plugins (`workflows/plugins/base.py`) — the hook system in Go is roughly 4-5 interfaces + one registry, about 300 LOC, fitting exactly into a chapter budget. The s12 candidate is sessions (`core/sessions/`) — JSONL session store + resume logic, about 400 LOC. Neither is in v1 of the curriculum: the v1 arc is "from minimum loop to paper-to-code"; adding them would dilute the theme. If you finish v1 and want more, treat both as self-study — the extension exercises in this appendix already cover half the ground.

Finally, the research notes (`/Users/yeding/learn-DeepCode/.learn/research-notes.md`) contain the full 12-mechanism dossier, a 15-word glossary, and 10 anti-patterns — this appendix compresses them into "which paths to read," but the full version has more *why* detail. If you plan to use this appendix as a frequently-revisited bookmark, those research notes are the other companion you should finish reading.
