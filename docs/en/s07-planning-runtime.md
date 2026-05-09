---
title: "s07 · Planning checkpoint + JSONL attempts"
chapter: 07
slug: s07-planning-runtime
est_read_min: 11
---

# s07 · Planning checkpoint + JSONL attempts

> Three on-disk artifacts plus a 40-line shape check. `AtomicWriteJSON` uses tmp+fsync+rename so readers see either the old file or the new one — never a half-written one. `AppendJSONL` serializes appends with a process-local `sync.Mutex`. `ValidatePlanText` asks one question — does the text mention all five required section names? That's the entire substance of upstream's `planning_runtime.py`.

---

## Problem

A planning LLM call takes 30+ seconds and occasionally fails. Without breadcrumbs:

- Process crash → the whole run is gone; restart starts from zero;
- Debugging a bad plan → no record of "what was tried last time";
- Multiple retries → intermediate artifacts overwrite each other; nobody knows which write was killed mid-flight;

The most insidious case is the **half-written file**: if `planning_checkpoint.json` is written halfway and the process dies, the next start reads it and panics — one crash poisons every future restart. Python upstream uses `tmp.replace(target)` (POSIX rename is atomic). Go must do the same, plus an explicit `f.Sync()` because Go's `os.Close` does not implicitly fsync the way Python's `with` block does on exit.

There's also the question: how do we know the LLM returned a **plausible** plan? Strict `yaml.safe_load`? Too strict — upstream learned long ago that planner models often emit a markdown-plus-YAML-fence hybrid that strict parsers reject for trivial reasons. Upstream's choice: **substring-check the five section names**. That's the loose `validate_plan_text` you see in the source.

## Solution

s07 splits `planning_runtime.py` into 5 Go files, one concern each:

1. **`paths.go`** — given `taskDir`, return the three artifact paths. Pure function, no I/O.
2. **`atomic.go`** — `AtomicWriteJSON(ctx, path, v)`: marshal → write `.tmp` → `f.Sync()` → `os.Rename`. Any failure removes the `.tmp` and leaves the target untouched.
3. **`jsonl.go`** — `AppendJSONL(ctx, path, v)`: take a per-path process-local `sync.Mutex`, then `O_APPEND` one JSON line + `\n`. Plus a generic `ReadAllJSONL[T any]` for tests.
4. **`validate.go`** — `ValidatePlanText(text) []string`: lower-case the text, check whether each of the 5 section names appears as a substring. Returns the **missing** list; empty slice means OK.
5. **`runtime.go`** — `PlanningRuntime{}` glues the four together and adds `IsExistingPlanUsable(taskDir) bool`, the cheap "can we resume?" probe s10 will call at workflow start.

Zero LLM dependency. Zero `httptest.Server`. Every test runs hermetically in `t.TempDir()`.

## How It Works

```ascii-anim frames=3
┌────────────────────────────────────────────────────────────┐
│  rt := &PlanningRuntime{}                                  │
│                                                            │
│  rt.WriteCheckpoint(ctx, taskDir, Checkpoint{...})         │
│         │                                                  │
│         ▼                                                  │
│  AtomicWriteJSON(ctx, "<taskDir>/planning_checkpoint.json")│
│         │                                                  │
│         ├─▶ marshal v                                      │
│         ├─▶ os.OpenFile(path+".tmp", O_CREATE|O_TRUNC)     │
│         ├─▶ f.Write(data)                                  │
│         ├─▶ f.Sync()       ← force fsync; any failure...   │
│         ├─▶ f.Close()                                      │
│         └─▶ os.Rename(tmp, path) ← ...removes tmp + keeps  │
│                                                            │
│  rt.RecordAttempt(ctx, taskDir, Attempt{OK:false, ...})    │
│         │                                                  │
│         ▼                                                  │
│  AppendJSONL(ctx, "<taskDir>/planning_attempts.jsonl")     │
│         ├─▶ lockFor(absPath).Lock()  ← per-path mutex      │
│         ├─▶ os.OpenFile(O_APPEND|O_CREATE)                 │
│         ├─▶ f.Write(json + "\n")                           │
│         └─▶ Unlock                                         │
└────────────────────────────────────────────────────────────┘
```

