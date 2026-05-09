# s08 — loop-detector

> A `LoopDetector` + `ProgressTracker` pair that catches three runaway modes — repeated tools, wall-clock timeout, no-progress stall — without false-firing on legitimate slow LLM calls. The non-obvious trick is `NoteLLMWait(d)`: an offset that subtracts model-side latency from the stall budget so a 60-second context window doesn't masquerade as a frozen pipeline.

## What this is

Upstream's `utils/loop_detector.py` ships two tightly-related dataclasses: `LoopDetector` (the abort decider) and `ProgressTracker` (the bookkeeping counter). Together they wrap the per-file inner loop in `code_implementation_workflow.py`, which is the only piece of the platform that runs long enough to need this kind of safety belt.

s08 ports both to ~250 lines of Go. The headline mechanism is the **LLM-wait offset**: the upstream comment at `loop_detector.py:55-58` calls out that "wall-clock budget that *excludes* LLM-call time" — `note_llm_wait` adds the elapsed LLM seconds back to `last_progress_time` so the stall check only penalises true tool-side inactivity. We model that in Go with a `pendingLLMOffset` field consumed (and zeroed) inside every `CheckTool` call.

## Run it

```bash
cd agents/s08-loop-detector

go run .
```

Output:

```
LoopDetector demo: calling "execute_python" five times in a row
---
call #1  code=ok             should_stop=false  message=processing normally
call #2  code=ok             should_stop=false  message=processing normally
call #3  code=ok             should_stop=false  message=processing normally
call #4  code=ok             should_stop=false  message=processing normally
call #5  code=loop_detected  should_stop=true   message=loop detected: "execute_python" called 5 times consecutively
---
aborting on call #5 due to loop_detected
```

## Test it

```bash
go test -v ./...
```

5 PASS, well under one second. All tests use a `*FakeClock` so timing is deterministic — no `time.Sleep`, no minute-scale waits.

## File map

- [`clock.go`](clock.go) — `Clock` interface, production `realClock{}`, hermetic `*FakeClock`
- [`detector.go`](detector.go) — `LoopDetector`, four `Status` codes, functional options, `NoteLLMWait` offset
- [`progress.go`](progress.go) — `ProgressTracker` + `ProgressSnapshot` (deduplicated file paths, mutex-guarded)
- [`main.go`](main.go) — CLI demo: 5 same-tool calls, prints each `Status`
- [`detector_test.go`](detector_test.go) — five tests covering loop / timeout / stall / offset / max-errors

## Why an injectable Clock

Production code uses `realClock{}` (delegates to `time.Now`). Tests use `*FakeClock` whose `Advance(d)` steps a private `now time.Time` field forward. The whole detector reads time exclusively through `d.clock.Now()`, so a test that verifies "after 31 seconds idle, stall fires" runs in microseconds — not 31 real seconds.

The interface is preferred over a `now func() time.Time` closure because `*FakeClock` carries its own state cleanly, and `WithClock(fc)` reads better than `WithNow(fc.Now)` at the call site.

## What's deliberately absent

| Feature | Where it shows up |
|---|---|
| `start_file(filename)` per-file stopwatch | Folded into the unified `Timeout` budget — s10 owns the file-level loop and re-creates a detector per file |
| Phase percent + ETA estimation in `ProgressTracker` | Presentation concern — belongs in s10's report layer |
| `should_abort()` + `get_abort_reason()` introspection helpers | Replaced by the typed `Status{Code, Message, ShouldStop}` return — callers switch on `st.Code` directly |
| `print()` side-effects in `record_error` / `record_success` | s08 has zero stdout side-effects; logging belongs at the call site |

## Upstream reference

- `utils/loop_detector.py:1-253` — full file, both classes.
- See [`docs/zh/s08-loop-detector.md`](../../docs/zh/s08-loop-detector.md) and [`docs/en/s08-loop-detector.md`](../../docs/en/s08-loop-detector.md) for the lesson.
- Annotated upstream: [`upstream-readings/s08-loop-detector.py`](../../upstream-readings/s08-loop-detector.py).
