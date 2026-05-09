// File: resolve.go — overlay a phase override on top of agents.defaults.
//
// Resolve() answers the question every downstream caller cares about: "for
// phase X, what model / provider / token budget should I use?" It does the
// upstream `_pick(name)` algorithm field-by-field, no reflect, no surprises.
package main

// AgentSettings is the resolved (phase + defaults) view. Concrete types
// only — every field is guaranteed present after Resolve.
//
// Upstream calls this `ResolvedAgentSettings`; we shorten the name because
// we never need to qualify "resolved" vs "raw" elsewhere in the package.
type AgentSettings struct {
	Provider    string
	Model       string
	MaxTokens   int
	Temperature float64
}

// Resolve returns the AgentSettings for the named phase (e.g. "planning",
// "implementation", or any other key the user added under "agents"). When
// the phase is absent, defaults are returned unchanged.
//
// Determinism: Resolve is a pure function of the receiver — calling it
// twice with the same phase yields == structs (the test suite asserts
// this with reflect.DeepEqual).
func (c *Config) Resolve(phase string) AgentSettings {
	d := c.Agents.Defaults
	out := AgentSettings{
		Provider:    d.Provider,
		Model:       d.Model,
		MaxTokens:   d.MaxTokens,
		Temperature: d.Temperature,
	}
	override, ok := c.Agents.Phases[phase]
	if !ok {
		return out
	}
	if override.Provider != nil {
		out.Provider = *override.Provider
	}
	if override.Model != nil {
		out.Model = *override.Model
	}
	if override.MaxTokens != nil {
		out.MaxTokens = *override.MaxTokens
	}
	if override.Temperature != nil {
		out.Temperature = *override.Temperature
	}
	return out
}