Core ~40 lines (excerpt from [`agents/s07-planning-runtime/atomic.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s07-planning-runtime/atomic.go) + [`runtime.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s07-planning-runtime/runtime.go)):

```go
func AtomicWriteJSON(ctx context.Context, path string, v any) error {
    data, _ := json.MarshalIndent(v, "", "  ")
    _ = os.MkdirAll(filepath.Dir(path), 0o755)

    tmp := path + ".tmp"
    f, _ := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
    if _, err := f.Write(data); err != nil { _ = f.Close(); _ = os.Remove(tmp); return err }
    if err := f.Sync();          err != nil { _ = f.Close(); _ = os.Remove(tmp); return err }
    if err := f.Close();         err != nil {                _ = os.Remove(tmp); return err }
    if err := os.Rename(tmp, path); err != nil {              _ = os.Remove(tmp); return err }
    return nil
}

func ValidatePlanText(text string) []string {
    lower := strings.ToLower(text)
    var missing []string
    for _, s := range RequiredPlanSections {
        if !strings.Contains(lower, s) { missing = append(missing, s) }
    }
    return missing
}
```

**5 non-obvious points**:

1. **`f.Sync()` patches Python's silent assumption** — upstream's `tmp.replace(target)` is atomic at the kernel level, but **data durability** is not: after rename, the extents may still live in the page cache. ext4 with `data=ordered` will protect you on Linux, but other filesystems / mount options won't. Go's extra `f.Sync()` is zero-cost belt-and-suspenders.
2. **`PlanningRuntime` zero-value is usable** — no `New()` function because the type holds no I/O state. Every method takes `taskDir` and computes its own paths. So spawning two concurrent runtimes for two tasks is just `&PlanningRuntime{}` twice — no setup, no teardown.
3. **`AppendJSONL`'s mutex is keyed by absolute path, not global** — a global mutex would serialize two unrelated tasks for no reason; per-path lock means two goroutines on the same task serialize (mandatory) while two goroutines on different tasks run fully in parallel.
4. **`ReadAllJSONL[T any]` is generic, not `[]map[string]any`** — Go 1.18+ generics let tests write `ReadAllJSONL[Attempt](path)` and skip a second `json.Unmarshal` round. Production never uses this function (production reads logs line-by-line); test assertions become extremely natural.
5. **`ValidatePlanText` does not parse YAML** — upstream doesn't strictly either: it does a substring check first and only **then** tries `yaml.safe_load`. The Go port stops at substring, for two reasons: (a) when YAML parsing fails, upstream falls back to the substring result anyway; (b) Go's stdlib has no YAML — pulling in a third-party dependency just for validation would be lopsided.

## What Changed (vs. s06 / earlier chapters)

```diff
+ first chapter that touches disk — s01..s06 were all in-memory + httptest
+ introduces atomic write (tmp+fsync+rename) — s10 reuses the same primitive
+ introduces JSONL append + per-path mutex — s10 reuses for per-file logs
+ introduces "zero-value-is-usable" Runtime pattern — PlanningRuntime{} is a complete object
+ introduces a generic test helper, ReadAllJSONL[T any]
- zero LLM dependency, zero Provider dependency — second chapter (after s05) entirely off the conversation axis
```

Earlier chapters all assumed "the conversation runs once in memory and ends" — s01's round-trip, s02's tool table, s06's Runner loop are all RAM-only. s07 is the first chapter that admits **long workflows must have checkpoints**: a 30-second LLM call, a 50-attempt retry budget, a multi-hour implementation phase — without disk, one interruption costs you everything.

A deeper take: earlier chapters solve "within-conversation" problems (how to call the model, how to respond to tools, how to manage context). s07 solves "across-process" problems (crash recovery, audit trail, concurrent append). It's an orthogonal axis — you could remove s07 entirely and the agent would still run; it would just lose its resume capability.

## Try It

```bash
cd agents/s07-planning-runtime

# Validate a candidate plan (all 5 sections present)
cat > /tmp/good_plan.md <<'EOF'
file_structure: main.go
implementation_components: parser
validation_approach: go test
environment_setup: go 1.23
implementation_strategy: top-down
EOF
go run . /tmp/good_plan.md

# Validate a plan missing some sections
cat > /tmp/bad_plan.md <<'EOF'
file_structure: main.go
implementation_components: parser
EOF
go run . /tmp/bad_plan.md   # exits 3, lists 3 missing sections

# Inspect the attempt log on disk
cat $TMPDIR/learn-deepcode-s07/planning_attempts.jsonl

# Tests
go test -v ./...
```

