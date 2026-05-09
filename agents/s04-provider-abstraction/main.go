// File: main.go — CLI entry point.
//
// Usage:
//
//   s04 -provider anthropic -model claude-sonnet-4-20250514 "hello"
//   s04 -provider openai    -model gpt-4o-mini             "hello"
//   s04 -model claude-haiku-4-5-20251001 "ping"            # auto-detect
//
// Reads ANTHROPIC_API_KEY or OPENAI_API_KEY from env per provider.
//
// This is a thin demo: build AgentSettings from flags + env, hand them to
// NewProviderFromSettings, do one round-trip, print the first text block.
// Tests in provider_test.go exercise the same path through httptest.Server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	provider := flag.String("provider", "",
		"backend hint: \"anthropic\" | \"openai\" | \"\" (auto from -model)")
	model := flag.String("model", "claude-sonnet-4-20250514",
		"model id (default: claude-sonnet-4-20250514)")
	maxTokens := flag.Int("max-tokens", 1024, "per-call token budget")
	temp := flag.Float64("temperature", 0.1, "sampling temperature")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: s04 [-provider NAME] [-model ID] <prompt>\n\n"+
				"  Auto-routing: model containing \"claude\" → Anthropic,\n"+
				"  everything else → OpenAI-compatible /chat/completions.\n\n"+
				"  Reads ANTHROPIC_API_KEY (Anthropic) or OPENAI_API_KEY (OpenAI)\n"+
				"  from env. No config file — that's s03's job.\n")
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	prompt := strings.Join(flag.Args(), " ")

	settings := AgentSettings{
		Provider:    *provider,
		Model:       *model,
		MaxTokens:   *maxTokens,
		Temperature: *temp,
	}
	if isAnthropicSettings(settings) {
		settings.APIKey = os.Getenv("ANTHROPIC_API_KEY")
		if settings.APIKey == "" {
			log.Fatalf("ANTHROPIC_API_KEY is not set")
		}
	} else {
		settings.APIKey = os.Getenv("OPENAI_API_KEY")
		if settings.APIKey == "" {
			log.Fatalf("OPENAI_API_KEY is not set")
		}
	}

	p, err := NewProviderFromSettings(settings)
	if err != nil {
		log.Fatalf("build provider: %v", err)
	}

	req := ChatRequest{
		Model:       settings.Model,
		MaxTokens:   settings.MaxTokens,
		Temperature: settings.Temperature,
		Messages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: prompt}}},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := p.Chat(ctx, req)
	if err != nil {
		log.Fatalf("chat: %v", err)
	}
	for _, b := range resp.Content {
		if b.Type == "text" {
			fmt.Println(b.Text)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "[s04] no text block; finish_reason=%s tool_calls=%d\n",
		resp.FinishReason, len(resp.ToolCalls))
}
