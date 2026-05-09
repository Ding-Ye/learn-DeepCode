# s04 — provider-abstraction

> Two backends, one interface. The smallest typed `Provider` that lets the rest of the curriculum stay backend-agnostic.

## What this is

A Go module that defines:

1. A `Provider` interface with one method, `Chat(ctx, ChatRequest) (ChatResponse, error)`.
2. Canonical request / response types (`ChatRequest`, `ChatResponse`, `Message`, `ContentBlock`, `ToolSchema`, `ToolCallRequest`, `Usage`) plus `FinishStop` / `FinishToolCalls` / `FinishLength` / `FinishError` constants.
3. Two concrete implementations:
   - **`AnthropicProvider`** — POSTs the native Messages API (`x-api-key` + `anthropic-version`).
   - **`OpenAIProvider`** — POSTs OpenAI-compatible `/chat/completions` (`Authorization: Bearer`).
4. A `NewProviderFromSettings(AgentSettings) (Provider, error)` factory that picks the right impl from a model string ("claude" / "anthropic" → Anthropic, everything else → OpenAI).

That's the whole module. Every later session (s06, s10, ...) treats the LLM as a `Provider` and never sees the wire format again.

## Run it

```bash
export ANTHROPIC_API_KEY=sk-ant-...
go run . -model claude-sonnet-4-20250514 "ping"

export OPENAI_API_KEY=sk-...
go run . -provider openai -model gpt-4o-mini "ping"
```

The CLI auto-routes by model name: anything containing "claude" or "anthropic" hits the Anthropic endpoint; everything else goes to OpenAI-compatible.

## Test it (offline)

```bash
go test -v ./...
```

Five tests, no network. Two decode fixtures into canonical types; one drives the factory routing table; two run real `httptest.Server` round-trips and assert the auth header shape; one is a finish-reason normalization table-test.

## Why hand-rolled HTTP?

DeepCode's `core/providers/anthropic.py` uses `anthropic.AsyncAnthropic`; `core/providers/openai_compat.py` uses `openai.AsyncOpenAI`. Both SDKs hide the wire format behind several thousand lines of kwarg-shaping. We hand-roll the JSON envelopes (continuing the s01 convention) so the contract is visible:

| Concern | Anthropic wire | OpenAI wire |
|---|---|---|
| Auth header | `x-api-key: <key>` | `Authorization: Bearer <key>` |
| Version pin | `anthropic-version: 2023-06-01` | (none) |
| Endpoint | `POST /v1/messages` | `POST /v1/chat/completions` |
| Tool result block | `{"type":"tool_use", id, name, input:{...}}` | `choices[0].message.tool_calls[i].function.{name,arguments:string}` |
| Finish reason | `stop_reason: "end_turn"` / `"tool_use"` / `"max_tokens"` | `finish_reason: "stop"` / `"tool_calls"` / `"length"` |
| Args encoding | object | JSON-encoded string |

The translation lives in two functions: `parseAnthropicResponse` and `parseOpenAIResponse`. Both produce the same `ChatResponse`.

## What's deliberately absent

| Feature | Where it shows up |
|---|---|
| Streaming (`stream: true`) | not in scope (upstream `core/providers/anthropic.py:200+`) |
| Retry / backoff | not in scope (upstream `LLMProvider._run_with_retry`, ~250 LOC) |
| Image content blocks | not in scope (upstream `_strip_image_content`) |
| Prompt caching | not in scope (`ProviderSpec.supports_prompt_caching`) |
| Adaptive `max_tokens` shrink | not in scope (s06 stretch goal) |
| Provider auto-detection from API key prefix | not in scope (`registry.find_by_key_prefix`) |

The `Provider` interface is intentionally minimal. s06 adds the loop; s09 adds memory compaction. Both build on this single method.

## File map

- [`provider.go`](provider.go) — `Provider` interface + canonical value types
- [`anthropic.go`](anthropic.go) — `AnthropicProvider` (Messages API)
- [`openai.go`](openai.go) — `OpenAIProvider` (Chat Completions API)
- [`factory.go`](factory.go) — `NewProviderFromSettings` + minimal `AgentSettings`
- [`main.go`](main.go) — CLI entry point
- [`provider_test.go`](provider_test.go) — five offline tests
- [`testdata/`](testdata/) — four golden fixtures (text + tool-use, both backends)

## Upstream reference

- `core/providers/base.py` — `LLMProvider` ABC + `LLMResponse` + `ToolCallRequest`.
- `core/providers/anthropic.py:26-200` — `AnthropicProvider.__init__`, `_convert_messages`, the `chat()` entry.
- `core/providers/openai_compat.py:1-300` — `OpenAICompatProvider.__init__`, message + tool conversion.
- `core/providers/registry.py` — provider keyword routing (the rule we condense to two cases).
- See [`docs/zh/s04-provider-abstraction.md`](../../docs/zh/s04-provider-abstraction.md) and [`docs/en/s04-provider-abstraction.md`](../../docs/en/s04-provider-abstraction.md) for the full lesson.
- Annotated upstream copy: [`upstream-readings/s04-providers.py`](../../upstream-readings/s04-providers.py).