Expected stdout (good_plan):

```
OK — all 5 required sections present (158 bytes)
```

Expected stdout (bad_plan):

```
MISSING 3/5 sections:
  - validation_approach
  - environment_setup
  - implementation_strategy
```

Tests: 5 PASS, sub-second, all under `t.TempDir()`.

## Upstream Source Reading

```upstream:workflows/planning_runtime.py#L18-L72
REQUIRED_PLAN_SECTIONS = (
    "file_structure",
    "implementation_components",
    "validation_approach",
    "environment_setup",
    "implementation_strategy",
)


def planning_paths(paper_dir: str | Path) -> dict[str, Path]:
    root = Path(paper_dir)
    return {
        "checkpoint": root / "planning_checkpoint.json",
        "attempts":   root / "planning_attempts.jsonl",
        "meta":       root / "planning_result_meta.json",
    }


def write_json(path: str | Path, payload: dict[str, Any]) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = target.with_suffix(target.suffix + ".tmp")
    tmp.write_text(
        json.dumps(payload, ensure_ascii=False, indent=2, default=str),
        encoding="utf-8",
    )
    tmp.replace(target)


def append_jsonl(path: str | Path, payload: dict[str, Any]) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(payload, ensure_ascii=False, default=str))
        handle.write("\n")
```

**Reading notes**:

- **`REQUIRED_PLAN_SECTIONS` is a cross-module contract** — the prompt template tells the LLM to output these 5 keys, the validator checks the same tuple, and s10's code-gen phase reads the plan by these names. Any rename means three places to update — that's why both sides declare it as an immutable constant (Python: tuple; Go: `var ... = []string{...}`, mutable in theory but read-only by convention).
- **`tmp.replace(target)` semantics** — that's `os.replace`, the Python wrapper around POSIX `rename(2)`. The crucial property is "atomic within one directory" — which is why the code uses `target.with_suffix(suffix + ".tmp")` to get a sibling name **in the same directory**, not `/tmp` or another filesystem (rename across filesystems gives `EXDEV`, not atomicity). Go's `os.Rename` has the same constraint, and atomic.go's `dir := filepath.Dir(path)` is the equivalent guard.
- **Python's `with target.open("a")` is line-atomic under the GIL** — multiple threads in one process appending concurrently won't interleave because the GIL serializes Python-level operations. Go has no GIL, so explicit `sync.Mutex` is mandatory. This is one of those points where upstream silently relies on the language runtime and the Go port has to make it explicit.
- **No `json-repair` here** — elsewhere upstream uses the `json-repair` library to parse "approximately-JSON" output from LLMs, but `planning_runtime.py` writes/reads its own files, so the input format is deterministic. This is a useful boundary to internalize: **external inputs** (LLMs) get tolerant parsing; **internal state** (your own checkpoints) gets strict stdlib parsing. Mix the two and bugs leak into every future snapshot.
- **`coerce_text_to_minimal_plan` is the planner-failure fallback** — that 60-line "YAML scaffolder" was deliberately not ported. It's a **policy** ("if the model won't follow the schema, we'll inject a minimal plan so implementation can still proceed"), not a **mechanism**. The mechanism (persistence, atomic write, append log, shape validation) lives in s07; the policy (what to do when the planner fails) belongs in s10's orchestration logic.

**Read further**: from `planning_runtime.py` follow into `agent_orchestration_engine.py`'s phase 4 — see how `_run_planning(...)` injects `build_planning_checkpoint_callback` into `AgentRunSpec.checkpoint_callback`. That's the "async callback injection" pattern this Go chapter doesn't translate (Go calls `WriteCheckpoint` directly inside the for-loop, no callback). Annotated copy: [`upstream-readings/s07-planning.py`](../../upstream-readings/s07-planning.py).

---

**Next**: s08 unifies "ran too long / called the same tool too many times / saw too many errors" into one `LoopDetector` — and fixes a commonly overlooked anti-pattern: counting LLM network latency as "stuck".
