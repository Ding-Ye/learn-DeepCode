// File: load_test.go — five hermetic tests for Load + Resolve.
//
// Tests must not touch the network and must not depend on the host's
// environment beyond what they explicitly set with t.Setenv (which
// auto-restores on test exit).
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Test 1: parse the golden config and check top-level structure.
func TestLoad_GoldenConfigStructure(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-OPENAI") // referenced by the golden
	cfg, err := Load(context.Background(), "testdata/deepcode_config.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.Defaults.Model == "" {
		t.Errorf("defaults.model: empty")
	}
	if got := cfg.Agents.Defaults.MaxTokens; got != 40000 {
		t.Errorf("defaults.maxTokens: got %d want 40000", got)
	}
	if _, ok := cfg.Agents.Phases["planning"]; !ok {
		t.Errorf("phases.planning: missing")
	}
	if _, ok := cfg.Agents.Phases["implementation"]; !ok {
		t.Errorf("phases.implementation: missing")
	}
	if cfg.Tools.DefaultSearchServer != "filesystem" {
		t.Errorf("tools.defaultSearchServer: got %q want filesystem",
			cfg.Tools.DefaultSearchServer)
	}
	if cfg.Workspace.Root != "./deepcode_lab" {
		t.Errorf("workspace.root: got %q", cfg.Workspace.Root)
	}
	if _, ok := cfg.Providers["openai"]; !ok {
		t.Errorf("providers.openai: missing")
	}
	if _, ok := cfg.Providers["anthropic"]; !ok {
		t.Errorf("providers.anthropic: missing")
	}
}

// Test 2: ${OPENAI_API_KEY} is substituted from the process environment.
func TestLoad_EnvSubstitution(t *testing.T) {
	t.Setenv("TEST_VAR", "secret-value-42")
	cfg, err := Load(context.Background(), "testdata/with_env.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Providers["openai"].APIKey
	if got != "secret-value-42" {
		t.Errorf("apiKey: got %q want secret-value-42", got)
	}
}

// Test 3: phase override merge — planning overrides model + maxTokens,
// implementation overrides only temperature, missing fields fall through.
func TestResolve_PhaseOverrides(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "x")
	cfg, err := Load(context.Background(), "testdata/deepcode_config.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	planning := cfg.Resolve("planning")
	wantPlanning := AgentSettings{
		Provider:    "auto",                                // from defaults
		Model:       "anthropic/claude-sonnet-4-20250514", // overridden
		MaxTokens:   32000,                                 // overridden
		Temperature: 0.1,                                   // from defaults
	}
	if !reflect.DeepEqual(planning, wantPlanning) {
		t.Errorf("planning: got %+v want %+v", planning, wantPlanning)
	}

	impl := cfg.Resolve("implementation")
	wantImpl := AgentSettings{
		Provider:    "auto",
		Model:       "openai/gpt-5.4",
		MaxTokens:   40000,
		Temperature: 0.2, // overridden
	}
	if !reflect.DeepEqual(impl, wantImpl) {
		t.Errorf("implementation: got %+v want %+v", impl, wantImpl)
	}

	// Phase that doesn't exist → defaults.
	missing := cfg.Resolve("review")
	wantMissing := AgentSettings{
		Provider: "auto", Model: "openai/gpt-5.4",
		MaxTokens: 40000, Temperature: 0.1,
	}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Errorf("unknown phase: got %+v want %+v", missing, wantMissing)
	}
}

// Test 4: a missing env var returns *MissingEnvError, not a generic error.
func TestLoad_MissingEnvVar(t *testing.T) {
	// Build a config that references a variable we are sure is unset.
	const badName = "S03_DEFINITELY_UNSET_VARIABLE_XYZ"
	if _, ok := os.LookupEnv(badName); ok {
		t.Skipf("env %s is unexpectedly set", badName)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	body := `{"agents":{"defaults":{"provider":"auto","model":"x","maxTokens":1,"temperature":0}},` +
		`"providers":{"openai":{"apiKey":"${` + badName + `}"}},` +
		`"tools":{"defaultSearchServer":"filesystem"},` +
		`"workspace":{"root":"./x"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(context.Background(), path)
	if err == nil {
		t.Fatalf("Load: want MissingEnvError, got nil")
	}
	var miss *MissingEnvError
	if !errors.As(err, &miss) {
		t.Fatalf("Load err: want *MissingEnvError, got %T (%v)", err, err)
	}
	if miss.Name != badName {
		t.Errorf("MissingEnvError.Name: got %q want %q", miss.Name, badName)
	}
}

// Test 5: Resolve is idempotent — calling twice yields equal structs.
func TestResolve_Idempotent(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "x")
	cfg, err := Load(context.Background(), "testdata/deepcode_config.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := cfg.Resolve("planning")
	b := cfg.Resolve("planning")
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Resolve not idempotent: %+v vs %+v", a, b)
	}
	if a != b {
		t.Errorf("Resolve struct comparison: %+v != %+v", a, b)
	}
}
