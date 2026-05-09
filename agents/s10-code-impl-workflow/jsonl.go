// Package main — s10-code-impl-workflow.
//
// File: jsonl.go — minimal append-only JSONL helper. Mirrors s07's jsonl.go
// (process-local mutex per absolute path, one JSON object per line). The
// per-file attempt log uses this primitive: every file the workflow
// touches yields exactly one append.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	jsonlLocksMu sync.Mutex
	jsonlLocks   = map[string]*sync.Mutex{}
)

func lockFor(path string) *sync.Mutex {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	jsonlLocksMu.Lock()
	defer jsonlLocksMu.Unlock()
	m, ok := jsonlLocks[abs]
	if !ok {
		m = &sync.Mutex{}
		jsonlLocks[abs] = m
	}
	return m
}

// AppendJSONL marshals v and appends it as one newline-terminated line.
func AppendJSONL(ctx context.Context, path string, v any) error {
	_ = ctx

	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("append jsonl %s: marshal: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("append jsonl %s: mkdir parent: %w", path, err)
	}

	m := lockFor(path)
	m.Lock()
	defer m.Unlock()

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("append jsonl %s: open: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("append jsonl %s: write: %w", path, err)
	}
	if _, err := f.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("append jsonl %s: write newline: %w", path, err)
	}
	return nil
}
