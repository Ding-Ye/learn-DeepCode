---
title: "s05 · Immutable workflow context"
chapter: 05
slug: s05-workflow-context
est_read_min: 10
---

# s05 · Immutable workflow context

> A 5-field `WorkflowContext` value type, built once by `Prepare(input, opts)` and threaded read-only through every phase. Go has no `frozen=True` — but **value-type + unexported fields + read-only accessors** gives you the same guarantee, enforced by the compiler.

---

## Problem

Threading raw strings and dicts across a dozen phases is the single biggest source of bugs in upstream DeepCode's history. Is `paper_path` relative or absolute? Is `task_id` a UUID or 8 hex chars? Is `workspace_root` `os.getcwd()/deepcode_lab` this minute, or `~/.deepcode/`? If Phase 4 mutates `dir_info["reference_path"]`, Phase 7 sees a different string than Phase 2 wrote — and good luck reproducing that.

`workflows/workflow_context.py` solves this by **freezing** everything into an `@dataclass(slots=True)`: typed fields, every path is a `pathlib.Path`, derived values are `@property`-based. Phase 2/3 are still allowed to fill in optional fields like `paper_path` (a compromise upstream didn't fully resolve), but from Phase 4 onward the object is effectively immutable.

But Python's `@dataclass` is **not** truly frozen by default — you'd need `frozen=True` (which upstream skipped). How do we give Go callers a stronger guarantee than upstream itself has?

## Solution

Go has no `dataclass(frozen=True)`, but it has stronger structural equivalents:

1. **Value type, not pointer** — `func phase4(ctx WorkflowContext)` passes by value. Each call site gets its own copy. Even if a callee tried to mutate its copy, the caller's original is untouched.
2. **Unexported fields + read-only accessors** — `taskID`, `inputSource`, etc. are lowercase. Outside the package they're invisible. Anyone wanting to "modify" must go through an exported method — and we deliberately ship zero setters. This is **compile-time** immutability, not a runtime check.
3. **`Prepare` is the single constructor** — input-kind detection, task-id allocation, workspace resolution all live in `Prepare(input string, opts Options)`. Other code can only read.

Bonus: because every field is a comparable type (5 strings), `==` works out of the box. `before == after` is structural equality with no `Equal()` method to write. Python needs `@dataclass(eq=True)` to get the same property.

## How It Works

```ascii-anim frames=2
┌──────────────────────────────────────────────────────────┐
│  Prepare("paper.pdf", Options{WorkspaceRoot: "/tmp/x"})  │
│         │                                                │
│         ▼                                                │
│  detectInputKind("paper.pdf") = "pdf"                    │
│  generateTaskID()             = "task_a1b2c3d4"          │
│  resolveWorkspaceRoot(opts)   = "/tmp/x"                 │
│         │                                                │
│         ▼                                                │
│  WorkflowContext{                                        │
│    taskID:        "task_a1b2c3d4",                       │
│    inputSource:   "paper.pdf",                           │
│    inputKind:     "pdf",                                 │
│    workspaceRoot: "/tmp/x",                              │
│    taskDir:       "/tmp/x/tasks/task_a1b2c3d4"           │
│  }                                                       │
│         │                                                │
│         ▼  ctx.ReferencePath()                           │
│  filepath.Join(taskDir, "reference.md")                  │
│  → "/tmp/x/tasks/task_a1b2c3d4/reference.md"             │
└──────────────────────────────────────────────────────────┘
```

Core ~30 lines (excerpt from [`agents/s05-workflow-context/context.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s05-workflow-context/context.go) + [`prepare.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s05-workflow-context/prepare.go)):

```go
type WorkflowContext struct {
    taskID        string
    inputSource   string
    inputKind     InputKind
    workspaceRoot string
    taskDir       string
}

func (c WorkflowContext) TaskID() string        { return c.taskID }
func (c WorkflowContext) InputKind() InputKind  { return c.inputKind }
func (c WorkflowContext) TaskDir() string       { return c.taskDir }
// ... three more accessors of the same shape

func Prepare(input string, opts Options) (WorkflowContext, error) {
    if strings.TrimSpace(input) == "" {
        return WorkflowContext{}, &EmptyInputError{}
    }
    taskID := opts.TaskIDOverride
    if taskID == "" {
        var err error
        taskID, err = generateTaskID()
        if err != nil {
            return WorkflowContext{}, err
        }
    }
    root, err := resolveWorkspaceRoot(opts)
    if err != nil {
        return WorkflowContext{}, err
    }
    return WorkflowContext{
        taskID:        taskID,
        inputSource:   input,
        inputKind:     detectInputKind(input),
        workspaceRoot: root,
        taskDir:       filepath.Join(root, "tasks", taskID),
    }, nil
}
```

Derived paths all go through `filepath.Join` ([`paths.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s05-workflow-context/paths.go)):

```go
func (c WorkflowContext) ReferencePath() string {
    return filepath.Join(c.taskDir, "reference.md")
}
func (c WorkflowContext) LogsDir() string {
    return filepath.Join(c.taskDir, "logs")
}
// ... three more of the same shape
```

**4 non-obvious points**:

1. **`Prepare` does NOT create any directory** — it's a pure compute function. The task directory gets `mkdir`ed by s07/s10 when they actually need to write something. This makes s05 tests run in nanoseconds — pure in-memory `filepath.Join`, no `t.TempDir()` strictly required.
2. **`task_<8 hex>` uses `crypto/rand`, not `math/rand`** — even for collision-avoidance-only IDs, crypto/rand is 4 bytes and zero extra cost. Keeps `math/rand` away from anything ID-shaped, lest someone misread it as a guess-resistant token later.
3. **`detectInputKind` puts URLs ahead of extensions** — `https://x.com/paper.pdf` is a URL, not a PDF. Upstream does the same: PDF download is Phase 2's job; Phase 1 only cares about "fetch vs. read directly".
4. **Derived paths use value receivers** — `func (c WorkflowContext) ReferencePath()` not `(c *WorkflowContext)`. Value receivers reinforce the contract: "I won't mutate anything". One glance and the reader knows this is a query, not a mutation.

## What Changed (vs. s04)

```diff
+ context.go    introduces WorkflowContext value type + InputKind string enum + 5 read-only accessors
+ prepare.go    single constructor Prepare(); input-kind detection table; *EmptyInputError typed error
+ paths.go      5 derived path methods, all via filepath.Join
- zero LLM dependency — this chapter is pure data + filepath manipulation
+ tests use filepath.ToSlash for OS-portable string comparison
+ introduces "value-type + unexported fields = Go's frozen=" pattern
```

s04 cared about **protocol translation** (mapping Anthropic vs OpenAI to one canonical `ChatResponse`); s05 cares about **task identity** (what invariants every phase of one run shares). The two layers are orthogonal — s05 doesn't know Provider exists, s04 doesn't know any task exists.

A deeper comparison:
- s01-s04 all defined **mutable** data structures (`Registry` obviously mutates the map; `Config` allows field tweaks). s05 is the first **read-only** type — it sets a precedent: any value that "represents the identity of one task run" should follow this pattern.
- s01-s04 all carried an LLM dependency (HTTP requests, tool schemas, finish-reason parsing). s05 is the first **pure data + filepath** chapter, demonstrating that "immutable context" is orthogonal to "talking to a model" as an abstraction.

## Try It

```bash
cd agents/s05-workflow-context

# Local path
go run . paper.pdf

# URL
go run . https://arxiv.org/abs/2401.01234

# Custom workspace
go run . -workspace /tmp/learn-ws spec.md

# Tests
go test -v ./...
```

Expected stdout (PDF input):

```
task_id:        task_a1b2c3d4
input_source:   paper.pdf
input_kind:     pdf
workspace_root: /Users/you/.deepcode-learn
task_dir:       /Users/you/.deepcode-learn/tasks/task_a1b2c3d4
---
reference_path:              /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/reference.md
initial_plan_path:           /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/initial_plan.md
implementation_report_path:  /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/implementation_report.md
logs_dir:                    /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/logs
generate_code_dir:           /Users/you/.deepcode-learn/tasks/task_a1b2c3d4/generate_code
```

Tests: 5 PASS, nanoseconds. No filesystem writes, no network.

## Upstream Source Reading

```upstream:workflows/workflow_context.py#L62-L126
@dataclass(slots=True)
class WorkflowContext:
    """Everything the pipeline needs to know about one task."""

    task_id: str
    input_source: str
    input_kind: InputKind
    workspace_root: Path
    task_dir: Path
    enable_indexing: bool
    task_kind: TaskKind = "paper2code"
    skip_research_analysis: bool = False
    paper_path: Path | None = None
    paper_md_path: Path | None = None
    standardized_text: str | None = None

    @property
    def reference_path(self) -> Path:
        return self.task_dir / "reference.txt"

    @property
    def initial_plan_path(self) -> Path:
        return self.task_dir / "initial_plan.txt"

    @property
    def implementation_report_path(self) -> Path:
        return self.task_dir / "code_implementation_report.txt"
```

**Reading notes**:

- **The `@dataclass(slots=True)` compromise** — upstream uses slots for memory efficiency but **skipped** `frozen=True`. Reason: Phase 2/3 still backfill `paper_path` and `standardized_text`. This is a real-world example of "tried for immutability, dragged back by two phases". Our Go port chooses to be more aggressive: strip those mutable fields out of Context entirely (if/when we port Phase 2, they live in that phase's own Output value). That makes Context truly immutable.
- **`pathlib.Path` operator overloading** — upstream uses `task_dir / "reference.txt"`, which is legal because `Path.__truediv__` exists. Go has no operator overloading, so we write `filepath.Join(task_dir, "reference.txt")`. Equivalent semantics; readability difference is negligible.
- **`to_dir_info()` is technical debt** — upstream needs an "export to dict" method for the legacy stringly-typed Phase 4-10. We skip this API outright: Go callers use accessor methods, which read better than dict lookups. If JSON serialization is ever needed, adding `MarshalJSON` would be three lines.
- **`resolve_workspace_root` 3-tier priority** — upstream is env > yaml > cwd. We collapse to 2 tiers (opts.WorkspaceRoot > $HOME/.deepcode-learn) and push env-var interpolation to s03's config loader. One mechanism per chapter.
- **Why the `EXTENSION_TO_KIND` table deserves attention** — `.markdown` → `md`, `.doc` → `docx`, `.htm` → `html` are three real edge cases users will hit. Our Go table copies them line-for-line, in the same file as the lookup function — unit tests catch any future hand-edits that drop a row.

**Read further**: from `workflow_context.py` follow into `workflows/environment.py` for `prepare_workflow_environment` — that's where mkdir + Phase 0 side effects actually happen (s10 is where we reproduce that). Trace `task_dir` usage into `workflows/code_implementation_workflow.py` to see `task_dir / "generate_code" / file_name` string-concat — that's the code s10 will reorganize. Annotated copy: [`upstream-readings/s05-workflow-context.py`](../../upstream-readings/s05-workflow-context.py).

---

**Next**: s06 fuses s02's Registry and s04's Provider into a tool-capable Runner — a real agent loop: call model → detect tool_use → dispatch tool → feed back tool_result → repeat until the model emits final text.
