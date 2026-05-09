// Package main — s05-workflow-context.
//
// Prepare is the only constructor for WorkflowContext. It is the single
// place where input-kind detection, task-id allocation, and workspace
// resolution live — keeping side-effects out of the data type itself.
//
// Upstream counterpart: workflows/environment.prepare_workflow_environment
// (referenced in workflow_context.py docstring) — the unified Phase 0+1.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options configures Prepare. All fields are optional; the zero value is
// valid and produces sensible defaults.
type Options struct {
	// WorkspaceRoot, if non-empty, overrides the default `$HOME/.deepcode-learn`.
	// Tests pass `t.TempDir()` here to stay hermetic.
	WorkspaceRoot string

	// TaskIDOverride, if non-empty, replaces the random "task_<hex>" id.
	// Used by tests for deterministic assertions.
	TaskIDOverride string
}

// EmptyInputError is returned when Prepare is called with an empty input.
type EmptyInputError struct{}

func (EmptyInputError) Error() string { return "workflow_context: input source is empty" }

// extensionToKind mirrors upstream's EXTENSION_TO_KIND table verbatim.
// Lowercase keys; the lookup also lowercases the file's extension.
var extensionToKind = map[string]InputKind{
	".pdf":      InputKindPDF,
	".md":       InputKindMD,
	".markdown": InputKindMD,
	".docx":     InputKindDOCX,
	".doc":      InputKindDOCX,
	".txt":      InputKindTXT,
	".html":     InputKindHTML,
	".htm":      InputKindHTML,
}

// detectInputKind classifies the input string. URLs win over extensions
// (some servers hand back .pdf URLs but they're still URLs to fetch).
func detectInputKind(input string) InputKind {
	lower := strings.ToLower(input)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return InputKindURL
	}
	ext := strings.ToLower(filepath.Ext(input))
	if kind, ok := extensionToKind[ext]; ok {
		return kind
	}
	return InputKindUnknown
}

// generateTaskID returns "task_<8 hex chars>" using crypto/rand.
// 4 random bytes hex-encoded = 8 hex chars (32 bits of entropy — plenty
// for collision avoidance within a single workspace).
func generateTaskID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("workflow_context: cannot read random bytes: %w", err)
	}
	return "task_" + hex.EncodeToString(b[:]), nil
}

// resolveWorkspaceRoot picks the workspace root directory. Order:
//  1. opts.WorkspaceRoot (if non-empty)
//  2. $HOME/.deepcode-learn (via os.UserHomeDir)
//
// Note: deliberately simpler than upstream's three-step DEEPCODE_WORKSPACE
// > yaml > cwd resolver. s03 will own env-var interpolation; s05 stays pure.
func resolveWorkspaceRoot(opts Options) (string, error) {
	if opts.WorkspaceRoot != "" {
		return opts.WorkspaceRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("workflow_context: cannot resolve home dir: %w", err)
	}
	return filepath.Join(home, ".deepcode-learn"), nil
}

// Prepare builds a WorkflowContext from an input source string and options.
//
// The input may be a local path (any extension in extensionToKind) or an
// http(s):// URL. Empty input is an error (returned as *EmptyInputError so
// tests can errors.As-check it).
//
// Pure function: NO files are created. Prepare only computes the context.
// The task directory is materialized later by whoever owns I/O (s07's
// planning runtime, s10's workflow). This keeps tests fast and hermetic.
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

// Compile-time assertion: *EmptyInputError satisfies the error interface.
// Callers can `errors.As(err, &workflow.EmptyInputError{})` to discriminate.
var _ error = (*EmptyInputError)(nil)
