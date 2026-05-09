---
title: "s08 · Loop detector + stall vs LLM offset"
chapter: 08
slug: s08-loop-detector
est_read_min: 11
---

# s08 · Loop detector + stall vs LLM offset

> A `LoopDetector` that catches three runaway modes (repeated tool, wall-clock timeout, no-progress stall), a `ProgressTracker` for completed-file bookkeeping, and a `NoteLLMWait` offset that distinguishes a real stall from a slow 60-second LLM call. About 250 lines of Go in total. **Tests use a `*FakeClock` and finish in microseconds** — pure logic, zero network.

---

## Problem

`workflows/code_implementation_workflow.py` can run for 30 minutes across 30 files, with 5–10 tool calls per file. Three classic failure modes show up in long loops like this:

1. **One tool repeated forever** — the LLM gets stuck in some state, calls `read_file` over and over, never reaches `write_file`.
2. **Wall-clock budget exceeded** — a single file taking longer than 10 minutes is almost always a deadlock; waiting won't help.
3. **Progress stall** — gaps between tool calls grow too large; something is clearly blocking.

A naive stall detector — "kill if `time.time() - last_progress_time > 300s`" — **falsely fires on legitimate slow LLM calls**. Long contexts, network blips, provider rate-limits: a single round-trip routinely takes 60–120 seconds. Counting that **model-side wait** as a stall murders healthy pipelines at random in production.

The upstream fix is in the comment at `utils/loop_detector.py:55-58`:

> Wall-clock budget that *excludes* LLM-call time. `note_llm_wait` adds the elapsed LLM seconds back to `last_progress_time` so the stall check only penalises true tool-side inactivity.

s08's entire job is to port that offset mechanism faithfully into Go, plus make the **clock injectable** so tests cover every timing branch in microseconds.

## Solution

```ascii-anim frames=2
┌──────────────────────────────────────────────────────────────────────┐
│  ┌──────────────┐   CheckTool(name)    ┌────────────────────────┐    │
│  │   Caller     │ ───────────────────▶ │    LoopDetector        │    │
│  │ (s10 file    │                      │  history[]             │    │
│  │  loop)       │ ◀─── Status{...} ─── │  lastProgressAt        │    │
│  └──────────────┘                      │  consecutiveErrors     │    │
│         │                              │  pendingLLMOffset ◀──┐ │    │
│         │ NoteLLMWait(d)               │  startedAt           │ │    │
│         │  (after each LLM call) ────────────────────────────┘ │    │
│         ▼                              └────────────────────────┘    │
│   inside CheckTool:                                                  │
│     1. history.append(name); trim to last 10                         │
│     2. lastProgressAt += pendingLLMOffset; pendingLLMOffset = 0      │
│     3. if last MaxRepeats names are identical → "loop_detected"      │
│     4. if now - startedAt > Timeout         → "timeout"              │
│     5. if now - lastProgressAt > StallThreshold → "stall"            │
│     6. if consecutiveErrors >= MaxErrors    → "max_errors"           │
│     7. else                                 → "ok"                   │
└──────────────────────────────────────────────────────────────────────┘
```

The four status codes (`loop_detected` / `timeout` / `stall` / `max_errors`) match upstream's strings verbatim so Go and Python log lines compare cleanly. `ok` is the happy path.

