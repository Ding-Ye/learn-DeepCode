// Package main — s10-code-impl-workflow.
//
// File: plan.go — read+parse the implementation plan that drives the
// per-file workflow loop.
//
// Upstream supports a YAML-ish plan with five required sections (file_structure,
// implementation_components, validation_approach, environment_setup,
// implementation_strategy). For the teaching cut here we accept a simpler
// JSON shape:
//
//	{"files": ["main.go", "config.go", "handler.go"]}
//
// Real YAML parsing would pull in `gopkg.in/yaml.v3`; we want s10 to be
// dependency-free so the testdata file lives with a `.yaml` extension but
// is JSON-encoded. A future extension can swap LoadPlan for a real YAML
// reader without changing any caller.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Plan is the typed plan content. The only field the workflow needs is the
// ordered list of files to implement. Path strings are relative to the
// task's generate_code/ directory.
type Plan struct {
	Files []string `json:"files"`
}

// LoadPlan reads path as JSON and returns the parsed plan.
//
// Future work: support upstream's YAML format with the five required
// sections. For now we trade fidelity for zero-dependency parsing so this
// session stays self-contained. The .yaml extension on testdata is
// deliberate — readers see "this is the same artefact upstream produces"
// even though the bytes happen to be JSON-encoded.
func LoadPlan(path string) (Plan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, fmt.Errorf("load plan %s: %w", path, err)
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		return Plan{}, fmt.Errorf("load plan %s: parse: %w", path, err)
	}
	if len(p.Files) == 0 {
		return Plan{}, fmt.Errorf("load plan %s: empty files list", path)
	}
	return p, nil
}
