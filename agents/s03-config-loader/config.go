// Package main — s03-config-loader.
//
// Config struct tree mirroring deepcode_config.json. Every JSON tag uses the
// upstream camelCase spelling (apiKey, maxTokens, ...). Phase-override fields
// are pointer types so Resolve() can distinguish "absent" from "explicit zero".
//
// Upstream counterpart: core/config.py (the Pydantic BaseSettings tree).
package main

import "encoding/json"

// Config is the root parsed shape of deepcode_config.json. Only the subset
// the curriculum exercises is modelled; unknown keys are ignored by
// json.Unmarshal so newer upstream fields don't break us.
type Config struct {
	Agents               AgentsConfig               `json:"agents"`
	Providers            map[string]ProviderConfig  `json:"providers"`
	Tools                ToolsConfig                `json:"tools"`
	Workspace            WorkspaceConfig            `json:"workspace"`
	DocumentSegmentation DocumentSegmentationConfig `json:"documentSegmentation"`
	Logger               LoggerConfig               `json:"logger"`
}

// AgentsConfig holds the defaults and the per-phase override blocks.
//
// Upstream's AgentsConfig declares planning + implementation as fixed
// properties; we widen to a map so a learner can add new phase names
// (e.g. "review") without touching this struct. The two well-known phase
// names are still the canonical ones the runner asks for.
type AgentsConfig struct {
	Defaults AgentDefaults         `json:"defaults"`
	Phases   map[string]AgentPhase `json:"-"` // populated from sibling keys
}

// UnmarshalJSON splits the JSON object's keys: "defaults" goes into Defaults,
// every other key (planning, implementation, ...) is treated as an AgentPhase.
// This matches upstream's flat layout where "planning" and "implementation"
// are top-level keys inside "agents".
func (a *AgentsConfig) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	a.Phases = map[string]AgentPhase{}
	for k, v := range raw {
		if k == "defaults" {
			if err := json.Unmarshal(v, &a.Defaults); err != nil {
				return err
			}
			continue
		}
		var p AgentPhase
		if err := json.Unmarshal(v, &p); err != nil {
			return err
		}
		a.Phases[k] = p
	}
	return nil
}

// AgentDefaults holds the values every phase inherits unless overridden.
// All fields are concrete types — they always have a value (zero or set).
type AgentDefaults struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	MaxTokens   int     `json:"maxTokens"`
	Temperature float64 `json:"temperature"`
}

// AgentPhase carries optional overrides. Pointers let Resolve() detect
// "field not present in JSON" — a non-pointer would conflate that with
// "field present and equal to the zero value", which upstream's Pydantic
// model distinguishes via Optional[T] = None.
type AgentPhase struct {
	Provider    *string  `json:"provider,omitempty"`
	Model       *string  `json:"model,omitempty"`
	MaxTokens   *int     `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

// ProviderConfig is one entry under "providers". apiKey may be a literal
// secret or a "${ENV_VAR}" reference resolved by Load().
type ProviderConfig struct {
	APIKey       string            `json:"apiKey,omitempty"`
	BaseURL      string            `json:"apiBase,omitempty"`
	ExtraHeaders map[string]string `json:"extraHeaders,omitempty"`
}

// ToolsConfig — only the subset s03 uses; mcpServers is preserved as
// a raw map so callers can iterate without us redeclaring the MCP schema.
type ToolsConfig struct {
	DefaultSearchServer string                     `json:"defaultSearchServer"`
	MCPServers          map[string]json.RawMessage `json:"mcpServers,omitempty"`
}

// WorkspaceConfig — root directory for tasks/outputs. Used by s05+.
type WorkspaceConfig struct {
	Root       string `json:"root"`
	MaxInputMB int    `json:"maxInputMb,omitempty"`
}

// DocumentSegmentationConfig — when to chunk long inputs (used by upstream
// L4 phase; included so our struct round-trips the example JSON faithfully).
type DocumentSegmentationConfig struct {
	Enabled            bool `json:"enabled"`
	SizeThresholdChars int  `json:"sizeThresholdChars,omitempty"`
}

// LoggerConfig — minimal subset (level + transports). Real DeepCode has
// nested global/task/llm sinks; we model just what s03 tests need.
type LoggerConfig struct {
	Level      string   `json:"level,omitempty"`
	Transports []string `json:"transports,omitempty"`
}
