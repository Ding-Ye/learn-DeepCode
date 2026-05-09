// Package main — s08-loop-detector.
//
// ProgressTracker is the bookkeeping companion to LoopDetector. The detector
// tells you when to abort; the tracker tells you what's been done. They're
// deliberately separate types so you can use either alone (s10 will compose
// both, but a future runner could keep just one).
//
// Upstream counterpart: utils/loop_detector.py:182-253 (`class
// ProgressTracker`). We drop the phase-percent + ETA logic from upstream
// (those are presentation concerns belonging in s10's report layer) and
// keep just the two-counter core: completed file paths (deduplicated) and
// iteration count.
package main

import (
	"strings"
	"sync"
)

// ProgressSnapshot is the read-only view returned by Snapshot(). The Files
// slice is a copy — mutating it does not affect the tracker's internal
// state. Pass by value; it's three fields and a string slice header.
type ProgressSnapshot struct {
	FilesCompleted int
	Iterations     int
	Files          []string // deduplicated, in completion order
}

// ProgressTracker counts completed files (deduplicated by normalized path)
// and iteration ticks. Methods are mutex-guarded so a single tracker can be
// shared by goroutines that report from different files concurrently.
//
// Zero-value is safe — all fields default to their idiomatic zero. There
// is no constructor.
type ProgressTracker struct {
	mu             sync.Mutex
	files          []string        // ordered list of unique completed paths
	completed      map[string]bool // membership for O(1) dedup
	filesCompleted int
	iterations     int
}

// CompleteFile records a successful write of `path`. Returns true on the
// first call for that normalized path; false on duplicates (matching
// upstream's `complete_file` return contract). Normalization mirrors
// upstream: backslashes → forward slashes, surrounding whitespace and
// slashes trimmed. Empty paths are accepted but never deduplicated — they
// always count as a new completion.
func (p *ProgressTracker) CompleteFile(path string) bool {
	normalized := strings.Trim(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "/")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.completed == nil {
		p.completed = make(map[string]bool)
	}
	if normalized != "" && p.completed[normalized] {
		return false
	}
	if normalized != "" {
		p.completed[normalized] = true
		p.files = append(p.files, normalized)
	}
	p.filesCompleted++
	return true
}

// RecordIteration bumps the per-task iteration counter. s10 will call this
// once per pass through the file-by-file outer loop.
func (p *ProgressTracker) RecordIteration() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.iterations++
}

// Snapshot returns a value-type copy of the current state. The Files slice
// is a fresh copy — callers may safely append to or mutate it.
func (p *ProgressTracker) Snapshot() ProgressSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	files := make([]string, len(p.files))
	copy(files, p.files)
	return ProgressSnapshot{
		FilesCompleted: p.filesCompleted,
		Iterations:     p.iterations,
		Files:          files,
	}
}
