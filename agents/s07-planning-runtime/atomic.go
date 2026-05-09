// File: atomic.go — atomic JSON write via tmp+fsync+rename.
//
// On POSIX, rename(2) within a directory is atomic — readers see either the
// old file or the new file, never a half-written one. Combine that with an
// fsync on the temp file before rename and you get durable, atomic writes
// that don't corrupt the target if the process crashes mid-write.
//
// Upstream counterpart: workflows/planning_runtime.py:44-52 (write_json).
// Upstream uses pathlib's tmp.replace(target); this is the same primitive.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteJSON marshals v as indented JSON and writes it atomically to
// path. The sequence is: marshal → mkdir parent → create sibling .tmp →
// write+sync → rename .tmp → path. On any error before the rename, the .tmp
// file is removed and the target at path (if any) is untouched.
//
// The ctx parameter is accepted for API consistency with the rest of the
// learn-DeepCode codebase even though no syscall here honors cancellation —
// when a future caller switches to context-aware I/O it won't need to change
// the signature.
func AtomicWriteJSON(ctx context.Context, path string, v any) error {
	_ = ctx // consistency only; no cancellable syscalls below

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("atomic write %s: marshal: %w", path, err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("atomic write %s: mkdir parent: %w", path, err)
	}

	tmp := path + ".tmp"
	// Use O_TRUNC so a stale .tmp from a prior crash is overwritten cleanly.
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

// atomicWriteJSONWithSync is a test-helper variant that lets callers inject
// a custom sync step to simulate a mid-write failure. It is unexported on
// purpose — production code should always use AtomicWriteJSON.
func atomicWriteJSONWithSync(ctx context.Context, path string, v any, sync func(*os.File) error) error {
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
	if err := sync(f); err != nil {
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
