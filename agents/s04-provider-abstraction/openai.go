// File: openai.go — OpenAIProvider, a Provider over OpenAI-compatible
// /chat/completions endpoints (OpenAI proper, OpenRouter, DeepSeek, etc.).
//
// Wire format is hand-rolled JSON over net/http (consistent with s01) so the
// reader can see the contract. Upstream uses the AsyncOpenAI SDK with
// ~2,000 LOC of edge-case handling (Responses API, Kimi thinking, OpenRouter
// attribution, prompt caching). s04 stays at the minimum surface needed for
// Chat Completions: text + tool_calls + finish_reason.
//
// Upstream counterpart: core/providers/openai_compat.py:1-300 (init + the
// chat() entry, before the SDK kwarg-shaping branches).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OpenAIProvider implements Provider against any OpenAI-compatible chat
// endpoint. BaseURL points at "/" of the API root (e.g. "https://api.openai.com/v1").
type OpenAIProvider struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewOpenAIProvider returns a provider pointed at the OpenAI public API.
// BaseURL can be overridden post-construction for OpenAI-compatible
// gateways and httptest servers.
func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		BaseURL:    "https://api.openai.com/v1",
		APIKey:     apiKey,
		HTTPClient: &http.Client{},
	}
}

// openAIMessage is one entry in the request "messages" array. Content is a
// string for plain user/assistant turns; tool calls go on the assistant
// message via ToolCalls; tool results use role="tool".
type openAIMessage struct {
	Role       string                 `json:"role"`
	Content    string                 `json:"content,omitempty"`
	ToolCalls  []openAIToolCall       `json:"tool_calls,omitempty"`
	ToolCallID string                 `json:"tool_call_id,omitempty"`
	Name       string                 `json:"name,omitempty"`
}

// openAIToolCall is the wire shape of a tool invocation. Note that
// Function.Arguments is a JSON-encoded *string*, not an object — this is the
// quirk our parse step normalizes away.
type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openAITool is a tool definition in the OpenAI "function" envelope.
type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

// openAIRequest is the body POSTed to /chat/completions.
type openAIRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	Messages    []openAIMessage `json:"messages"`
	Tools       []openAITool    `json:"tools,omitempty"`
}

// openAIResponse is the subset of the response body we consume.
type openAIResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Chat is the Provider entry point.
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body, err := p.chatRequestBody(req)
	if err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("openai: build body: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+p.APIKey)

	resp, err := p.HTTPClient.Do(httpReq)
	if err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("openai: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("openai: http %d: %s", resp.StatusCode, string(respBody))
	}
	return parseOpenAIResponse(respBody)
}

// chatRequestBody marshals a canonical ChatRequest into the OpenAI Chat
// Completions wire shape.
func (p *OpenAIProvider) chatRequestBody(req ChatRequest) ([]byte, error) {
	out := openAIRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, toOpenAIMessage(m))
	}
	for _, t := range req.Tools {
		var ot openAITool
		ot.Type = "function"
		ot.Function.Name = t.Name
		ot.Function.Description = t.Description
		ot.Function.Parameters = t.InputSchema
		out.Tools = append(out.Tools, ot)
	}
	return json.Marshal(out)
}

// toOpenAIMessage flattens our typed content blocks into the OpenAI shape.
// Text blocks are concatenated into Content; tool_use blocks become entries
// in ToolCalls; tool_result blocks become a separate role="tool" message —
// but only the first such conversion happens here, since ContentBlock-typed
// tool_result must be split into its own message by the caller. For
// simplicity and to keep parity with single-turn fixtures, we include
// tool_use as ToolCalls on assistant messages and copy tool_result content
// into Content when role=="tool".
func toOpenAIMessage(m Message) openAIMessage {
	out := openAIMessage{Role: m.Role}
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			if out.Content != "" {
				out.Content += "\n"
			}
			out.Content += b.Text
		case "tool_use":
			tc := openAIToolCall{ID: b.ToolUseID, Type: "function"}
			tc.Function.Name = b.ToolName
			// OpenAI expects arguments as a JSON-encoded string. If our
			// canonical Input is empty, send "{}" so the wire is valid.
			if len(b.Input) == 0 {
				tc.Function.Arguments = "{}"
			} else {
				tc.Function.Arguments = string(b.Input)
			}
			out.ToolCalls = append(out.ToolCalls, tc)
		case "tool_result":
			out.ToolCallID = b.ToolUseID
			out.Content = b.Output
		}
	}
	return out
}

// parseOpenAIResponse maps wire bytes onto a canonical ChatResponse. The
// load-bearing bits: read choices[0], copy text into Content, expand each
// tool_calls entry into a canonical ToolCallRequest, and normalize
// finish_reason via normalizeOpenAIStop.
func parseOpenAIResponse(body []byte) (ChatResponse, error) {
	var raw openAIResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(raw.Choices) == 0 {
		return ChatResponse{FinishReason: FinishError}, fmt.Errorf("openai: response has no choices")
	}
	choice := raw.Choices[0]
	out := ChatResponse{
		FinishReason: normalizeOpenAIStop(choice.FinishReason),
		Usage: Usage{
			InputTokens:  raw.Usage.PromptTokens,
			OutputTokens: raw.Usage.CompletionTokens,
		},
	}
	if choice.Message.Content != "" {
		out.Content = append(out.Content, ContentBlock{
			Type: "text",
			Text: choice.Message.Content,
		})
	}
	for _, tc := range choice.Message.ToolCalls {
		// OpenAI ships arguments as a JSON-encoded *string*. Decode it once
		// so the canonical Args is the parsed object form (a JSON object as
		// raw bytes, the same representation Anthropic gives us natively).
		args := json.RawMessage(tc.Function.Arguments)
		if !json.Valid(args) {
			args = json.RawMessage(`{}`)
		}
		out.Content = append(out.Content, ContentBlock{
			Type:      "tool_use",
			ToolUseID: tc.ID,
			ToolName:  tc.Function.Name,
			Input:     args,
		})
		out.ToolCalls = append(out.ToolCalls, ToolCallRequest{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		})
	}
	return out, nil
}

// normalizeOpenAIStop maps OpenAI's finish_reason vocabulary onto the
// canonical Finish* constants.
func normalizeOpenAIStop(stop string) string {
	switch stop {
	case "stop":
		return FinishStop
	case "tool_calls", "function_call":
		return FinishToolCalls
	case "length":
		return FinishLength
	default:
		return stop
	}
}
