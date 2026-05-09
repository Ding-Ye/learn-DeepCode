---
title: "s10 · File-by-file code implementation workflow"
chapter: 10
slug: s10-code-impl-workflow
est_read_min: 22
---

# s10 · File-by-file code implementation workflow

> The architectural climax. One `Workflow.Run(ctx, planPath, taskDir) (RunReport, error)` call composes a Runner (s06's loop), a LoopDetector (s08's safety net), a MemoryAgent (s09's clean-slate compaction), and the AtomicWriteJSON / AppendJSONL primitives (s07's persistence) into the orchestrator that turns a YAML plan into a directory of generated source files. ~800 LOC across 11 files — the longest chapter in the book, because four mechanisms converge in a single body and the session-isolation rule forbids importing any of them.

---

## Problem

Every previous chapter solved one mechanism in isolation. s06 had a runner with no safety net. s07 had checkpoints with no agent. s08 had a loop detector with no loop to watch. s09 had memory compaction with no run to compact. None of them is a working agent system on its own — and a reader who reaches the end of s09 should be allowed to ask: **does this stack actually fit together?**

s10 is the answer. It's the only chapter whose tests exercise multiple mechanisms simultaneously; it's the only chapter whose CLI runs a real (replay-driven) end-to-end agent loop that writes generated code to disk. It's also the longest, because the session-isolation rule means s10 redeclares the minimal subset of every collaborator rather than importing them.

The upstream artefact is `workflows/code_implementation_workflow.py` (1,184 lines). The teaching cut keeps the per-file orchestration body — about 200 lines of Python — and drops MCP server bring-up, document segmentation, retry-shrink token policies, and progress-callback plumbing. What survives is the heart: a per-file body that asks the model for one tool call at a time, gates each call through the loop detector, dispatches the tool, compacts memory after every `write_file`, and appends one JSONL row per file.

The output is a typed `RunReport`:

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

That's the exact same set of fields upstream populates in its `_last_run_state` dict — but typed, value-returned, and without the surrounding 1,000 lines of glue.

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
                  │    1. if exists on disk → skip (resume support)    │
                  │    2. reg = NewRegistry();                          │
                  │       registerFileScopedTools(reg, codeDir)        │
                  │    3. messages = [system, plan, "implement <f>"]   │
                  │    4. runner.Run(ctx, RunSpec{...})                │
                  │         │                                          │
                  │         │   per inner iteration:                   │
                  │         │     a. provider.Chat(req)                │
                  │         │     b. for each call: detector.CheckTool │
                  │         │        if ShouldStop → StopAborted       │
                  │         │     c. dispatch each call, append result │
                  │         │     d. OnToolResult fires → if write_file│
                  │         │        memAgent.Compact(messages)        │
                  │         │     e. final-text → StopDone             │
                  │         │     f. iterations exhausted → StopMax    │
                  │         ▼                                          │
                  │    5. AppendJSONL(attempts.jsonl, fileAttempt)     │
                  │    6. on aborted/max_iter/error → break out        │
                  └────────────────────────────────────────────────────┘
                                           │
                                           ▼
                  ┌────────────────────────────────────────────────────┐
                  │  RunReport{Status, FilesCompleted, Total,          │
                  │   Iterations, Elapsed, UnimplementedFiles}         │
                  │  AtomicWriteJSON(implementation_report.json)       │
                  └────────────────────────────────────────────────────┘