Core type (excerpt from [`agents/s08-loop-detector/detector.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s08-loop-detector/detector.go)):

```go
type LoopDetector struct {
    MaxRepeats     int
    Timeout        time.Duration
    StallThreshold time.Duration
    MaxErrors      int

    clock             Clock
    history           []string
    lastProgressAt    time.Time
    consecutiveErrors int
    pendingLLMOffset  time.Duration
    startedAt         time.Time
}

func (d *LoopDetector) NoteLLMWait(d2 time.Duration) {
    if d2 <= 0 {
        return
    }
    d.pendingLLMOffset += d2
}

func (d *LoopDetector) CheckTool(name string) Status {
    now := d.clock.Now()
    d.history = append(d.history, name)
    if len(d.history) > historyWindow {
        d.history = d.history[len(d.history)-historyWindow:]
    }
    if d.pendingLLMOffset > 0 {
        d.lastProgressAt = d.lastProgressAt.Add(d.pendingLLMOffset)
        d.pendingLLMOffset = 0
    }
    // ... four if-branches in order: loop / timeout / stall / max_errors
}
```

## How It Works

**Four non-obvious points**:

1. **The offset is "consumed" inside `CheckTool`, not applied immediately in `NoteLLMWait`** — upstream Python does `last_progress_time += elapsed_seconds` eagerly; we deliberately stage the value in `pendingLLMOffset` and apply it on the next `CheckTool`. Behaviourally equivalent (the only realistic call pattern is note → check), but the Go staged design makes `pendingLLMOffset` **observable** in tests: you can assert it's non-zero between note and check, proving the wiring is correct.
2. **`Clock` is an interface, not a `now func() time.Time`** — the interface lets `*FakeClock` carry its own state (`now time.Time` field + `Advance(d)` method), and `WithClock(fc)` reads better than `WithNow(fc.Now)`. Cost: one extra type. Benefit: `fc.Advance(31*time.Second)` followed by an assertion that "stall fired" is a one-liner — no `time.Sleep`.
3. **The check ordering matters** — `loop_detected` must come before `timeout`, because a tool spamming five times in one second should report "loop", not wait 600 seconds for "timeout". Our `if` chain strictly follows `loop → timeout → stall → max_errors`, returning on the first hit, matching upstream.
4. **`MaxRepeats < 2` disables loop detection** — any sequence of length 1 cannot "repeat". Upstream relies on `len(self.tool_history) >= self.max_repeats` to disable implicitly; we write `if d.MaxRepeats >= 2 && ...` explicitly to make intent legible.

`ProgressTracker` is a different beast: it **makes no decisions**, only counts. `CompleteFile(path)` deduplicates (using upstream's path-normalization rules: `replace("\\","/")` plus trim); `Snapshot()` returns a value-type `ProgressSnapshot` whose `Files` field is a fresh copy — callers may mutate without affecting internal state.

## What Changed (vs. s07)

```diff
+ clock.go     New Clock interface + realClock + *FakeClock — injectable-time pattern
+ detector.go  LoopDetector + 4 Status codes + NoteLLMWait offset mechanism
+ progress.go  ProgressTracker + ProgressSnapshot, mutex-guarded
+ Tests use FakeClock to compress 31-second waits into microseconds
- Zero LLM dependence, zero I/O — pure logic, orthogonal to s07's atomic write/jsonl
+ Functional Options for thresholds (WithMaxRepeats / WithTimeout / ...) — fixes anti-pattern #10
```

s07 cared about **resumability** (atomic write + jsonl + meta + 5-section validate); s08 cares about **when to stop** (loop / timeout / stall / errors). Both compose in s10: s10's file loop uses s07's jsonl for attempt logs, s08's detector for abort decisions, and s07's `IsExistingPlanUsable` to resume from checkpoint.

Deeper comparison:
- s07 is **stateful with I/O** (writes jsonl, writes checkpoint, atomic disk operations); s08 is **stateful with zero I/O** (reads only the clock, reads only its own in-memory fields). Both are "safety belt" layers but with very different risk surfaces.
- s07 introduced "`taskDir` is given by s05; I don't construct it" as a dependency contract; s08 introduces "`Clock` is injected at construction; I don't call `time.Now()` directly" — a cleaner contract. s08's contract is stricter because *all* of its behaviour is a function of time — externalising time externalises testing.
- s07's `ValidatePlanText` returns `[]string` of missing sections; s08's `CheckTool` returns a single `Status{Code, Message, ShouldStop}`. Both styles are more typed than upstream Python's dict.

## Try It

```bash
cd agents/s08-loop-detector

# Run the demo (5 same-named tool calls → 5th call triggers loop_detected)
go run .

# Run the tests (5 PASS, all use FakeClock, sub-second total)
go test -v ./...

# vet + build
go vet ./...
go build ./...
```

Expected stdout (demo):

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

Test matrix (5 tests):

| # | Scenario | Expected Code |
|---|---|---|
| 1 | 5 identical tool names in a row | `loop_detected` |
| 2 | Clock advanced past Timeout | `timeout` |
| 3 | Clock advanced past StallThreshold, **no** NoteLLMWait | `stall` |
| 4 | Clock advanced past StallThreshold, **with** NoteLLMWait(stall+1s) | `ok` ← offset wins |
| 5 | 3 RecordError calls then CheckTool | `max_errors` |

Test #4 is this chapter's signature test — it isolates the proof that `NoteLLMWait`'s offset cancels an equal amount of wall-clock advance.

## Upstream Source Reading

```upstream:utils/loop_detector.py#L23-L141
class LoopDetector:
    def __init__(
        self,
        max_repeats: int = 5,
        timeout_seconds: int = 600,
        stall_threshold: int = 300,
        max_errors: int = 10,
    ):
        self.max_repeats = max_repeats
        self.timeout_seconds = timeout_seconds
        self.stall_threshold = stall_threshold
        self.max_errors = max_errors

        self.tool_history: List[str] = []
        self.start_time = time.time()
        self.last_progress_time = time.time()
        self.consecutive_errors = 0
        # Wall-clock budget that *excludes* LLM-call time. ``note_llm_wait``
        # adds the elapsed LLM seconds back to ``last_progress_time`` so the
        # stall check only penalises true tool-side inactivity.
        self._pending_llm_offset_s: float = 0.0

    def note_llm_wait(self, elapsed_seconds: float) -> None:
        if elapsed_seconds <= 0:
            return
        self.last_progress_time += elapsed_seconds
```

**Reading notes**:

- **`note_llm_wait` is two lines** — upstream concentrates all the complexity in *when* it's called: the caller (`code_implementation_workflow.py`) does `loop_detector.note_llm_wait(time.time() - llm_start)` after every `await provider.chat(...)`. The Go port's equivalent contract: **every LLM call must be followed by `NoteLLMWait(time.Since(start))`**. s10 enforces this at every site.
- **`set(recent_tools)` is more Pythonic but no faster than a Go inner loop** — `len(set(...)) == 1` over 5 elements isn't slower than five iterations, but it's harder to read. The Go version's explicit `for` makes "all equal?" obvious to a reviewer.
- **`current_file` / `file_start_time` are folded into `startedAt` in our port** — upstream assumes one detector is reused across files; we assume s10 will allocate a fresh detector per file. This shaves ~30 lines (no two-stage `start_file()` / `complete_file()` API) at the cost of one extra `d := NewLoopDetector(...)` per file in s10.
- **`should_abort()` and `get_abort_reason()` are upstream's afterthought introspection helpers** — they call `check_tool_call("")` with an empty name to "peek" at state. The Go port deletes both: `CheckTool`'s typed `Status` already carries both "should I stop?" and "why?" — no second API needed.
- **`record_progress` does three things at once in upstream** — clear errors, clear offset, update timestamp. The Go port splits these into `RecordSuccess` (high-level success signal) and "the offset is consumed in the next CheckTool" — separating "what counts as progress" from "what counts as LLM wait" at the type level.

**Read next**: from `loop_detector.py` go to `workflows/code_implementation_workflow.py:300-450` to see how the detector is wired into the per-file inner loop and where `note_llm_wait` is inserted — that's the code s10 will reorganize. Then re-read `utils/loop_detector.py:182-253` for `ProgressTracker`; note that upstream has phase-percent + ETA estimation that we deliberately did not port — that logic belongs in s10's `RunReport` layer. Annotated copy: [`upstream-readings/s08-loop-detector.py`](../../upstream-readings/s08-loop-detector.py).

---

**Next**: s09 takes the `[]Message` history coming out of s06's runner and "clean-slate"-compacts it after every successful `write_file` — clearing back to system prompt + initial plan + the current round's whitelisted tool results. The shift from "don't get stuck" to "don't run out of memory".
