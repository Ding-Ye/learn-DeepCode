---
title: "Appendix A · Multi-agent orchestration philosophy & paper-to-code reproducibility"
chapter: "appendix-a"
slug: appendix-a-multi-agent-philosophy
est_read_min: 18
---

# Appendix A · Multi-agent orchestration philosophy & paper-to-code reproducibility

> This is the *why* chapter. No new Go code. The previous ten sessions ported DeepCode's mechanisms one at a time — s01 is the irreducible request/response, s10 is the file-by-file implementation workflow. After ten chapters, though, you still owe yourself five questions: why did upstream design it this way? Why is it not one chat chain? Why is the workflow context immutable? Why does every I/O go through MCP? Why are some parts of paper-to-code fundamentally not automatable? This appendix answers those five.

---

## 显式智能体协议 vs. 聊天链 / Explicit agent protocols vs. chat chains

DeepCode's biggest architectural choice lives in the phase enum at the top of `workflows/agent_orchestration_engine.py` (2,312 lines): 0=env, 1=input-norm, 2=doc-seg, 3=req-analysis, 4=planning, 5=plan-review, 6-10=impl, 11=finalize. Every phase has its own prompt (in `prompts/code_prompts.py`), its own input contract (the artifact produced by the previous phase), its own output contract (some JSON or YAML written to disk). This is not a chat chain. This is a *protocol*.

Why not a chat chain? Because chat chains have one unfixable failure mode: when the LLM gets it wrong on round 3, round 4 cannot see the wrong, because round 4 has no idea what "right" looks like. A chat chain's state lives entirely in history; once errors accumulate there's no anchor to rewind to. Explicit protocols solve this with immediate validation — `workflows/planning_runtime.py`'s `validate_plan_text` checks for five required sections, and if the schema misses, the entire phase reruns instead of stuffing the bad output into the next prompt's history. The learn-version's s07 ports this exact 5-section validation — not because it is fun, but because LLM output without a schema is fog.

```
chat chain:        explicit protocol (DeepCode):
┌─────┐            ┌──────────┐
│ Q1  │            │ phase 0  │ env  → ctx.WorkspaceRoot
│ A1  │            └────┬─────┘
│ Q2  │                 ▼
│ A2  │            ┌──────────┐
│ Q3  │            │ phase 1  │ input → ctx.InputKind
│ A3  │            └────┬─────┘
│ ... │                 ▼
└─────┘            ┌──────────┐
                   │ phase 2  │ doc-seg → segments[]
                   └────┬─────┘
                   ...
                   ┌──────────┐
                   │ phase 4  │ planning → plan.yaml (validated)
                   └──────────┘
```

Explicit protocols have a second benefit: every phase can swap its LLM independently. `core/llm_runtime.py`'s `get_workflow_provider(phase=..., provider_name=..., model=...)` lets you point phase 4 at GPT-5 and phase 6-10 at Claude Sonnet — because each phase's I/O is a contract, not history, models can be mixed and matched. A chat chain cannot do that, because a chat chain's history is the accumulated identity of one model.

In the learn-version, s10 is the concentrated form of this idea: the plan is phase 4's output, and s10 only reads the plan — it does not read how the plan was made. It can even chew on a hand-written `plan_minimal.yaml`, because s10 trusts contracts, not provenance. That is the ultimate benefit of explicit protocols: upstream and downstream are decoupled so completely that the downstream does not even need to know the upstream exists.

Further reading:
- `workflows/agent_orchestration_engine.py:80-500` — the phase scheduler entry point
- `prompts/code_prompts.py` — the system prompt for each phase
- learn-version s10 (`docs/en/s10-code-impl-workflow.md`) — Go rewrite of phases 6-10

## 不可变上下文是契约 / Immutable context is a contract

What gets passed between phases? Upstream's answer is the `WorkflowContext` at `workflows/workflow_context.py:62-120` — a `@dataclass(slots=True)` immutable struct. task_id, input_source, input_kind, workspace_root, task_dir, paper_path, standardized_text are all fields, all paths are `pathlib.Path` (absolute). No `set_*` method; no one can mutate it.

This is not Python style purity; it is a way to kill an entire class of real-world bug. In early DeepCode commits, phases passed `dir_info: dict[str, Any]` between each other — a string-keyed dict that any phase could add a key to or read a key from. The result: phase 5 reads a key phase 3 never wrote, KeyError ships randomly to production; some callback sets `paper_path = None`, every downstream phase crashes together. Anti-pattern #5 in the research notes named this directly: stringly-typed config is a time bomb.

```
mutable dict[str, Any]:              immutable WorkflowContext:
┌──────────────────┐                ┌────────────────────┐
│ phase 1: ctx[X]=1│                │ phase 1: derive ctx│
│ phase 2: ctx[X]=2│ ←── mutation   │   pass to phase 2  │
│ phase 3: del X   │     race       │ phase 2: derive    │
│ phase 4: KeyError│                │   new value, NOT   │
└──────────────────┘                │   mutate parent    │
                                    └────────────────────┘
```

