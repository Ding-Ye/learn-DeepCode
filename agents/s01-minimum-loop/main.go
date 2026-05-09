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

const (
	defaultModel     = "claude-sonnet-4-20250514"
	defaultMaxTokens = 1024
)

func main() {
	model := flag.String("model", envOr("ANTHROPIC_MODEL", defaultModel),
		"Claude model id (default: "+defaultModel+")")
	system := flag.String("system", "",
		"optional system prompt")
	verbose := flag.Bool("v", false,
		"print provider / model / token counts on stderr")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: s01 [-v] [-model ID] [-system TEXT] <prompt>\n\n"+
				"  Reads ANTHROPIC_API_KEY from env.\n"+
				"  Calls /v1/messages once, prints the first text block.\n\n"+
				"  Examples:\n"+
				"    s01 \"hello\"\n"+
				"    s01 -v \"explain multi-agent orchestration\"\n"+
				"    s01 -model claude-haiku-4-5-20251001 -v \"summarize Go modules\"\n")
	}
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	prompt := strings.Join(flag.Args(), " ")

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatalf("ANTHROPIC_API_KEY is not set")
	}

	client := NewClient(apiKey)

	req := MessageRequest{
		Model:     *model,
		MaxTokens: defaultMaxTokens,
		System:    *system,
		Messages: []InputMessage{
			{Role: "user", Content: prompt},
		},
	}

	if *verbose {
		fmt.Fprintf(os.Stderr,
			"[s01] provider=anthropic model=%s prompt_bytes=%d\n",
			*model, len(prompt))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := client.SendMessage(ctx, req)
	if err != nil {
		log.Fatalf("send: %v", err)
	}

	text, err := resp.FirstText()
	if err != nil {
		log.Fatalf("no text content: %v (stop_reason=%s)", err, resp.StopReason)
	}

	if *verbose {
		fmt.Fprintf(os.Stderr,
			"[s01] stop_reason=%s in_tokens=%d out_tokens=%d\n",
			resp.StopReason, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}

	fmt.Println(text)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
