# s10 — code-impl-workflow

> The architectural climax. Composes a Runner (s06), a LoopDetector (s08), a MemoryAgent (s09) and the AtomicWriteJSON / AppendJSONL primitives (s07) into one top-level orchestrator that turns a YAML plan into a directory of generated source files. ~800 LOC end-to-end — large because it is the only chapter where four mechanisms converge in a single body.

## What this is

Upstream's `workflows/code_implementation_workflow.py` is a 1,100-line Python file. About 200 of those lines are the actual per-file orchestration loop — the rest is logging, MCP server bring-up, document segmentation hooks, retry policies, and progress callback plumbing. s10 extracts the loop and ships it as one package.

The mechanism in three sentences:

1. Read a JSON plan with a `files` list. For each file, register a fresh tool registry (read_file, write_file, execute_python), seed messages with system prompt + initial plan + a per-file user message, and call `Runner.Run`.
2. The runner asks the loop detector before each tool dispatch ("should we abort?"); if yes, return aborted with a reason. After every successful `write_file`, run `MemoryAgent.Compact` so the next file starts with a clean slate.
3. Append one JSONL row per file to `taskDir/implementation_attempts.jsonl`. Emit one `RunReport{Status, FilesCompleted, Total, Iterations, Elapsed, UnimplementedFiles}` summarising the whole run.

## Why this is the largest chapter

Roughly 800 LOC across 11 source files. That's deliberate: this is the only chapter that proves all the prior pieces actually fit together. The thinnest possible redeclaration of each upstream collaborator (Provider, Tool, Registry, Runner, LoopDetector, MemoryAgent, AtomicWriteJSON, AppendJSONL) is required because s10 obeys the project's session-isolation rule — its `go.mod` does NOT import s02 / s04 / s06 / s07 / s08 / s09. Composition without coupling.

If the rule were "feel free to import", s10 would be ~250 LOC and you could read it in 10 minutes. With the rule it's ~800 LOC and you need 30 minutes; in exchange you can run `go test ./agents/s10-code-impl-workflow` standalone with no other chapter's source on disk.

## Run it

```bash
cd agents/s10-code-impl-workflow

# Demo: replay a 3-file conversation, write files to a temp dir
go run . -plan testdata/plan_minimal.yaml -replay testdata/replay_three_files.json
```

Output:

```
status:           completed
reason:
files_completed:  3/3
iterations:       6
elapsed:          2ms
task_dir:         /var/folders/.../s10-demo-XXXXXXXX
```

Inspect the task dir:

```
$task_dir/
├── generate_code/
│   ├── main.go
│   ├── config.go
│   └── handler.go
├── implementation_attempts.jsonl   # one JSON line per file
└── implementation_report.json      # final RunReport, atomic-written
```

## Test it

```bash
go test -count=1 -v ./...
```

5 PASS in <1s. All tests use `t.TempDir()` and a `ReplayProvider` — no network, no real LLM, no shared state.

| # | Test | Verifies |
|---|---|---|
| 1 | `TestWorkflow_HappyPathThreeFiles` | 3-file plan + 6-response replay → all 3 files on disk + Status=="completed" |
| 2 | `TestWorkflow_AbortedByLoopDetector` | replay repeats the same tool 6× → Status=="aborted" with a "loop" reason |
| 3 | `TestWorkflow_MemoryCompactionInvokedPerWriteFile` | OnCompact fires exactly once per write_file; counting Tokenizer mock sees calls |
| 4 | `TestWorkflow_JSONLAttemptLogOnePerFile` | after 3 files, attempts.jsonl has 3 lines with non-empty timestamps and stop_reasons |
| 5 | `TestWorkflow_ResumeFromCheckpoint` | pre-existing a.go on disk → workflow skips it, only 4 provider calls used |

## Files

- `types.go` — minimal redeclaration of `Provider` / `Tool` / `Message` / `ContentBlock` / `ChatRequest` / `ChatResponse` / `Usage` / `ToolCallRequest`
- `registry.go` — minimal `Registry` (NewRegistry / Register / Get / List)
- `runner.go` — minimal `Runner` with LoopDetector pre-tool gating; one extra `StopAborted` reason and `RunSpec.OnToolResult` callback
- `loop_detector.go` — minimal `LoopDetector` (the 5 status codes, no FakeClock)
- `memory.go` — minimal `MemoryAgent.Compact` + `EssentialTools` whitelist + `Tokenizer` interface + `MessagesTokens`
- `plan.go` — `Plan` struct + `LoadPlan` (JSON, despite the .yaml fixture extension)
- `atomic.go` — `AtomicWriteJSON` (tmp + sync + rename)
- `jsonl.go` — `AppendJSONL` (per-path sync.Mutex)
- `tools_filesystem.go` — `read_file` / `write_file` / `execute_python` Tool stubs
- `report.go` — `RunReport` struct + Status taxonomy
- `workflow.go` — the orchestrator (`Workflow.Run`)
- `main.go` — CLI demo + `ReplayProvider`
- `workflow_test.go` — 5 hermetic integration-style tests
- `testdata/plan_minimal.yaml` — JSON-encoded 3-file plan
- `testdata/replay_three_files.json` — 6-response replay (write_file + final-text per file)

## Composition map

```
Workflow.Run
  │
  ├── LoadPlan(planPath)              [plan.go]
  ├── MemoryAgent{InitialPlan, ...}   [memory.go]
  ├── NewLoopDetector()               [loop_detector.go]
  ├── NewRunner(provider, detector)   [runner.go]
  │
  └── for each file in plan.Files:
        ├── if exists on disk → skip (resume)
        ├── reg = NewRegistry(); registerFileScopedTools(reg, codeDir)  [tools_filesystem.go]
        ├── messages = [system, plan, "implement <file>"]
        ├── runner.Run(ctx, RunSpec{...})
        │     └── per iteration:
        │           ├── provider.Chat
        │           ├── for each tool_call: detector.CheckTool(name)
        │           ├── dispatch tool, append tool_result
        │           └── OnToolResult fires → if write_file: memAgent.Compact(messages)
        ├── AppendJSONL(taskDir/implementation_attempts.jsonl, fileAttempt)  [jsonl.go]
        └── on StopAborted/StopMaxIterations/StopError: break
  │
  └── AtomicWriteJSON(taskDir/implementation_report.json, RunReport)  [atomic.go]
```

## What s10 explicitly does NOT do

- No real MCP. The three filesystem tools are direct Go funcs; upstream goes through stdio JSON-RPC.
- No retry / token-shrink policy. A failed provider call returns StopError to the workflow which surfaces it as Status=="error".
- No document segmentation, no requirement analysis, no plan review. Those live in upstream's `agent_orchestration_engine.py` (2,300 lines, intentionally not a chapter).
- No real YAML parsing. The plan fixture is JSON wrapped in a `.yaml` extension; future work can swap LoadPlan for a YAML reader.

## Dependencies

None. The `go.mod` declares `module github.com/Ding-Ye/learn-DeepCode/agents/s10-code-impl-workflow` and lists no requires. Per the project rule, sessions are isolated learning units.

## Upstream reading

Annotated extract: [`upstream-readings/s10-code-impl-workflow.py`](../../upstream-readings/s10-code-impl-workflow.py).
