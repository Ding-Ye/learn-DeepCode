// File: anthropic.go — AnthropicProvider, a Provider over the native
// Messages API.
//
// Wire format is hand-rolled JSON over net/http (consistent with s01) so the
// reader can see exactly which bytes go on the wire. Upstream uses the
// AsyncAnthropic SDK; we deliberately don't, for pedagogy.
//
// Upstream counterpart: core/providers/anthropic.py:26-200 — `__init__`,
// `_convert_messages`, top-level `chat`. Our translation lives in two
// methods: chatRequestBody (canonical → wire) and parseAnthropicResponse
// (wire → canonical).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// AnthropicProvider implements Provider against the public Messages API.
// Holds its own http.Client so there's no global SDK state — every test
// spins up a fresh one pointed at httptest.Server.
type AnthropicProvider struct {
	BaseURL    string // e.g. "https://api.anthropic.com"
	APIKey     string
	APIVersion string
	HTTPClient *http.Client
}

// NewAnthropicProvider returns a provider pre-pointed at the public endpoint.
// Tests construct their own value with BaseURL set to httptest.Server.URL.
func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		BaseURL:    "https://api.anthropic.com",
		APIKey:     apiKey,
		APIVersion: "2023-06-01",
		HTTPClient: &http.Client{},
	}
}

// anthropicMessage is one entry in the Messages API request envelope.
type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

// anthropicContentBlock is the wire shape of one block. Anthropic uses a
// tagged union with three concrete types: text, tool_use, tool_result.
type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`         // tool_use
	Name      string          `json:"name,omitempty"`       // tool_use
	Input     json.RawMessage `json:"input,omitempty"`      // tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   string          `json:"content,omitempty"`     // tool_result body
}

// anthropicTool is the wire shape of one tool definition.
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// anthropicRequest is the body POSTed to /v1/messages.
type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
}

// anthropicResponse is the body we decode from a 200.
type anthropicResponse struct {
	ID         string                  `json:"id"`
	Model      string                  `json:"model"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Chat is the Provider entry point. It builds the Messages payload, POSTs
// it, and decodes the body into a canonical ChatResponse.
func (p *AnthropicProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body, err := p.chatRequestBody(req)
	if err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("anthropic: build body: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", p.APIVersion)

	resp, err := p.HTTPClient.Do(httpReq)
	if err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("anthropic: do request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("anthropic: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("anthropic: http %d: %s", resp.StatusCode, string(respBody))
	}

	return parseAnthropicResponse(respBody)
}

// chatRequestBody marshals a canonical ChatRequest into the Messages-API
// wire shape. The system prompt (canonical role="system") is hoisted to a
// top-level field; everything else stays in messages.
func (p *AnthropicProvider) chatRequestBody(req ChatRequest) ([]byte, error) {
	out := anthropicRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			// Anthropic only takes a plain string for system. Concatenate
			// any text blocks; ignore non-text in system messages.
			for _, b := range m.Content {
				if b.Type == "text" {
					if out.System != "" {
						out.System += "\n\n"
					}
					out.System += b.Text
				}
			}
			continue
		}
		out.Messages = append(out.Messages, anthropicMessage{
			Role:    m.Role,
			Content: toAnthropicBlocks(m.Content),
		})
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return json.Marshal(out)
}

// toAnthropicBlocks translates canonical ContentBlocks to wire blocks.
func toAnthropicBlocks(blocks []ContentBlock) []anthropicContentBlock {
	out := make([]anthropicContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, anthropicContentBlock{Type: "text", Text: b.Text})
		case "tool_use":
			out = append(out, anthropicContentBlock{
				Type:  "tool_use",
				ID:    b.ToolUseID,
				Name:  b.ToolName,
				Input: b.Input,
			})
		case "tool_result":
			out = append(out, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: b.ToolUseID,
				Content:   b.Output,
			})
		}
	}
	return out
}

// parseAnthropicResponse maps wire bytes onto a canonical ChatResponse.
// This is where the finish-reason normalization happens.
func parseAnthropicResponse(body []byte) (ChatResponse, error) {
	var raw anthropicResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("anthropic: decode response: %w", err)
	}
	out := ChatResponse{
		FinishReason: normalizeAnthropicStop(raw.StopReason),
		Usage: Usage{
			InputTokens:  raw.Usage.InputTokens,
			OutputTokens: raw.Usage.OutputTokens,
		},
	}
	for _, b := range raw.Content {
		switch b.Type {
		case "text":
			out.Content = append(out.Content, ContentBlock{Type: "text", Text: b.Text})
		case "tool_use":
			out.Content = append(out.Content, ContentBlock{
				Type:      "tool_use",
				ToolUseID: b.ID,
				ToolName:  b.Name,
				Input:     b.Input,
			})
			out.ToolCalls = append(out.ToolCalls, ToolCallRequest{
				ID:   b.ID,
				Name: b.Name,
				Args: b.Input,
			})
		}
	}
	return out, nil
}

// normalizeAnthropicStop maps Anthropic's stop_reason vocabulary onto the
// canonical Finish* constants. Unknown values pass through verbatim so a
// human can still see them in logs.
func normalizeAnthropicStop(stop string) string {
	switch stop {
	case "end_turn", "stop_sequence":
		return FinishStop
	case "tool_use":
		return FinishToolCalls
	case "max_tokens":
		return FinishLength
	default:
		return stop
	}
}