Why is it a contract? Because immutability equals "I promise what I see is what the upstream produced — no one snuck in to change it." Phase 4 (planning) gets ctx, writes plan.yaml; phase 5 (plan-review) gets the *same* ctx, reads plan.yaml; if ctx had been mutated in between, the two phases would not be looking at the same world. Immutability writes that invariant into the type system itself.

The learn-version's s05 ports this faithfully: `WorkflowContext` is a Go struct passed by value, no pointer-receiver methods, no setters. Go has no `frozen=True` keyword, but pass-by-value + unexported fields + derived path methods give the same guarantee. Anti-pattern #4 (`string` paths mixed with `pathlib.Path`) is fixed at the same time: every path in s05 is a `string`, but only via `filepath.Join`, and always absolute.

Further reading:
- `workflows/workflow_context.py:1-168` — the entire file
- learn-version s05 (`docs/en/s05-workflow-context.md`) — the immutable Go struct
- s07 (`docs/en/s07-planning-runtime.md`) — the first session that actually consumes `ctx.TaskDir`

## MCP 作为 I/O 边界 / MCP as the I/O boundary

DeepCode does not let the LLM call `os.write` or `subprocess.run` directly. Every side effect goes through MCP (Model Context Protocol): a stdio subprocess, JSON-RPC framed. `tools/code_implementation_server.py` is the most important — it exposes `read_file` / `write_file` / `execute_python` / `execute_bash` / `search_code` / `get_file_structure`; `core/agent_runtime/tools/registry.py` manages those subprocesses' lifecycles via `AsyncExitStack` and caches schemas for performance.

Why this layer? Three reasons.

The first is *security*. The LLM occasionally and confidently says "let me `rm -rf /`". If the LLM shells out directly, you can only pray. The MCP boundary lets you allowlist inside the server — `tools/command_executor.py` does exactly this: cross-platform command execution + path allowlist + timeout. The agent runner never sees the difference between "server refused" and "server returned nothing useful"; it only sees an `is_error: true` tool_result.

The second is *protocol decoupling*. A tool's implementation can be Python, Go, or Node — as long as it speaks MCP. Team A writes a RAG server, Team B writes a browser-automation server, both plug into the same agent runner without either knowing the other's language. The learn-version stops short of real MCP (s02 simulates the lifecycle with `io.Closer`), but Appendix B exercise #5 is exactly that: wrap a real MCP stdio with `os/exec` + JSON-RPC framing.

The third is *observability*. Every MCP call has (name, args, result) — `core/observability/` writes those into `task_dir/logs/mcp.jsonl`. When you do an incident review, that JSONL is the single source of truth: what did the LLM want, what did the server reply, what did the LLM do next. The learn-version's s10 carries this habit forward via `AppendJSONL` writing `implementation_attempts.jsonl` — coarser-grained than upstream (one line per file rather than one per tool), but the idea is the same.

```
without MCP boundary (chat + tool=os):  with MCP boundary (DeepCode):
┌──────────┐                            ┌──────────┐    ┌─────────────┐
│  LLM     │                            │  LLM     │    │ MCP server  │
│   ↓      │                            │   ↓      │    │  ┌────────┐ │
│  os.exec │ ← LLM has direct access    │ tool_use │ →  │  │ allow- │ │
│  os.open │   to all OS capability     │ JSON-RPC │    │  │ list,  │ │
└──────────┘                            └──────────┘    │  │ timeout│ │
                                                        │  └────────┘ │
                                                        │   ↓ os.*    │
                                                        └─────────────┘
```

The learn-version's s06 Runner is the simplified embodiment of this idea. The runner does not touch the OS directly; it delegates tool dispatch to a `Registry`, and the tools inside that registry can be in-memory pure functions (`echo`, `now`), they can be filesystem-touching (`read_file` / `write_file` in s10), and in the future they can be real MCP — the boundary stays, the implementation is free. That is the value of abstraction.

Further reading:
- `core/agent_runtime/tools/registry.py:11-130` — registry + AsyncExitStack
- `tools/code_implementation_server.py` — the workhorse MCP server
- `tools/command_executor.py` — command-execution allowlist
- learn-version s02 (`docs/en/s02-tool-registry.md`), s06 (`docs/en/s06-tool-capable-runner.md`) — Go-side registry + dispatch

## 论文到代码的可复现性极限 / Paper-to-code reproducibility limits

DeepCode hits 75.9% win rate against human PhDs on PaperBench (README L326-373) — that is a real, paper-acknowledged number. The number's existence does not mean *every* paper can be auto-reproduced. This section lists the un-reproducibility sources DeepCode openly recognizes and actively handles in engineering — once you read it, you'll know which tasks let DeepCode shine and which tasks you should keep for yourself.

