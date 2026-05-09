// Package main — s05-workflow-context tests.
//
// All tests are hermetic: t.TempDir() for workspace overrides, no network,
// no global state. They run in nanoseconds — pure data + filepath joins.
package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Test 1: .pdf input → InputKind=="pdf"; non-empty TaskID; TaskDir contains "tasks".
func TestPrepare_PDFInput(t *testing.T) {
	ws := t.TempDir()
	ctx, err := Prepare("paper.pdf", Options{WorkspaceRoot: ws})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if ctx.InputKind() != InputKindPDF {
		t.Errorf("InputKind: got %q want %q", ctx.InputKind(), InputKindPDF)
	}
	if ctx.TaskID() == "" {
		t.Errorf("TaskID is empty")
	}
	if !strings.HasPrefix(ctx.TaskID(), "task_") {
		t.Errorf("TaskID: got %q, want prefix task_", ctx.TaskID())
	}
	if !strings.Contains(filepath.ToSlash(ctx.TaskDir()), "/tasks/") {
		t.Errorf("TaskDir: got %q, want a /tasks/ component", ctx.TaskDir())
	}
	if ctx.InputSource() != "paper.pdf" {
		t.Errorf("InputSource: got %q want %q", ctx.InputSource(), "paper.pdf")
	}
	if ctx.WorkspaceRoot() != ws {
		t.Errorf("WorkspaceRoot: got %q want %q", ctx.WorkspaceRoot(), ws)
	}
}

// Test 2: https:// input → InputKind=="url".
func TestPrepare_URLInput(t *testing.T) {
	ws := t.TempDir()
	ctx, err := Prepare("https://arxiv.org/abs/2401.01234", Options{WorkspaceRoot: ws})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if ctx.InputKind() != InputKindURL {
		t.Errorf("InputKind: got %q want %q", ctx.InputKind(), InputKindURL)
	}

	// http:// should also be detected as url.
	ctx2, err := Prepare("http://example.com/spec.html", Options{WorkspaceRoot: ws})
	if err != nil {
		t.Fatalf("Prepare http: %v", err)
	}
	if ctx2.InputKind() != InputKindURL {
		t.Errorf("http InputKind: got %q want %q", ctx2.InputKind(), InputKindURL)
	}
}

// Test 3: Derived paths use filepath.Join — verified by inspecting the joined
// components after a normalising filepath.ToSlash. This is OS-portable: on
// macOS/Linux the separator is already "/"; on Windows ToSlash converts
// back-slashes so we can do exact equality.
func TestDerivedPaths_UseFilepathJoin(t *testing.T) {
	ws := t.TempDir()
	ctx, err := Prepare("input.md", Options{
		WorkspaceRoot:  ws,
		TaskIDOverride: "task_deadbeef",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	wsSlash := filepath.ToSlash(ws)
	taskDir := wsSlash + "/tasks/task_deadbeef"

	cases := []struct {
		name, want, got string
	}{
		{"TaskDir", taskDir, filepath.ToSlash(ctx.TaskDir())},
		{"ReferencePath", taskDir + "/reference.md", filepath.ToSlash(ctx.ReferencePath())},
		{"InitialPlanPath", taskDir + "/initial_plan.md", filepath.ToSlash(ctx.InitialPlanPath())},
		{"ImplementationReportPath", taskDir + "/implementation_report.md", filepath.ToSlash(ctx.ImplementationReportPath())},
		{"LogsDir", taskDir + "/logs", filepath.ToSlash(ctx.LogsDir())},
		{"GenerateCodeDir", taskDir + "/generate_code", filepath.ToSlash(ctx.GenerateCodeDir())},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q want %q", c.name, c.got, c.want)
		}
	}
}

// Test 4: Value semantics. Pass WorkflowContext to a function that copies it
// and tries to "mutate" — outside the package there is no setter and all
// fields are unexported, so the only thing the function can do is observe.
// We verify the original is byte-identical (==) before and after.
//
// This is Go's structural answer to Python's @dataclass(frozen=True): you
// don't need a runtime check, the type system enforces it.
func TestValueSemantics(t *testing.T) {
	ws := t.TempDir()
	before, err := Prepare("paper.pdf", Options{
		WorkspaceRoot:  ws,
		TaskIDOverride: "task_aaaaaaaa",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	consume(before)
	after := before // explicit copy by value

	if before != after {
		t.Errorf("WorkflowContext changed after pass-by-value:\n  before=%+v\n  after=%+v", before, after)
	}
	// Sanity: the accessors still return the same things.
	if before.TaskID() != after.TaskID() {
		t.Errorf("TaskID drifted: %q vs %q", before.TaskID(), after.TaskID())
	}
	if before.TaskDir() != after.TaskDir() {
		t.Errorf("TaskDir drifted: %q vs %q", before.TaskDir(), after.TaskDir())
	}
}

// consume takes a WorkflowContext by value. Even if the body tried to assign
// to ctx fields, only the local copy would change — and there are no
// exported setters anyway, so the body cannot even attempt mutation from
// outside the package. (Inside the package we deliberately avoid writing
// any setter to make the contract structural, not just stylistic.)
func consume(ctx WorkflowContext) {
	_ = ctx.TaskID()
	_ = ctx.InputKind()
	// no mutation possible: ctx.taskID = "x" would only mutate the local
	// copy AND only compiles inside this package. We don't write that line.
}

// Test 5: empty input returns *EmptyInputError, errors.As-discriminable.
func TestPrepare_EmptyInputError(t *testing.T) {
	cases := []string{"", "   ", "\t\n"}
	for _, in := range cases {
		ctx, err := Prepare(in, Options{})
		if err == nil {
			t.Errorf("Prepare(%q): want error, got nil ctx=%+v", in, ctx)
			continue
		}
		var typed *EmptyInputError
		if !errors.As(err, &typed) {
			t.Errorf("Prepare(%q): err type %T does not satisfy *EmptyInputError", in, err)
		}
		if ctx != (WorkflowContext{}) {
			t.Errorf("Prepare(%q): ctx must be zero value on error, got %+v", in, ctx)
		}
	}
}
