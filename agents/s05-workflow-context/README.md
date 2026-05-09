# s05 — workflow-context

> An immutable per-task `WorkflowContext` value type. Built once by `Prepare(input, opts)`, threaded read-only through every subsequent phase. Replaces the "raw strings + dicts threaded across 11 phases" antipattern.

## What this is

DeepCode's `workflows/workflow_context.py` is an `@dataclass(slots=True)` that pins down everything a multi-phase pipeline needs to know about one task: the input source, its detected kind, the workspace root, the per-task directory. Upstream Python uses `@dataclass(slots=True)` plus `Path` typing to keep the object cheap and unambiguous.

s05 ports the same idea to ~100 lines of Go using a different mechanism: **value-type struct with unexported fields and read-only accessor methods**. Go has no `frozen=True` decorator, but pass-by-value plus the absence of setters gives the same guarantee, enforced at compile time.

## Run it

```bash
cd agents/s05-workflow-context

go run . paper.pdf
go run . https://arxiv.org/abs/2401.01234
go run . spec.md
go run . -workspace /tmp/my-ws spec.md
```

Output:

```
task_id:        task_a1b2c3d4
input_source:   paper.pdf
input_kind:     pdf
workspace_root: /Users/.../.deepcode-learn
task_dir:       /Users/.../.deepcode-learn/tasks/task_a1b2c3d4
---
reference_path:              .../task_a1b2c3d4/reference.md
initial_plan_path:           .../task_a1b2c3d4/initial_plan.md
implementation_report_path:  .../task_a1b2c3d4/implementation_report.md
logs_dir:                    .../task_a1b2c3d4/logs
generate_code_dir:           .../task_a1b2c3d4/generate_code
```

## Test it

```bash
go test -v ./...
```

5 PASS, well under one second. No filesystem writes, no network. `t.TempDir()` is the only I/O surface area.

## File map

- [`context.go`](context.go) — `WorkflowContext` value type with unexported fields + read-only accessors; `InputKind` string enum
- [`prepare.go`](prepare.go) — `Prepare(input, opts)` constructor; input-kind detection table; `*EmptyInputError`
- [`paths.go`](paths.go) — derived path methods (`ReferencePath`, `LogsDir`, etc.) — all using `filepath.Join`
- [`main.go`](main.go) — CLI demo
- [`context_test.go`](context_test.go) — five tests covering kind detection, URL handling, OS-portable paths, value semantics, empty-input error

## Why value-type + unexported fields

Go has no `dataclass(frozen=True)`. The equivalent is:

| Property | How Go achieves it |
|---|---|
| Cannot reassign fields from outside | Fields are unexported (`taskID`, not `TaskID`) |
| Mutations don't leak across function calls | Pass-by-value default; every call site gets its own copy |
| Read-only access | Public methods with value receivers (`func (c WorkflowContext) TaskID() string`) |
| Equality comparable | All fields are comparable types → struct is automatically `==`-able |

The `==` operator working out-of-the-box (Test 4) is a free bonus — Python `@dataclass(eq=True)` requires the decorator argument; Go gives it for free when every field is comparable.

## What's deliberately absent

| Feature | Where it shows up |
|---|---|
| `to_dir_info()` legacy bridge dict | Not needed — Go callers use the accessor methods directly |
| `paper_path` / `paper_md_path` / `standardized_text` (Phase-2/3 fields) | Out of scope: those are mutated by upstream after construction; we keep s05 strictly immutable. They'd reappear as a separate `Phase2Output` value if we ported Phase 2 |
| `DEEPCODE_WORKSPACE` env-var resolver | s03 owns env-var interpolation; s05 takes a single `Options.WorkspaceRoot` override |
| Materializing `task_dir` on disk | Done by s07/s10 — s05 is pure data computation |

## Upstream reference

- `workflows/workflow_context.py:1-168` — the full file.
- See [`docs/zh/s05-workflow-context.md`](../../docs/zh/s05-workflow-context.md) and [`docs/en/s05-workflow-context.md`](../../docs/en/s05-workflow-context.md) for the lesson.
- Annotated upstream: [`upstream-readings/s05-workflow-context.py`](../../upstream-readings/s05-workflow-context.py).
