// File: jsonl.go — append-only JSONL log helper.
//
// One JSON object per line, newline-terminated. The append is serialized by
// a process-local sync.Mutex keyed by absolute path. This makes concurrent
// appends from multiple goroutines safe within one process, but does NOT
// protect against concurrent writers from different processes — for that
// you need an OS-level file lock (flock on Unix; LockFileEx on Windows).
// See Appendix B exercise #4 for the cross-process upgrade.
//
// Upstream counterpart: workflows/planning_runtime.py:66-71 (append_jsonl).
// Upstream relies on the GIL for line atomicity; we use an explicit mutex
// because Go has no global lock.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// jsonlLocks holds one mutex per absolute file path. Looking up the same
// path always returns the same mutex, so two goroutines targeting the same
// JSONL file serialize, but two goroutines targeting different files can
// run in parallel.
var (
	jsonlLocksMu sync.Mutex
	jsonlLocks   = map[string]*sync.Mutex{}
)

func lockFor(path string) *sync.Mutex {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path // fall back to whatever the caller gave us
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

// AppendJSONL marshals v to JSON and appends it as one newline-terminated
// line to the file at path, creating parent directories as needed. The
// append is serialized by a process-local mutex keyed on the absolute path.
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

// ReadAllJSONL reads every line of the file at path and decodes each line
// into a value of type T. Used by tests to verify what was appended; not
// meant for streaming large logs (it loads everything into memory).
func ReadAllJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read jsonl %s: open: %w", path, err)
	}
	defer f.Close()

	var out []T
	scanner := bufio.NewScanner(f)
	// Allow long lines (default is 64KB; planner attempts can exceed that).
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, fmt.Errorf("read jsonl %s: line %d: %w", path, lineNo, err)
		}
		out = append(out, v)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read jsonl %s: scan: %w", path, err)
	}
	return out, nil
}
