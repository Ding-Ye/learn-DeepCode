# s07 — planning-runtime

> Three on-disk artifacts under one task directory: an atomic JSON checkpoint, an append-only JSONL attempt log, and a final result-meta. Plus a cheap `ValidatePlanText` that asks one question — does the text mention all five required sections? — without trying to parse YAML perfectly.

## What this is

DeepCode's `workflows/planning_runtime.py` decouples planning-phase persistence from orchestration. The mechanism is small but load-bearing: the orchestrator calls back into `write_json` (atomic) for every checkpoint, `append_jsonl` for every attempt, and `validate_plan_text` for every candidate. Crash recovery, audit trails, and "should I retry or coerce?" decisions all hang off these primitives.

s07 ports the four primitives to ~250 lines of Go:

1. **`AtomicWriteJSON`** — marshal → write `.tmp` → fsync → rename. Readers always see the old or new file, never a half-written one.
2. **`AppendJSONL`** — open-append-with-mutex, one JSON per line, newline-terminated. Process-local mutex; cross-process needs flock (Appendix B exercise #4).
3. **`ValidatePlanText`** — case-insensitive substring match against five required section names (`file_structure`, `implementation_components`, `validation_approach`, `environment_setup`, `implementation_strategy`).
4. **`PlanningRuntime`** — the small orchestrator that wires the three above together and adds `IsExistingPlanUsable(taskDir)` for resume support.

Zero LLM dependence — every test runs hermetically in `t.TempDir()`.

## Run it

```bash
cd agents/s07-planning-runtime

# Validate a candidate plan and log the attempt
go run . path/to/plan.md

# Tests
go test -v ./...
```

The CLI exits `0` on a 5-section plan, `3` on a missing-section plan, `1` on read error, `2` on usage error. The attempt is logged at `$TMPDIR/learn-deepcode-s07/planning_attempts.jsonl` either way.

## Test it

```bash
go test -v ./...
```

5 PASS, well under 1s. No network, only `t.TempDir()`.

## File map

- [`paths.go`](paths.go) — `PlanningPaths(taskDir) Paths` joining the three artifact paths
- [`atomic.go`](atomic.go) — `AtomicWriteJSON(ctx, path, v)` via tmp+fsync+rename
- [`jsonl.go`](jsonl.go) — `AppendJSONL(ctx, path, v)` + `ReadAllJSONL[T any](path)`
- [`validate.go`](validate.go) — `ValidatePlanText(text) []string` (missing sections)
- [`runtime.go`](runtime.go) — `PlanningRuntime{}` with `RecordAttempt` / `WriteCheckpoint` / `WriteMeta` / `IsExistingPlanUsable`
- [`main.go`](main.go) — small validate-and-log CLI
- [`runtime_test.go`](runtime_test.go) — 5 hermetic tests

## What's deliberately absent

| Feature | Where it shows up |
|---|---|
| YAML parsing of the plan body | s10 — once we actually consume `file_structure` items, we'll add a real parser |
| Cross-process file locking (`flock`) | Appendix B exercise #4 — process-local `sync.Mutex` is enough for the curriculum |
| `coerce_text_to_minimal_plan` (the YAML scaffolder) | Out of scope — that's a planner-fallback policy, not a runtime concern |
| `build_planning_checkpoint_callback` (async closure) | Replaced by direct `WriteCheckpoint` calls — Go has no async/await |
| Plan-validation against parsed YAML structure | Out of scope — substring check is what upstream's loose check does too |

## Upstream reference

- `workflows/planning_runtime.py:1-263` — full module (constants, paths, write/read, validate, coerce, is_usable).
- See [`docs/zh/s07-planning-runtime.md`](../../docs/zh/s07-planning-runtime.md) and [`docs/en/s07-planning-runtime.md`](../../docs/en/s07-planning-runtime.md) for the lesson.
- Annotated upstream: [`upstream-readings/s07-planning.py`](../../upstream-readings/s07-planning.py).
