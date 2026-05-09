// File: factory.go — `NewProviderFromSettings` picks the concrete Provider
// implementation that matches the resolved AgentSettings from s03.
//
// Why a redeclared AgentSettings? Sessions are isolated: each agents/sNN-
// has its own go.mod and never imports another session. We copy the subset
// of s03's `AgentSettings` shape we actually use here (Provider, Model,
// MaxTokens, Temperature) plus two fields s03 sets via expandEnv on the
// providers block: APIKey + BaseURL.
//
// Upstream counterpart: `core/config.py:make_llm_provider` (L552-L618) and
// `core/providers/registry.py:find_by_name` / `find_by_model` — both feed
// into the same factory step. The selection rule below mirrors
// `find_by_model`'s keyword scan but stays at two backends.
package main

import (
	"errors"
	"strings"
)

// AgentSettings is the subset of s03's resolved settings this package
// consumes. Redeclared rather than imported per the project's session-
// isolation rule.
type AgentSettings struct {
	Provider    string  // "anthropic" | "openai" | "auto" | "" (any)
	Model       string  // e.g. "claude-sonnet-4-20250514", "gpt-4o-mini"
	MaxTokens   int
	Temperature float64
	APIKey      string  // resolved (env-expanded) credential
	BaseURL     string  // optional override; empty falls back to provider default
}

// ErrMissingAPIKey is returned when the resolved settings have an empty
// APIKey and the chosen backend cannot operate without one.
var ErrMissingAPIKey = errors.New("provider settings have empty APIKey")

// NewProviderFromSettings selects the right Provider implementation for
// `s` and returns it ready to call. The selection rule:
//
//  1. If s.Provider explicitly names "anthropic" or s.Model contains
//     "claude" / "anthropic", use AnthropicProvider.
//  2. Otherwise use OpenAIProvider (the catch-all for OpenAI-compatible
//     gateways: OpenAI proper, OpenRouter, DeepSeek, Gemini-compat, ...).
//
// BaseURL on the returned provider is overridden iff s.BaseURL is non-empty,
// preserving the public defaults from NewAnthropicProvider /
// NewOpenAIProvider when the config left the field blank.
func NewProviderFromSettings(s AgentSettings) (Provider, error) {
	if s.APIKey == "" {
		return nil, ErrMissingAPIKey
	}
	if isAnthropicSettings(s) {
		p := NewAnthropicProvider(s.APIKey)
		if s.BaseURL != "" {
			p.BaseURL = s.BaseURL
		}
		return p, nil
	}
	p := NewOpenAIProvider(s.APIKey)
	if s.BaseURL != "" {
		p.BaseURL = s.BaseURL
	}
	return p, nil
}

// isAnthropicSettings encodes the routing rule. Three signals can flip it:
//
//   - explicit Provider field == "anthropic"
//   - Model substring "claude" (covers "claude-sonnet-4-20250514",
//     "anthropic/claude-haiku-4-5-20251001", and any future Claude family)
//   - Model substring "anthropic" (covers the "anthropic/claude-..."
//     OpenRouter-style prefix)
func isAnthropicSettings(s AgentSettings) bool {
	provider := strings.ToLower(strings.TrimSpace(s.Provider))
	if provider == "anthropic" {
		return true
	}
	model := strings.ToLower(s.Model)
	return strings.Contains(model, "claude") || strings.Contains(model, "anthropic")
}
