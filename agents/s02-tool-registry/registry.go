package main

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// Registry holds the tools an agent can call, keyed by Name().
//
// Concurrency: methods are safe for concurrent use. The cached schema slice
// is rebuilt under the same mutex on next List() after a Register/Unregister.
//
// Lifetime: any registered tool that implements CloserTool is closed by the
// registry's Close() method. Errors from individual closers are collected
// into a joined error so a partial failure doesn't hide a later one.
type Registry struct {
	mu      sync.Mutex
	tools   map[string]Tool
	cached  []ToolSchema // nil = invalidated; len == 0 with non-nil = empty cache
	closers []CloserTool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register stores t. If a tool with the same name was already registered,
// the new value replaces it (matches upstream's "last write wins" semantics).
// Schema cache is invalidated.
//
// If t implements CloserTool, its Close() will be called by Registry.Close.
// Re-registering a CloserTool with the same name does NOT remove the old one
// from the closer list — both will be closed (the old one's Close is still
// the contract we promised to honor).
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
	r.cached = nil
	if c, ok := t.(CloserTool); ok {
		r.closers = append(r.closers, c)
	}
}

// Unregister removes by name. Schema cache invalidated. Closers are NOT
// removed (consistent with Register: the contract is "we'll close every
// CloserTool we ever saw").
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
	r.cached = nil
}

// Get returns the tool by name. The bool is false (and Tool is nil) when
// the name is unknown — same shape as a map lookup.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tools[name]
	return t, ok
}

// Has is a convenience for `_, ok := Get(name); return ok`.
func (r *Registry) Has(name string) bool {
	_, ok := r.Get(name)
	return ok
}

// Len returns the number of registered tools.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tools)
}

// List returns the cached schema slice. Builtins (name not prefixed with
// "mcp_") come first, alphabetically; mcp_ tools come second, alphabetically.
// This matches upstream's get_definitions() ordering.
func (r *Registry) List() []ToolSchema {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cached != nil {
		out := make([]ToolSchema, len(r.cached))
		copy(out, r.cached)
		return out
	}
	builtins := make([]ToolSchema, 0, len(r.tools))
	mcps := make([]ToolSchema, 0)
	for _, t := range r.tools {
		s := t.Schema()
		if strings.HasPrefix(s.Name, "mcp_") {
			mcps = append(mcps, s)
		} else {
			builtins = append(builtins, s)
		}
	}
	sort.Slice(builtins, func(i, j int) bool { return builtins[i].Name < builtins[j].Name })
	sort.Slice(mcps, func(i, j int) bool { return mcps[i].Name < mcps[j].Name })
	r.cached = append(builtins, mcps...)
	out := make([]ToolSchema, len(r.cached))
	copy(out, r.cached)
	return out
}

// Names returns just the names, in the same order as List.
func (r *Registry) Names() []string {
	out := make([]string, 0)
	for _, s := range r.List() {
		out = append(out, s.Name)
	}
	return out
}

// Close invokes Close() on every CloserTool ever registered (even those
// later overwritten or unregistered). Errors are joined; a partial failure
// does not stop subsequent closers from running.
func (r *Registry) Close() error {
	r.mu.Lock()
	closers := r.closers
	r.closers = nil
	r.mu.Unlock()
	var errs []error
	for _, c := range closers {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
