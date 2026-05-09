// Package main — s01-minimum-loop.
//
// Wire-format types for Anthropic Messages API. Captured from
// https://docs.anthropic.com/en/api/messages at 2026-05.
//
// Upstream counterpart: core/providers/anthropic.py:26-150 — but DeepCode
// uses the AsyncAnthropic SDK; we hand-roll the JSON envelope to make the
// wire format visible. (See README §"Why no SDK?".)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// MessageRequest is the payload POSTed to /v1/messages.
type MessageRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []InputMessage `json:"messages"`
}

// InputMessage is one user/assistant turn in the request.
type InputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MessageResponse is the body Anthropic returns from /v1/messages.
//
// Trimmed to fields the minimum loop actually consumes: id, type, role, model,
// content (an array of typed blocks; we only handle "text"), stop_reason,
// usage. Upstream stores everything; we don't, on purpose — this is s01.
type MessageResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
}

// ContentBlock is a tagged union; s01 only reads Type=="text".
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Usage echoes Anthropic's token accounting.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// APIError is the typed error returned for non-2xx responses.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("anthropic api: %d %s: %s", e.Status, e.Code, e.Message)
}

// errEmptyContent is returned when the API replies 200 but with no text block.
var errEmptyContent = errors.New("response had no text content block")

// Client is a thin wrapper around the Messages endpoint. Holds its own
// http.Client so there's no global SDK state — every test gets a fresh one.
type Client struct {
	BaseURL    string
	APIKey     string
	APIVersion string
	HTTPClient *http.Client
}

// NewClient returns a Client pointing at the public Anthropic endpoint.
// Tests construct one with BaseURL = httptest.Server.URL instead.
func NewClient(apiKey string) *Client {
	return &Client{
		BaseURL:    "https://api.anthropic.com",
		APIKey:     apiKey,
		APIVersion: "2023-06-01",
		HTTPClient: &http.Client{},
	}
}

// SendMessage POSTs a MessageRequest and decodes the response.
// On non-2xx the returned error is *APIError.
func (c *Client) SendMessage(ctx context.Context, req MessageRequest) (*MessageResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", c.APIVersion)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}

	var mr MessageResponse
	if err := json.Unmarshal(respBody, &mr); err != nil {
		return nil, fmt.Errorf("decode response: %w (body=%s)", err, truncate(respBody, 200))
	}
	return &mr, nil
}

// FirstText returns the first text block of a response, or errEmptyContent.
func (m *MessageResponse) FirstText() (string, error) {
	for _, b := range m.Content {
		if b.Type == "text" {
			return b.Text, nil
		}
	}
	return "", errEmptyContent
}

func parseAPIError(status int, body []byte) error {
	// Anthropic returns: {"type":"error","error":{"type":"...","message":"..."}}
	var env struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return &APIError{
		Status:  status,
		Code:    env.Error.Type,
		Message: env.Error.Message,
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
