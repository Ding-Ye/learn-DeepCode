# s06 — tool-capable-runner

> The full agent loop. One `Provider`, one `Registry`, one `for`. The model speaks; the runner detects tool calls; the registry dispatches; the result feeds back; repeat until the model gives a final text answer or `MaxIterations` is hit.

## What this is

s01 made one provider call and printed the answer. s02 built a tool registry. s04 abstracted the LLM behind a `Provider` interface. s06 wires those three patterns into a real loop:

1. Call `Provider.Chat` with the current messages and the registry's schema list.
2. If the response carries `tool_calls` → dispatch each via `Registry.Get` + `tool.Run`, append a `tool_result` block per call, continue.
3. Otherwise the response is the final answer → return `RunResult{StopReason: StopDone}`.
4. Hit `MaxIterations` first → return the synthetic apology message from upstream's `_DEFAULT_MAX_ITERATIONS_MESSAGE` template, with `StopReason: StopMaxIterations`.

The whole runner is ~150 LOC of Go. The interesting part is what's deliberately absent — see "What's deliberately absent" below.

## Session-isolation rule

s06 has its own `go.mod` and **does not import** s02 or s04. It redeclares the minimal subset of types it needs: `Provider`, `Tool`, `Message`, `ContentBlock`, `ToolCallRequest`, `ChatRequest`, `ChatResponse`, `Usage`, plus a stripped-down `Registry`. The shapes are byte-compatible with the originals so a Tool from s02 can be copied here unchanged.

This is project-wide — every chapter is independently runnable, independently testable, and grep-friendly. A reader who jumps straight to s06 doesn't have to walk a dependency graph to understand the loop.

## Run it

```bash
cd agents/s06-tool-capable-runner

# Hardcoded 3-round replay: ask for echo → result → final answer.
go run .

# Tests (no network, all replay-driven, <1s)
go test -count=1 -v ./...
```

The CLI demo prints the full transcript so you can see exactly what messages the loop appends:

```
stop_reason: done
iterations:  3
transcript (6 messages):
  [0] user text="please call echo twice"
  [1] assistant tool_use(echo, id=call_1, args={"text":"hello, agent"})
  [2] user tool_result(id=call_1)="hello, agent"
  [3] assistant tool_use(echo, id=call_2, args={"text":"world!"})
  [4] user tool_result(id=call_2)="world!"
  [5] assistant text="Done. The echo tool returned 'hello, agent' then 'world!'."
```

## File map

- [`types.go`](types.go) — `Provider`, `Tool`, plus value types (`Message`, `ContentBlock`, `ToolCallRequest`, `ChatRequest`, `ChatResponse`, `Usage`)
- [`registry.go`](registry.go) — minimal `Registry`: `NewRegistry` / `Register` / `Get` / `List`
- [`spec.go`](spec.go) — `RunSpec` (input), `RunResult` (output), `Stop*` constants
- [`dispatch.go`](dispatch.go) — single-call dispatch with truncation + error-as-tool_result
- [`runner.go`](runner.go) — the loop (`Runner.Run`)
- [`replay.go`](replay.go) — `ReplayProvider` test/demo helper
- [`main.go`](main.go) — 3-round replay CLI demo
- [`runner_test.go`](runner_test.go) — 5 tests

## Tests

5 PASS, well under 1s, no network:

| # | Test | Verifies |
|---|---|---|
| 1 | `TestRun_OneRound` | provider returns text immediately → `StopDone`, no tool dispatch |
| 2 | `TestRun_TwoRound` | tool_use → text; `tool.Run` invoked exactly once; final text matches |
| 3 | `TestRun_MaxIterations` | `MaxIterations=1` + tool_use → `StopMaxIterations` + apology with `(1)` |
| 4 | `TestRun_Truncation` | 5000-byte tool output, `MaxToolBytes=200` → marker present, total len = 200 |
| 5 | `TestRun_ToolError` | `tool.Run` returns error → `tool_result.IsError=true`, loop continues |

## What's deliberately absent

| Feature | Where it would go |
|---|---|
| Empty-content retries (`_MAX_EMPTY_RETRIES`) | upstream stretch goal — the model occasionally returns blank text under finalization pressure |
| Length-recovery cycles | upstream stretch goal — when finish_reason=length, ask the model to continue |
| Orphan tool_result repair / micro-compaction | s09 (memory compaction) handles the long-running case |
| Loop detection (5× same tool, stalls) | s08 — the runner is dumb on purpose; s10 plugs in s08's detector |
| Hooks / injection callbacks / streaming | upstream-only; we don't need them for a teaching cut |
| Adaptive `maxTokens` retry policy | upstream stretch goal — would balloon the loop past 1000 LOC |

## Upstream reference

- `core/agent_runtime/runner.py:69-400` — `AgentRunSpec`, `AgentRunner.run` body.
- See [`docs/zh/s06-tool-capable-runner.md`](../../docs/zh/s06-tool-capable-runner.md) and [`docs/en/s06-tool-capable-runner.md`](../../docs/en/s06-tool-capable-runner.md) for the lesson.
- Annotated upstream: [`upstream-readings/s06-runner.py`](../../upstream-readings/s06-runner.py).
