// Package main — s05-workflow-context.
//
// WorkflowContext is the immutable per-task state container threaded through
// every later phase (planning in s07, code-impl in s10). It replaces the
// "raw strings + dicts" antipattern that bites every long-running pipeline.
//
// Go has no @dataclass(frozen=True). The equivalent is value-type semantics
// + unexported fields + read-only accessor methods. A copy is made on every
// pass-by-value (function call, struct embedding, slice append). Without
// setters, no caller can mutate the original. This is structurally enforced
// by the compiler: there is no syntax to write `ctx.taskID = "x"` from
// outside the package.
//
// Upstream counterpart: workflows/workflow_context.py:62-120
// (`@dataclass(slots=True) class WorkflowContext`).
package main

// InputKind labels the detected flavour of an input source. Mirrors upstream's
// `InputKind = Literal["pdf", "md", "docx", "txt", "html", "url"]`. We add
// "unknown" as the safe default — Go has no exhaustive Literal type, so the
// caller must handle it explicitly rather than receive an empty string.
type InputKind string

const (
	InputKindPDF     InputKind = "pdf"
	InputKindMD      InputKind = "md"
	InputKindDOCX    InputKind = "docx"
	InputKindTXT     InputKind = "txt"
	InputKindHTML    InputKind = "html"
	InputKindURL     InputKind = "url"
	InputKindUnknown InputKind = "unknown"
)

// WorkflowContext is the read-only handle every phase receives.
//
// All fields are unexported. The only way to build one is via Prepare();
// the only way to read fields is via the accessor methods below. Callers
// that pass a WorkflowContext by value (the default) can never observe a
// mutation by another goroutine because there is no method that writes.
//
// Note: this is a value type, NOT a pointer. Pass it by value everywhere.
// `ctx WorkflowContext` not `ctx *WorkflowContext`. Cheap to copy (5 strings).
type WorkflowContext struct {
	taskID        string
	inputSource   string
	inputKind     InputKind
	workspaceRoot string
	taskDir       string
}

// TaskID returns the short identifier ("task_<8 hex>") allocated by Prepare.
func (c WorkflowContext) TaskID() string { return c.taskID }

// InputSource returns the original input string (path or URL) as supplied
// by the caller — kept verbatim for audit/debug.
func (c WorkflowContext) InputSource() string { return c.inputSource }

// InputKind returns the detected input flavour. See the InputKind constants.
func (c WorkflowContext) InputKind() InputKind { return c.inputKind }

// WorkspaceRoot returns the resolved workspace root directory (absolute).
// Defaults to `$HOME/.deepcode-learn` unless overridden via Options.
func (c WorkflowContext) WorkspaceRoot() string { return c.workspaceRoot }

// TaskDir returns the per-task directory: WorkspaceRoot/tasks/<TaskID>.
// Every derived path (reference, initial plan, logs, generate_code) lives
// underneath it.
func (c WorkflowContext) TaskDir() string { return c.taskDir }
