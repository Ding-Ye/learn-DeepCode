// File: runtime_test.go — five hermetic tests for the planning runtime.
//
// Every test uses t.TempDir() exclusively for its task directory; nothing
// touches the network, the home directory, or shared global state.
package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// Test 1: validate_happy_path — a 5-section text returns nil/empty.
func TestValidatePlanText_HappyPath(t *testing.T) {
	plan := `# Plan
file_structure:
  - main.go
implementation_components:
  - parser
validation_approach:
  - go test
environment_setup:
  - go 1.23
implementation_strategy:
  - top-down
`
	missing := ValidatePlanText(plan)
	if len(missing) != 0 {
		t.Errorf("expected no missing sections, got %v", missing)
	}
}

// Test 2: validate_missing — text missing one section returns the slice with
// that one name.
func TestValidatePlanText_Missing(t *testing.T) {
	// Drop "validation_approach" deliberately.
	plan := `file_structure: x
implementation_components: y
environment_setup: z
implementation_strategy: w`
	missing := ValidatePlanText(plan)
	want := []string{"validation_approach"}
	if !reflect.DeepEqual(missing, want) {
		t.Errorf("missing: got %v want %v", missing, want)
	}

	// Also: case-insensitive — uppercase keys still count.
	planUpper := `FILE_STRUCTURE: x
IMPLEMENTATION_COMPONENTS: y
VALIDATION_APPROACH: ok
ENVIRONMENT_SETUP: z
IMPLEMENTATION_STRATEGY: w`
	if got := ValidatePlanText(planUpper); len(got) != 0 {
		t.Errorf("uppercase plan: expected no missing, got %v", got)
	}
}

// Test 3: atomic_write — simulate a Sync failure. Target file must be
// untouched; the .tmp file must NOT remain on disk.
func TestAtomicWrite_FailureMidWrite(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/checkpoint.json"

	// Pre-populate target with a known value so we can prove it's untouched.
	original := []byte(`{"old":"value"}`)
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	// Writer that fails on sync.
	failingSync := func(*os.File) error { return errors.New("simulated sync failure") }

	err := atomicWriteJSONWithSync(context.Background(), target,
		map[string]string{"new": "should-not-land"}, failingSync)
	if err == nil {
		t.Fatalf("expected error from failing sync, got nil")
	}

	// Original target untouched.
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("target mutated: got %q want %q", got, original)
	}

	// .tmp file removed.
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected .tmp removed, stat err=%v", err)
	}
}

// Test 4: jsonl_append — two goroutines append concurrently. ReadAllJSONL
// returns 2 valid entries.
func TestAppendJSONL_Concurrent(t *testing.T) {
	dir := t.TempDir()
	rt := &PlanningRuntime{}
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)

	go func() {
		defer wg.Done()
		errs <- rt.RecordAttempt(ctx, dir, Attempt{
			Provider: "anthropic", Model: "claude-x", OK: true, PlanBytes: 100,
		})
	}()
	go func() {
		defer wg.Done()
		errs <- rt.RecordAttempt(ctx, dir, Attempt{
			Provider: "openai", Model: "gpt-x", OK: false, Error: "boom", PlanBytes: 50,
		})
	}()
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("RecordAttempt: %v", e)
		}
	}

	got, err := ReadAllJSONL[Attempt](PlanningPaths(dir).Attempts)
	if err != nil {
		t.Fatalf("ReadAllJSONL: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d (%+v)", len(got), got)
	}

	// Order is non-deterministic but the set of providers must be exact.
	providers := []string{got[0].Provider, got[1].Provider}
	sort.Strings(providers)
	want := []string{"anthropic", "openai"}
	if !reflect.DeepEqual(providers, want) {
		t.Errorf("providers: got %v want %v", providers, want)
	}
	for _, e := range got {
		if e.At.IsZero() {
			t.Errorf("attempt has zero At timestamp: %+v", e)
		}
	}
}

// Test 5: is_existing_plan_usable — meta+checkpoint with success → true;
// missing meta → false; meta with status!=success → false.
func TestIsExistingPlanUsable(t *testing.T) {
	dir := t.TempDir()
	rt := &PlanningRuntime{}
	ctx := context.Background()

	// Empty dir → not usable.
	if rt.IsExistingPlanUsable(dir) {
		t.Errorf("empty dir: expected false, got true")
	}

	// Meta only (no checkpoint) → not usable.
	if err := rt.WriteMeta(ctx, dir, Meta{
		Status: "success", Provider: "p", Model: "m", PlanChars: 1024,
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if rt.IsExistingPlanUsable(dir) {
		t.Errorf("meta only: expected false, got true")
	}

	// Meta + checkpoint, status=success → usable.
	if err := rt.WriteCheckpoint(ctx, dir, Checkpoint{
		Phase: "code_planning", Attempt: 1, Mode: "fresh",
	}); err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}
	if !rt.IsExistingPlanUsable(dir) {
		t.Errorf("meta+checkpoint success: expected true, got false")
	}

	// Overwrite meta with status=failed → not usable again.
	if err := rt.WriteMeta(ctx, dir, Meta{
		Status: "failed", Provider: "p", Model: "m",
	}); err != nil {
		t.Fatalf("WriteMeta failed-status: %v", err)
	}
	if rt.IsExistingPlanUsable(dir) {
		t.Errorf("meta status=failed: expected false, got true")
	}
}