**Limit one: figures.** `docling` does not OCR figures when it parses a PDF; algorithm flowcharts, model architecture diagrams, loss curves are all raster pixels. If a paper's core contribution lives in a figure (the canonical case: a GAN architecture diagram with the body text saying only "as shown in Fig. 3"), the LLM sees a paragraph of empty text plus "as shown in Fig. 3" and cannot fill in the gap no matter how clever it is. `workflows/agents/document_segmentation_agent.py` does extraction across text segments but skips figures. The learn-version does not attempt this layer (s05 abstracts `InputKind` but does not parse PDF), but this is a real upstream limit.

**Limit two: datasets.** A paper says "we use ImageNet" — fine, where is ImageNet? Whose account? How much bandwidth? How much disk? DeepCode does not solve this — the generated code includes placeholders like `# TODO: download ImageNet here` for a human to fill in. This is a *design choice*: letting the agent silently download datasets is "self-driving," not "assisted driving"; upstream draws this boundary explicitly outside the LLM.

**Limit three: hyperparameters and seeds.** A paper tells you lr=3e-4 and batch=64, but implicitly leaves a thousand other small decisions unspecified (warmup steps? β1 = 0.9 by default? seed = 42?). DeepCode picks *one* reasonable choice but does not promise its run will hit the same number as the original paper. This limit applies to any paper-to-code tool — it is not a bug.

**Limit four: compute.** You cannot ask the agent system to run its own generated code to verify — that would be 100 H100s × 72 hours. DeepCode's phase 11 (finalize) does not run training; it runs unit tests + static checks + small-scale sanity checks. `tools/code_implementation_server.py`'s `execute_python` tool has a timeout, beyond which it aborts. That means the agent gets *local* signal ("does it import?"), not end-to-end signal ("does the model hit 85% on ImageNet?").

```
paper (PDF)
   │
   │ docling parsing      ← figures lost (limit 1)
   ▼
standardized text
   │
   │ planning             ← dataset placeholder (limit 2)
   │                      ← hyperparam guess (limit 3)
   ▼
plan.yaml
   │
   │ implementation
   ▼
generated_code/
   │
   │ phase 11 validation  ← only small tests (limit 4)
   ▼
RunReport
```

**Why does this section matter?** Because if you expect DeepCode to mean "paste a NeurIPS 2024 PDF, hit run, wake up tomorrow with an ICLR-grade reproduction," you'll be disappointed. But if you expect "take a paper with full pseudocode plus a dataset placeholder, get a structured Python project skeleton with tests that import and pass unit tests," its 75.9% win rate is genuinely useful — *because it consciously draws a bright line between what it can do and what it cannot*.

