// Package main — s06-tool-capable-runner.
//
// File: registry.go — minimal Tool registry, just enough to satisfy the
// runner's dispatch path (Register + Get + List). No Close() lifecycle or
// builtins-vs-mcp ordering: that's s02's specialty. The runner here only
// needs name→tool lookup plus a schema list to advertise to the model.
//
// Re-registration is "last write wins" — same semantics as s02.
package main

import "sort"

// Registry holds the tools the runner can dispatch.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns an empty registry ready for Register calls.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register stores t under t.Name(). Re-registering a name overwrites the
// previous tool — matches upstream and s02.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get returns the tool by name. ok==false means the name is unknown.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// List returns the schema slice the runner sends to the model, sorted by
// name for stability.
func (r *Registry) List() []ToolSchema {
	out := make([]ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Schema())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
