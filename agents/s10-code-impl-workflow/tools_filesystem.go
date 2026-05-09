// Package main — s10-code-impl-workflow.
//
// File: tools_filesystem.go — three small Tool implementations the per-file
// workflow body registers in its Registry: read_file, write_file, and a
// stub execute_python.
//
// Real upstream goes through MCP (`tools/code_implementation_server.py`)
// and supports many more tools; the teaching cut keeps just the three
// names that exercise every code path of the workflow:
//   - write_file is the file-completion signal (LoopDetector.RecordSuccess)
//   - read_file is essential-tool material for s09's Compact whitelist
//   - execute_python is a no-op stub returning "OK [stub]" — the point
//     isn't real execution; it's that the runner can dispatch a non-fs tool.
//
// All tools accept a JSON object {file_path: "..."} or {code: "..."}.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// readFileTool implements `read_file`. Treats file_path as relative to a
// configured workspace root (the workflow sets it to taskDir/generate_code/).
type readFileTool struct{ Workspace string }

func (t *readFileTool) Name() string { return "read_file" }

func (t *readFileTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "read_file",
		Description: "Read the full contents of a file relative to the workspace.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}`),
	}
}

func (t *readFileTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	full := filepath.Join(t.Workspace, p.FilePath)
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p.FilePath, err)
	}
	return string(data), nil
}

// writeFileTool implements `write_file`. Creates parent directories and
// writes content to workspace/file_path. Returns "wrote N bytes to <path>".
type writeFileTool struct{ Workspace string }

func (t *writeFileTool) Name() string { return "write_file" }

func (t *writeFileTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "write_file",
		Description: "Write content to a file relative to the workspace.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"content":{"type":"string"}},"required":["file_path","content"]}`),
	}
}

func (t *writeFileTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	full := filepath.Join(t.Workspace, p.FilePath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(p.FilePath), err)
	}
	if err := os.WriteFile(full, []byte(p.Content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", p.FilePath, err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(p.Content), p.FilePath), nil
}

// executePythonTool is a no-op stub. The runner dispatch path is the same
// as for any other tool; the body just returns "OK [stub]". Real upstream
// shells out to a sandboxed interpreter — out of scope for the teaching cut.
type executePythonTool struct{}

func (executePythonTool) Name() string { return "execute_python" }

func (executePythonTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "execute_python",
		Description: "Execute Python code (stub: always returns OK).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"}},"required":["code"]}`),
	}
}

func (executePythonTool) Run(_ context.Context, _ json.RawMessage) (string, error) {
	return "OK [stub]", nil
}

// registerFileScopedTools populates reg with the three tools above, scoped
// to the given workspace directory. Called once per file by the workflow.
func registerFileScopedTools(reg *Registry, workspace string) {
	reg.Register(&readFileTool{Workspace: workspace})
	reg.Register(&writeFileTool{Workspace: workspace})
	reg.Register(executePythonTool{})
}