The learn-version does not reproduce that line (s_full's demo runs from plan.yaml to a code directory, not from a PDF), but the line itself is the heart of upstream's design philosophy — the agent knows its limits, the agent does not pretend to be a PhD student.

Further reading:
- `workflows/agents/document_segmentation_agent.py` — long-PDF segmentation
- `workflows/agents/requirement_analysis_agent.py` — distill "what to do" into spec
- `tools/code_implementation_server.py` — `execute_python`'s timeout
- README L326-373 — PaperBench numbers

## 与 Claude Code / Cursor / Aider 的对比 / Comparison vs. Claude Code, Cursor, Aider

Most readers of this book also use Claude Code, Cursor, or Aider. They all call themselves "AI coding agents," but the problems they solve differ. This section spells out those differences — to calibrate where DeepCode fits and to help you pick the right tool for your project.

**Claude Code (the tool you're using right now).** Mode: human + agent interacting with an *existing* codebase. Strength: the understand-modify cycle — read code, edit code, run tests, commit. It has its own tool system (Bash, Read, Edit, Grep, Glob), session memory, and hooks. Its tools are for the *agent*, not the LLM directly — the LLM's tool_use goes through Claude Code's wrapper before reaching the OS. Conceptually Claude Code is DeepCode's phases 6-10 (the implementation phase), but more general, more interactive, and not dependent on a precomputed plan.

**Cursor.** Mode: IDE + LLM augmentation. Cursor is a fork of VS Code, and its LLM integration sits inside the editor — Cmd+K to edit a region, Cmd+L to ask codebase questions, Tab to complete. No phases; no plan; no cross-file workflow. It excels at the *single-file* experience: place the cursor, tell it what you want, it edits there. Versus DeepCode, Cursor will not generate a project skeleton from a paper, but once you already have a project, Cursor is far smoother than DeepCode could be.

**Aider.** Mode: CLI + git-aware diffs. Aider commits each change so you can review or revert. It is closest to Claude Code in product positioning, the difference being that Aider is more "shell purist" — no IDE, no Web UI, just a REPL. It supports more LLM backends than Claude Code does (OpenAI, Anthropic, Gemini, Ollama), but its tool system is weaker.

**DeepCode.** Different from all three: DeepCode does not work on an *existing* codebase. It starts from *zero* — paper / requirements / spec in, full project directory out. It has explicit phases (not a chat chain), it has plan-then-implement separation (not chat-and-edit), and it has paper-to-code-specific machinery (document segmentation, requirement analysis).

```
              from-zero  |   modify-existing  |  IDE-integrated  | single-file
DeepCode         ✓       |        △           |       ×          |     ×
Claude Code      △       |        ✓           |       ×          |     △
Cursor           ×       |        ✓           |       ✓          |     ✓
Aider            ×       |        ✓           |       ×          |     △
```

**Why does this matter to you?** Three judgements:

1. You want to start a new project from a paper / requirement / spec → DeepCode is the design fit.
2. You want to add a feature / fix a bug in an existing project → Claude Code or Cursor.
3. You want a hybrid (DeepCode for skeleton, Claude Code for iteration) — which is in fact upstream's own recommended workflow (README quickstart starting at L838).

The learn-version's role is *to help you understand* DeepCode's internals, *not* to replace DeepCode itself. The artifact s10 produces is `plan_minimal.yaml` (3 stub files), not a working ImageNet trainer — but the *mechanism* is the same: plan-then-iterate-with-loop-detection-and-memory-compaction. Once that mechanism is internalized, you will be able to read upstream's 2,300-line `agent_orchestration_engine.py` and see what it is doing, instead of being intimidated by its size.

Further reading:
- README L838-861 — upstream's official quickstart
- README L976-1123 — Nanobot Feishu/Telegram integration (one wrapping pattern)
- learn-version s_full (`docs/en/s_full-integration.md`) — wiring all 10 chapters to run a plan
- Appendix B — upstream source-reading map, your guide to which file to read next

---

### Cross-section recap: how the five choices support each other

Looking back across the appendix, the five sections are not independent. Explicit protocols (§1) are only stable when backed by immutable context (§2) — if phase 4's output could be silently rewritten by phase 5, the protocol becomes a paper promise. The MCP boundary (§3) makes the protocol's artifacts auditable: a JSONL line that reads "phase 5 called read_file with arg plan.yaml, got 6.3 KB back" turns post-mortem into a mechanical operation rather than archaeology. Paper-to-code limits (§4) in turn shape the protocol itself: phase 11 does not run training because the compute limit is what decides it can only verify "does this code import." Finally, the comparison with Claude Code / Cursor / Aider (§5) shows you why DeepCode's protocol is not *the only* right design — it is the optimal solution for the *from-zero* problem, not for every AI coding task.

If the five sections compress to a single sentence: DeepCode explicitly separates "what the agent should do" from "how the agent does it." The former is the phase protocol + immutable ctx + MCP boundary; the latter is LLM + prompts + tool dispatch. That separation gives the agent system the properties of software engineering — reviewable, testable, swappable. Chat chains cannot achieve this, because they fuse "what to do" and "how to do it" into a single thread of history.

### For the reader who finished this chapter

If you plan to use DeepCode: go to Appendix B, start with file #1 in the reading order, work through the 11 files in the table — roughly 8-12 hours total. By the end you will not just *use* DeepCode, you will *understand* it.

If you plan to *build* a system like DeepCode: read these five sections again, then ask yourself — what is your protocol? Is your ctx immutable? Where is your I/O boundary drawn? Where do your reproducibility limits live? Do your target users overlap with Claude Code / Cursor / Aider's? Don't write code until those five questions are answered — otherwise three months in you'll find yourself with a chat chain hidden under a stack of phase names.

If you are merely curious: the PaperBench number in README L326-373 (75.9%) is real; the 2026-04-17 config breaking change in CHANGELOG.md (collapsing `.env` into a single JSON) was a real design snap; the 2,312-line `workflows/agent_orchestration_engine.py` is real complexity. The romance of multi-agent systems is that they really do build things software engineers used to be unable to build — but underneath that romance lies the unromantic engineering discipline these five sections describe. You can't have one without the other.

### Do not treat this chapter as doctrine

A final friendly note: this appendix describes *DeepCode's current design choices*, not *the iron laws governing all future agent systems*. LLM capability is still evolving — maybe in two years chat chains will be stable (because models will reason strongly enough not to accumulate errors); maybe MCP gets replaced by another standard (agent protocols themselves are evolving fast); maybe paper-to-code's figure limit dissolves with multimodal LLMs. This appendix tells you the choices a state-of-the-art agent system made *at this moment* under *these constraints*, and the reasoning behind them. You'll be smarter for reading it, but don't become dogmatic. Re-read this in three years and notice which choices still hold and which have aged — that is what this chapter is really trying to give you.
