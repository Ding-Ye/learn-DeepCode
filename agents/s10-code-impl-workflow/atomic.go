// Package main — s10-code-impl-workflow.
//
// File: atomic.go — minimal atomic JSON write via tmp+sync+rename. Same
// primitive as s07's atomic.go, redeclared per the session-isolation rule.
//
// The sequence is: marshal → mkdir parent → create sibling .tmp →
// write+sync → rename .tmp → path. On any error before the rename, the
// .tmp file is removed and the target is untouched.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteJSON marshals v as indented JSON and writes it atomically.
// ctx is accepted for API consistency with the rest of the codebase.
func AtomicWriteJSON(ctx context.Context, path string, v any) error {
	_ = ctx

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("atomic write %s: marshal: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("atomic write %s: mkdir parent: %w", path, err)
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("atomic write %s: open tmp: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write %s: write: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write %s: sync: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write %s: close: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic write %s: rename: %w", path, err)
	}
	return nil
}
