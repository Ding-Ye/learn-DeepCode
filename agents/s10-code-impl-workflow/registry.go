// Package main — s10-code-impl-workflow.
//
// File: registry.go — minimal Tool registry. Just the three operations the
// workflow's per-file body needs (NewRegistry / Register / Get) plus a List
// helper that returns sorted schemas for the runner to advertise.
//
// Per the session-isolation rule, this is a deliberate redeclaration of
// s02's registry. No closer lifecycle, no MCP-vs-builtin ordering — those
// are s02's specialty. Re-registering a name overwrites (last write wins).
package main

import "sort"

// Registry holds the tools the workflow's runner can dispatch.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns an empty registry ready for Register calls.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register stores t under t.Name(). Re-registering a name overwrites.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get returns the tool by name. ok==false means the name is unknown.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// List returns the schema slice the runner sends to the model, sorted by
// name for stability across runs.
func (r *Registry) List() []ToolSchema {
	out := make([]ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Schema())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
