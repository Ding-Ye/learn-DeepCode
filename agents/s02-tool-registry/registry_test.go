package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Test 1: Register / Get / List round-trip + schema cache invalidation.
func TestRegistry_RegisterGetList(t *testing.T) {
	r := NewRegistry()
	if r.Len() != 0 {
		t.Errorf("empty Len: got %d", r.Len())
	}
	r.Register(NewEchoTool())
	r.Register(NewNowTool())
	if r.Len() != 2 {
		t.Errorf("Len after Register: got %d want 2", r.Len())
	}

	tool, ok := r.Get("echo")
	if !ok || tool.Name() != "echo" {
		t.Errorf("Get echo: ok=%v tool=%v", ok, tool)
	}
	if !r.Has("now") {
		t.Errorf("Has(now): false")
	}

	// List warms cache; second call must equal the first (deterministic order).
	first := r.List()
	second := r.List()
	if len(first) != len(second) {
		t.Fatalf("len mismatch on cached calls: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Name != second[i].Name {
			t.Errorf("cached order changed at %d: %q vs %q", i, first[i].Name, second[i].Name)
		}
	}

	// Adding a tool must invalidate the cache (we observe by length growing).
	r.Register(NewMCPSubprocessTool("demo"))
	third := r.List()
	if len(third) != 3 {
		t.Errorf("after Register: got len %d want 3", len(third))
	}
}

// Test 2: List() ordering — builtins (alphabetical) before mcp_ (alphabetical).
func TestRegistry_ListOrdering(t *testing.T) {
	r := NewRegistry()
	r.Register(NewMCPSubprocessTool("z"))
	r.Register(NewNowTool())
	r.Register(NewMCPSubprocessTool("a"))
	r.Register(NewEchoTool())

	got := []string{}
	for _, s := range r.List() {
		got = append(got, s.Name)
	}
	want := []string{"echo", "now", "mcp_a", "mcp_z"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order: got %v want %v", got, want)
	}
}

// Test 3: Close() invokes every CloserTool exactly once and joins errors.
func TestRegistry_CloseAggregatesErrors(t *testing.T) {
	a := NewMCPSubprocessTool("a")
	b := NewMCPSubprocessTool("b")
	calls := 0
	bErr := errors.New("b failed")
	a.CloseHook = func() error { calls++; return nil }
	b.CloseHook = func() error { calls++; return bErr }

	r := NewRegistry()
	r.Register(a)
	r.Register(b)
	r.Register(NewEchoTool()) // not a CloserTool — must not be touched

	if err := r.Close(); err == nil || !errors.Is(err, bErr) {
		t.Errorf("Close: want err containing bErr, got %v", err)
	}
	if calls != 2 {
		t.Errorf("close calls: got %d want 2", calls)
	}

	// Second Close is a no-op (closers slice was drained).
	if err := r.Close(); err != nil {
		t.Errorf("second Close: got err %v want nil", err)
	}
	if calls != 2 {
		t.Errorf("second Close called Close hooks again: %d", calls)
	}
}

// Test 4: re-registering replaces and invalidates cache.
func TestRegistry_ReRegisterReplaces(t *testing.T) {
	r := NewRegistry()
	r.Register(NewEchoTool())

	first := r.List()
	if len(first) != 1 {
		t.Fatalf("len: got %d want 1", len(first))
	}
	firstDesc := first[0].Description

	// Custom tool with same name "echo" but different description.
	r.Register(&customEcho{desc: "REPLACED"})
	second := r.List()
	if len(second) != 1 {
		t.Fatalf("len: got %d want 1 after replace", len(second))
	}
	if second[0].Description == firstDesc {
		t.Errorf("description not replaced: still %q", second[0].Description)
	}
	if second[0].Description != "REPLACED" {
		t.Errorf("description: got %q want REPLACED", second[0].Description)
	}
}

// Test 5: Get of unknown name returns (nil, false); Run dispatch works for known.
func TestRegistry_GetUnknownAndRun(t *testing.T) {
	r := NewRegistry()
	r.Register(NewEchoTool())
	if tool, ok := r.Get("does-not-exist"); ok || tool != nil {
		t.Errorf("Get unknown: tool=%v ok=%v", tool, ok)
	}
	tool, ok := r.Get("echo")
	if !ok {
		t.Fatalf("Get echo: ok=false")
	}
	out, err := tool.Run(context.Background(), json.RawMessage(`{"text":"ping"}`))
	if err != nil {
		t.Fatalf("Run echo: %v", err)
	}
	if out != "ping" {
		t.Errorf("Run echo: got %q want ping", out)
	}
}

// customEcho is a test-local tool with the same Name but different Schema.
type customEcho struct{ desc string }

func (c *customEcho) Name() string { return "echo" }
func (c *customEcho) Schema() ToolSchema {
	return ToolSchema{Name: "echo", Description: c.desc,
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`)}
}
func (c *customEcho) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return "custom-" + c.desc, nil
}
