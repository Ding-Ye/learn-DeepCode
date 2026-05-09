package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Test 1: unmarshal recorded fixture into MessageResponse and pull the first text.
func TestMessageResponse_UnmarshalAndFirstText(t *testing.T) {
	body, err := os.ReadFile("testdata/recorded_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var mr MessageResponse
	if err := json.Unmarshal(body, &mr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if mr.ID == "" || mr.Type != "message" || mr.Role != "assistant" {
		t.Fatalf("unexpected envelope: %+v", mr)
	}
	if mr.Usage.OutputTokens == 0 {
		t.Errorf("expected output tokens > 0, got %+v", mr.Usage)
	}
	text, err := mr.FirstText()
	if err != nil {
		t.Fatalf("FirstText: %v", err)
	}
	if !strings.Contains(text, "Multi-agent orchestration") {
		t.Errorf("text mismatch: %q", text)
	}
}

// Test 2: build a MessageRequest and assert its JSON shape matches the
// Anthropic Messages API contract.
func TestMessageRequest_JSONShape(t *testing.T) {
	req := MessageRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 256,
		System:    "be terse",
		Messages: []InputMessage{
			{Role: "user", Content: "ping"},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`"model":"claude-sonnet-4-20250514"`,
		`"max_tokens":256`,
		`"system":"be terse"`,
		`"role":"user"`,
		`"content":"ping"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in %s", want, got)
		}
	}
}

// Test 3: full HTTP round-trip via httptest.Server replaying the fixture.
func TestClient_SendMessage_Replay(t *testing.T) {
	fixture, err := os.ReadFile("testdata/recorded_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var seenAuth, seenVersion, seenContentType, seenPath string
	var seenMethod string
	var seenBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("x-api-key")
		seenVersion = r.Header.Get("anthropic-version")
		seenContentType = r.Header.Get("content-type")
		seenBody, _ = io.ReadAll(r.Body)

		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	c := &Client{
		BaseURL:    srv.URL,
		APIKey:     "sk-test-fake",
		APIVersion: "2023-06-01",
		HTTPClient: srv.Client(),
	}
	resp, err := c.SendMessage(context.Background(), MessageRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []InputMessage{
			{Role: "user", Content: "explain multi-agent orchestration"},
		},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if seenMethod != http.MethodPost {
		t.Errorf("method: got %s want POST", seenMethod)
	}
	if seenPath != "/v1/messages" {
		t.Errorf("path: got %s want /v1/messages", seenPath)
	}
	if seenAuth != "sk-test-fake" {
		t.Errorf("x-api-key not forwarded: got %q", seenAuth)
	}
	if seenVersion != "2023-06-01" {
		t.Errorf("anthropic-version: got %q", seenVersion)
	}
	if seenContentType != "application/json" {
		t.Errorf("content-type: got %q", seenContentType)
	}
	if !strings.Contains(string(seenBody), "explain multi-agent orchestration") {
		t.Errorf("request body missing prompt: %s", seenBody)
	}

	text, err := resp.FirstText()
	if err != nil {
		t.Fatalf("FirstText: %v", err)
	}
	if !strings.Contains(text, "Multi-agent orchestration") {
		t.Errorf("unexpected text: %q", text)
	}
}

// Test 4: 401 path returns *APIError with the upstream error type/message.
func TestClient_SendMessage_401(t *testing.T) {
	fixture, err := os.ReadFile("testdata/error_401.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	c := &Client{
		BaseURL:    srv.URL,
		APIKey:     "sk-bad",
		APIVersion: "2023-06-01",
		HTTPClient: srv.Client(),
	}
	_, err = c.SendMessage(context.Background(), MessageRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1,
		Messages:  []InputMessage{{Role: "user", Content: "x"}},
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", apiErr.Status)
	}
	if apiErr.Code != "authentication_error" {
		t.Errorf("code: got %q want authentication_error", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "invalid x-api-key") {
		t.Errorf("message: %q", apiErr.Message)
	}
}

// Test 5: FirstText on a response with only a non-text block returns errEmptyContent.
func TestMessageResponse_FirstText_Empty(t *testing.T) {
	mr := &MessageResponse{
		Content: []ContentBlock{
			{Type: "tool_use"}, // pretend a tool_use block snuck in
		},
	}
	_, err := mr.FirstText()
	if !errors.Is(err, errEmptyContent) {
		t.Errorf("got %v, want errEmptyContent", err)
	}
}