```

Five design decisions worth calling out:

1. **Session-isolation forces redeclaration.** Other sessions' types (Runner, LoopDetector, MemoryAgent, AtomicWriteJSON, AppendJSONL) are byte-compatible but separately declared. s10 contains its own `Runner` (~150 LOC), `LoopDetector` (~80 LOC), `MemoryAgent.Compact` (~100 LOC), `AtomicWriteJSON` (~30 LOC), `AppendJSONL` (~30 LOC). The price is duplicate code; the benefit is `cd agents/s10-code-impl-workflow && go test ./...` runs hermetically with zero other chapter source on disk.
2. **The runner is the integration point, not the workflow.** The workflow loop is straight-line; all the cross-mechanism glue happens inside `runner.Run`. Specifically the runner calls `detector.CheckTool` before each tool dispatch (s08 integration) and fires `RunSpec.OnToolResult` after each result (s09 integration via the workflow's callback). The workflow itself just iterates files and keeps score.
3. **Resume is filesystem-state, not flag-state.** `os.Stat(filepath.Join(codeDir, file))` is the source of truth for "is this file done?" — not a memory-agent flag, not a JSONL replay. A pre-existing file on disk is silently skipped; a new file is fully attempted. Tests pre-create `a.go` and verify the workflow makes only the 4 provider calls needed for `b.go` and `c.go` (vs. 6 needed if it had retried `a.go`).
4. **Compact runs after every write_file.** Upstream gates Compact behind `should_trigger_memory_optimization(messages, files_implemented_count)`; the teaching cut just compacts unconditionally after every successful write_file. Cheaper to reason about, marginally more aggressive on tokens. The hook is `RunSpec.OnToolResult`, which fires once per tool result with `(name, args, result, isError)`.
5. **`RunReport` is typed.** Upstream returns `dict[str, Any]` — research-notes anti-pattern #5 calls that out as a footgun. The Go port is one struct with a five-value Status taxonomy that the caller switches on. No string-key lookups, no `KeyError`s.

## How It Works

### 1. `types.go` — minimal redeclared shape

About 80 lines. Same shape as s06's: `Provider`, `Tool`, `Message`, `ContentBlock`, `ChatRequest`, `ChatResponse`, `ToolCallRequest`, `ToolSchema`, `Usage`. `Input` is `json.RawMessage` so the runner can dispatch arbitrary tool args without a string-roundtrip.

### 2. `registry.go` — minimal `Registry`

30 lines. `NewRegistry / Register / Get / List`. No `Close()` lifecycle (s02 owns that), no built-in-vs-MCP ordering, no schema cache invalidation. Re-registering a name overwrites — last write wins.

### 3. `loop_detector.go` — the 5 status codes

80 lines. `CheckTool(name) Status` returns one of `ok | loop_detected | timeout | stall | max_errors`. No `Clock` interface (s08 has it for hermetic tests; s10's tests use replay providers that exit before any wall-clock budget can fire). The runner integration site:

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

Note the AbortReason field — that's how the workflow surfaces the detector's message into `RunReport.Reason` without a second lookup.

### 4. `memory.go` — minimal `MemoryAgent.Compact`

100 lines. Same algorithm as s09: keep [system, synthetic-plan, ...messages from last write_file boundary] with non-essential tool blocks dropped. The same eight-tool whitelist (`read_file` / `write_file` / `execute_python` / `execute_bash` / `search_code` / `search_reference_code` / `get_file_structure` / `read_code_mem`) and the same boundary-pairing-preserving scan.

What's new vs. s09: s10 adds a `MessagesTokens` helper and the workflow calls it after every Compact. That single call exercises the configured Tokenizer, so a counting-Tokenizer mock plugged into `Workflow.MemoryTokenizer` lets tests prove the workflow actually ran the compaction it claimed to run.

### 5. `runner.go` — the integration loop

150 lines. Stripped s06 with two extras:

- `RunSpec.Detector *LoopDetector` — pre-tool gate before each dispatch.
- `RunSpec.OnToolResult func(name, args, result, isError)` — telemetry hook fired after each dispatched tool. The workflow uses it to detect successful write_file events and trigger Compact.

Plus a new `StopAborted` reason that surfaces detector messages without rebuilding the dispatch path. Everything else (truncation, error → IsError tool_result, MaxIterations apology) matches s06.

### 6. `tools_filesystem.go` — three small Tools

`read_file`, `write_file`, `execute_python`. The first two are real (they actually read / write files relative to a workspace dir set when the workflow registers them); `execute_python` is a one-liner that returns `"OK [stub]"`. The point isn't real execution — it's that the runner dispatches a non-fs tool through the same path as the fs ones, so we know the dispatch loop is general.

### 7. `workflow.go` — the orchestrator

200 lines. The body is one for-loop over `plan.Files`. Each iteration: skip-if-exists, build a fresh registry, build initial messages, call `runner.Run`, append a JSONL row, switch on `StopReason`. Any non-`StopDone` outcome breaks the outer loop with a corresponding `Status` value.

The `OnToolResult` callback is where s09 plugs in:

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

`messages` is a closure-captured local from `implementOneFile` — Compact returns a new slice, the local is overwritten, the next iteration's `runner.Run` sees the compacted state on its next provider call.

### 8. `plan.go` — JSON despite the .yaml extension

`LoadPlan(path)` reads `{"files": ["a.go", ...]}` from disk. The fixture file is named `plan_minimal.yaml` because that's what readers expect — the upstream artefact is YAML. Future work: swap in a real YAML reader from `gopkg.in/yaml.v3`. We chose JSON-now-YAML-later because s10 stays dependency-free in its `go.mod`.

### 9. `atomic.go` and `jsonl.go` — the persistence primitives

Same shape as s07. AtomicWriteJSON does tmp+sync+rename for the final RunReport snapshot (`taskDir/implementation_report.json`). AppendJSONL serialises one JSON line per file to `taskDir/implementation_attempts.jsonl`, with a per-path sync.Mutex so concurrent writers in the same process don't interleave.

## What Changed (vs. s09)

```diff
+ types.go        ~80 LOC    redeclared Provider/Tool/Message/ContentBlock/ChatRequest/
+                            ChatResponse/Usage/ToolCallRequest (json.RawMessage Input
+                            so the runner dispatches without string-roundtrip)
+ registry.go     ~30 LOC    minimal Registry (no Close, no MCP-vs-builtin ordering)
+ loop_detector.go ~80 LOC   the 5 status codes (no Clock — replay providers don't
+                            take wall-clock time)
+ memory.go       ~100 LOC   Compact + EssentialTools + Tokenizer + MessagesTokens
+ runner.go       ~150 LOC   stripped s06 + LoopDetector pre-tool gate + OnToolResult
+                            + StopAborted reason
+ tools_filesystem.go ~80 LOC  real read_file/write_file + stub execute_python
+ plan.go         ~50 LOC    JSON Plan loader (YAML deferred)
+ atomic.go       ~50 LOC    AtomicWriteJSON
+ jsonl.go        ~50 LOC    AppendJSONL
+ report.go       ~30 LOC    RunReport struct + Status taxonomy
+ workflow.go     ~200 LOC   the orchestrator: per-file loop, Compact-on-write_file,
+                            JSONL append, RunReport assembly
+ main.go         ~80 LOC    CLI demo + ReplayProvider
+ workflow_test.go            5 hermetic tests with t.TempDir() and ReplayProvider
- imports from s02/s04/s06/s07/s08/s09 — none. Session isolation is enforced.
```

s09 was a pure function (`messages → messages'`) — no I/O, no goroutines, single test file with five tests. s10 is the opposite: 11 source files, real filesystem writes, concurrent JSONL appends, integration tests that drive the whole stack. The chapter is large precisely because composition without coupling is expensive — and that expense is what proves the prior nine chapters actually fit together.

