# s01 — minimum-loop

> The smallest possible Anthropic Messages API call in Go. One request, one response, no tools, no streaming.

## What this is

A single Go binary that:

1. Reads a prompt from `argv`.
2. Reads `ANTHROPIC_API_KEY` from env.
3. POSTs `/v1/messages` with `{model, max_tokens, messages: [{role:"user", content:<prompt>}]}`.
4. Parses the response, prints `content[0].text`.
5. Exits.

That's the irreducible heartbeat of an agent. Every later session — tool calls (s02), tool-capable runner (s06), workflow context (s05), code-impl workflow (s10) — adds a layer on top of this round-trip.

## Run it

```bash
export ANTHROPIC_API_KEY=sk-ant-...
go run . "Explain multi-agent orchestration in one sentence"
go run . -v "summarize Go modules"
go run . -model claude-haiku-4-5-20251001 -v "ping"
```

`-v` writes `[s01] provider=…` and `[s01] stop_reason=…` to stderr. The actual answer goes to stdout.

## Test it (offline)

```bash
go test ./...
```

All five tests use `httptest.Server` replaying recorded JSON from `testdata/`. **No API key required.** This is the convention for the whole repo: every chapter ships fixtures so CI runs without secrets.

## Why no SDK?

DeepCode's `core/providers/anthropic.py` uses the `anthropic` Python SDK (`AsyncAnthropic`). We hand-roll the HTTP envelope here on purpose: the wire format is the protocol, and seeing the raw `x-api-key` header / `anthropic-version` pin / JSON body removes one level of "what does the SDK do?" mystery before s04 introduces a typed `Provider` interface that wraps both Anthropic and OpenAI.

By s06 you'll see the same Anthropic call from inside a tool-loop, but the wire format will be unchanged from this chapter — that's the lesson.

## What's deliberately absent

| Feature | Where it shows up |
|---|---|
| Tool calls | s02 (registry) → s06 (loop) |
| Iteration / loop | s06 |
| Multi-provider (OpenAI / Gemini) | s04 (and Phase G addendum) |
| Streaming (`stream: true`) | not in scope; covered by upstream's `stream` mode in `core/providers/anthropic.py:200+` |
| Prompt caching | not in scope; documented in upstream's `ProviderSpec.supports_prompt_caching` |
| Token estimation | s09 (memory compaction) |

## File map

- [`anthropic.go`](anthropic.go) — wire-format types + `Client.SendMessage`
- [`main.go`](main.go) — flag parsing + entry point
- [`main_test.go`](main_test.go) — five offline tests via `httptest.Server`
- [`testdata/recorded_response.json`](testdata/recorded_response.json) — golden Messages-API success body
- [`testdata/error_401.json`](testdata/error_401.json) — golden auth-error envelope

## Upstream reference

- `core/providers/anthropic.py:26-150` — `AnthropicProvider.__init__` + `chat()` + the SDK-level message conversion.
- See [`docs/zh/s01-minimum-loop.md`](../../docs/zh/s01-minimum-loop.md) and [`docs/en/s01-minimum-loop.md`](../../docs/en/s01-minimum-loop.md) for the full lesson with line-by-line annotations.
- Annotated upstream copy: [`upstream-readings/s01-anthropic.py`](../../upstream-readings/s01-anthropic.py).
