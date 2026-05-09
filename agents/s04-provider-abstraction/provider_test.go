// File: provider_test.go — five offline tests covering decode, factory
// routing, httptest round-trip, and finish-reason normalization.
//
// All fixtures live under testdata/. No live network calls.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// readFixture returns the bytes of a testdata fixture or fails the test.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// Test 1: Anthropic tool_use response decodes into one ToolCallRequest with
// FinishReason="tool_calls".
func TestDecodeAnthropicToolUse(t *testing.T) {
	body := readFixture(t, "anthropic_tool_use.json")
	resp, err := parseAnthropicResponse(body)
	if err != nil {
		t.Fatalf("parseAnthropicResponse: %v", err)
	}
	if got, want := resp.FinishReason, FinishToolCalls; got != want {
		t.Errorf("FinishReason: got %q want %q", got, want)
	}
	if got := len(resp.ToolCalls); got != 1 {
		t.Fatalf("ToolCalls count: got %d want 1", got)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_01ABC123" {
		t.Errorf("ToolCalls[0].ID = %q", tc.ID)
	}
	if tc.Name != "echo" {
		t.Errorf("ToolCalls[0].Name = %q", tc.Name)
	}
	var args struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	if args.Text != "hi" {
		t.Errorf("args.Text = %q want \"hi\"", args.Text)
	}
}

// Test 2: OpenAI tool_calls response decodes into the same canonical shape.
func TestDecodeOpenAIToolCall(t *testing.T) {
	body := readFixture(t, "openai_tool_call.json")
	resp, err := parseOpenAIResponse(body)
	if err != nil {
		t.Fatalf("parseOpenAIResponse: %v", err)
	}
	if got, want := resp.FinishReason, FinishToolCalls; got != want {
		t.Errorf("FinishReason: got %q want %q", got, want)
	}
	if got := len(resp.ToolCalls); got != 1 {
		t.Fatalf("ToolCalls count: got %d want 1", got)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_abc123" {
		t.Errorf("ToolCalls[0].ID = %q", tc.ID)
	}
	if tc.Name != "echo" {
		t.Errorf("ToolCalls[0].Name = %q", tc.Name)
	}
	var args struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	if args.Text != "hi" {
		t.Errorf("args.Text = %q want \"hi\"", args.Text)
	}
}

// Test 3: factory picks Anthropic for "claude-sonnet-4-5", OpenAI for
// "gpt-4o-mini".
func TestFactoryRouting(t *testing.T) {
	cases := []struct {
		name           string
		settings       AgentSettings
		wantAnthropic  bool
	}{
		{
			name:          "claude model",
			settings:      AgentSettings{Model: "claude-sonnet-4-5", APIKey: "k"},
			wantAnthropic: true,
		},
		{
			name:          "openai model",
			settings:      AgentSettings{Model: "gpt-4o-mini", APIKey: "k"},
			wantAnthropic: false,
		},
		{
			name:          "explicit anthropic provider",
			settings:      AgentSettings{Provider: "anthropic", Model: "weird-name", APIKey: "k"},
			wantAnthropic: true,
		},
		{
			name:          "anthropic-prefixed slug",
			settings:      AgentSettings{Model: "anthropic/claude-haiku-4-5", APIKey: "k"},
			wantAnthropic: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := NewProviderFromSettings(c.settings)
			if err != nil {
				t.Fatalf("NewProviderFromSettings: %v", err)
			}
			_, isAnthro := p.(*AnthropicProvider)
			if isAnthro != c.wantAnthropic {
				t.Errorf("isAnthropic = %v want %v (got %T)", isAnthro, c.wantAnthropic, p)
			}
		})
	}
}

// Test 4: httptest round-trip for both providers — verify the request shape
// each backend emits (Anthropic gets x-api-key + anthropic-version; OpenAI
// gets Authorization: Bearer).
func TestRoundTripAnthropicHeaders(t *testing.T) {
	var gotXAPIKey, gotVersion, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotAuth = r.Header.Get("authorization")
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(readFixture(t, "anthropic_text.json"))
	}))
	defer srv.Close()

	p := NewAnthropicProvider("sk-ant-test")
	p.BaseURL = srv.URL

	resp, err := p.Chat(context.Background(), ChatRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 64,
		Messages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "ping"}}},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got, want := resp.FinishReason, FinishStop; got != want {
		t.Errorf("FinishReason = %q want %q", got, want)
	}
	if gotXAPIKey != "sk-ant-test" {
		t.Errorf("x-api-key = %q want sk-ant-test", gotXAPIKey)
	}
	if gotVersion == "" {
		t.Errorf("anthropic-version header missing")
	}
	if gotAuth != "" {
		t.Errorf("Authorization should be empty for Anthropic, got %q", gotAuth)
	}
	if n := len(resp.Content); n != 1 || resp.Content[0].Text != "ok" {
		t.Errorf("Content = %+v", resp.Content)
	}
}

func TestRoundTripOpenAIHeaders(t *testing.T) {
	var gotAuth, gotXAPIKey string
	var gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		gotXAPIKey = r.Header.Get("x-api-key")
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(readFixture(t, "openai_text.json"))
	}))
	defer srv.Close()

	p := NewOpenAIProvider("sk-openai-test")
	p.BaseURL = srv.URL

	resp, err := p.Chat(context.Background(), ChatRequest{
		Model:     "gpt-4o-mini",
		MaxTokens: 64,
		Messages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "ping"}}},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got, want := resp.FinishReason, FinishStop; got != want {
		t.Errorf("FinishReason = %q want %q", got, want)
	}
	if gotAuth != "Bearer sk-openai-test" {
		t.Errorf("Authorization = %q want \"Bearer sk-openai-test\"", gotAuth)
	}
	if gotXAPIKey != "" {
		t.Errorf("x-api-key should be empty for OpenAI, got %q", gotXAPIKey)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q want /chat/completions", gotPath)
	}
	if len(gotBody) == 0 {
		t.Errorf("request body was empty")
	}
	if n := len(resp.Content); n != 1 || resp.Content[0].Text != "ok" {
		t.Errorf("Content = %+v", resp.Content)
	}
}

// Test 5: finish-reason normalization table.
func TestFinishReasonNormalization(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) string
		in   string
		want string
	}{
		{"anthropic end_turn", normalizeAnthropicStop, "end_turn", FinishStop},
		{"anthropic tool_use", normalizeAnthropicStop, "tool_use", FinishToolCalls},
		{"anthropic max_tokens", normalizeAnthropicStop, "max_tokens", FinishLength},
		{"openai stop", normalizeOpenAIStop, "stop", FinishStop},
		{"openai tool_calls", normalizeOpenAIStop, "tool_calls", FinishToolCalls},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.fn(c.in); got != c.want {
				t.Errorf("normalize(%q) = %q want %q", c.in, got, c.want)
			}
		})
	}
}