## Try It

```bash
cd agents/s10-code-impl-workflow

# Demo: replay a 3-file conversation, write files to a temp dir
go run . -plan testdata/plan_minimal.yaml -replay testdata/replay_three_files.json

# Tests (5 PASS, <1s)
go test -count=1 -v ./...
```

Demo output:

```
status:           completed
reason:
files_completed:  3/3
iterations:       6
elapsed:          2ms
task_dir:         /tmp/s10-demo-XXXXXXXX
```

Inspect the task dir:

```
$task_dir/
├── generate_code/                     # the materialised plan
│   ├── main.go
│   ├── config.go
│   └── handler.go
├── implementation_attempts.jsonl      # one JSON line per file
└── implementation_report.json         # final RunReport, atomic-written
```

Each attempt row:

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

**Reading notes**:

- **Loop detector is invoked exactly where the Go runner invokes it.** Both languages put the gate in front of the dispatch — not after. A loop that fires after dispatch is too late: the runaway tool already ran.
- **Memory compaction is gated in upstream, unconditional in the Go port.** Upstream's `should_trigger_memory_optimization(messages, files_implemented_count)` checks both message count and token budget; the teaching cut just compacts on every successful write_file. The trade-off is "marginally more aggressive on tokens" vs. "one fewer state machine to reason about" — for teaching the simpler version wins.
- **The status taxonomy is identical**. `completed | aborted | max_iterations | max_time | incomplete` upstream → `completed | aborted | max_iterations | max_time | error` in Go. The only rename is `incomplete` → `error` to match Go's idiom of reserving "incomplete" for non-fatal partial-success states.
- **Resume support is upstream's `_check_file_tree_exists`**; the Go port replaces it with `os.Stat` per file inside the outer loop. Upstream checks at workflow entry (skip the entire `create_file_structure` step); the Go port checks at file-iteration entry (skip individual files). Per-file resume is finer-grained — useful when only some files of a 30-file plan succeeded.

**Keep reading**: `workflows/agent_orchestration_engine.py` (2,312 lines) — the upstream conductor that runs `CodeImplementationWorkflow` as one of eleven phases. Intentionally NOT a chapter. Annotated extract: [`upstream-readings/s10-code-impl-workflow.py`](../../upstream-readings/s10-code-impl-workflow.py).

---

**Next**: there is no s11. The curriculum's code chapters end here; the s_full and Appendix chapters that follow are documentation-only. If you read every chapter from s01 to s10 you have walked from "the smallest possible HTTP request to Anthropic" all the way to "a multi-mechanism agent system that turns a plan into a directory of code". That arc is the book.
