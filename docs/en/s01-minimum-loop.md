---
title: "s01 · Minimum agent loop"
chapter: 01
slug: s01-minimum-loop
est_read_min: 12
---

# s01 · Minimum agent loop

> About 150 lines of Go that drive the shortest possible round-trip: prompt → JSON request → JSON response → text. Every later chapter adds one layer on top.

---

## Problem

The word "agent" is overused in 2025, and a newcomer reading DeepCode — a 57k-line multi-agent system — usually feels lost in the abstraction tower. `workflows/agent_orchestration_engine.py` orchestrates seven specialized agents; `core/agent_runtime/runner.py` is a 1065-line loop; `core/providers/anthropic.py` wraps the SDK; `core/llm_runtime.py` adds another layer for phase selection. Where exactly does an "LLM call" happen?

That confusion turns every later abstraction into folklore: you trust `agent.run()` does the right thing, but you can't say what "right" means. This chapter strips all the abstraction off and lets you **see one agent call as bytes**: one HTTP POST, one JSON request body, one JSON response body. That is the protocol. That is what an agent does at the bottom.

## Solution

We use Go stdlib `net/http` to make a single Anthropic Messages API call, with no LLM SDK. Three design decisions:

1. **No SDK** — upstream uses `AsyncAnthropic`, but the SDK is a *wrapper* around the protocol, not the protocol itself. Hand-rolling `http.NewRequestWithContext` + `json.Marshal` puts the request body right in front of you, byte for byte.
2. **No loop** — one request, one response, exit. `for` loops, tool dispatch, memory compaction are all reserved for later chapters.
3. **No global state** — `Client` explicitly holds `*http.Client`, `APIKey`, `APIVersion`. Tests inject via `httptest.Server` without monkey-patching anything.

Once you internalize these three things, s06 (loop with tools) is "wrap a `for` around s01" and s04 (multi-provider) is "put an interface above s01". All complexity grows from here.

## How It Works

```ascii-anim frames=2
┌────────────────────────────────────────────────────────┐
│   prompt (CLI argv)                                    │
│        │                                               │
│        ▼                                               │
│   MessageRequest{Model, MaxTokens, Messages: [user]}   │
│        │  json.Marshal                                 │
│        ▼                                               │
│   POST /v1/messages                                    │
│   x-api-key: $ANTHROPIC_API_KEY                        │
│   anthropic-version: 2023-06-01                        │
│        │                                               │
│        ▼  HTTPS                                        │
│   ┌────────────────┐                                   │
│   │ Anthropic API  │                                   │
│   └────────────────┘                                   │
│        │                                               │
│        ▼                                               │
│   MessageResponse{Content:[{Type:"text", Text:"..."}]} │
│        │  FirstText()                                  │
│        ▼                                               │
│   stdout                                               │
└────────────────────────────────────────────────────────┘
```

The core ~50 lines (excerpt from [`agents/s01-minimum-loop/anthropic.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s01-minimum-loop/anthropic.go)):

```go
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
```

**4 non-obvious points**:

1. **`anthropic-version` is required** — not "any latest version will do". This header anchors the request schema; future API revisions arrive via a new version number rather than silently rewriting fields. We hardcode `"2023-06-01"`, the stable Messages API version.
2. **`x-api-key`, not `Authorization: Bearer`** — OpenAI and Gemini use Bearer; Anthropic doesn't. s04's `Provider` interface hides this difference inside each implementation; s01 lets you **see** it.
3. **Errors return `*APIError`, not plain `error`** — `errors.As` lets a caller pull out status / code / message. This is Go-style typed errors: distinguishing 401 from 500 doesn't require parsing strings. Upstream Python wraps errors into `LLMResponse` because async needs that uniform return shape; we don't.
4. **`io.ReadAll` before `json.Unmarshal`** — not `json.NewDecoder(resp.Body).Decode(...)`. Reason: the error path needs the raw body in `APIError.Message` so a human can read it; streaming decode consumes the body and the error message is gone.

## What Changed (vs. previous chapter)

s01 is the first chapter — there is no previous one. But the conventions established here ripple through every later chapter:

```diff
+ agents/sNN-<slug>/   independent go.mod, no cross-chapter imports
+ testdata/*.json      every chapter ships fixtures; httptest.Server replays
+ context.Context      first parameter on every I/O function
+ typed *APIError      errors are explicit contracts, not strings
+ README.md            "read this if you read nothing else" entry per chapter
```

## Try It

```bash
# Live call (API key required)
export ANTHROPIC_API_KEY=sk-ant-...
cd agents/s01-minimum-loop

# Default sonnet-4
go run . "Explain multi-agent orchestration in one sentence"

# Verbose: model + token usage on stderr
go run . -v "summarize Go modules"

# Switch to Haiku 4.5
go run . -model claude-haiku-4-5-20251001 -v "summarize"

# Offline tests (no API key needed; replay from fixtures)
go test -v ./...
```

Expected stdout shape:

```
Multi-agent orchestration coordinates several specialized AI agents
(planner, coder, reviewer) so each focuses on a sub-task while a
conductor merges their outputs into one coherent result.
```

Expected stderr (`-v`):

```
[s01] provider=anthropic model=claude-sonnet-4-20250514 prompt_bytes=44
[s01] stop_reason=end_turn in_tokens=14 out_tokens=38
```

Tests: 5 PASS, ~0.5s.

## Upstream Source Reading

DeepCode's LLM entry point lives in `core/providers/anthropic.py`. It uses the SDK, but the SDK still puts the same JSON on the wire. We pick `__init__` and `chat()` for direct comparison.

```upstream:core/providers/anthropic.py#L26-L55
class AnthropicProvider(LLMProvider):
    """LLM provider using the native Anthropic SDK for Claude models."""

    def __init__(
        self,
        api_key: str | None = None,
        api_base: str | None = None,
        default_model: str = "claude-sonnet-4-20250514",
        extra_headers: dict[str, str] | None = None,
    ):
        super().__init__(api_key, api_base)
        self.default_model = default_model
        self.extra_headers = extra_headers or {}

        from anthropic import AsyncAnthropic
        client_kw: dict[str, Any] = {}
        if api_key:
            client_kw["api_key"] = api_key
        if api_base:
            client_kw["base_url"] = api_base
        if extra_headers:
            client_kw["default_headers"] = extra_headers
        client_kw["max_retries"] = 0  # retries live in LLMProvider._run_with_retry
        self._client = AsyncAnthropic(**client_kw)
```

```upstream:core/providers/anthropic.py#L526-L561
async def chat(
    self,
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None = None,
    model: str | None = None,
    max_tokens: int = 4096,
    temperature: float = 0.7,
) -> LLMResponse:
    kwargs = self._build_kwargs(messages, tools, model, max_tokens, temperature)
    started = time.monotonic()
    result: LLMResponse | None = None
    try:
        response = await self._client.messages.create(**kwargs)
        result = self._parse_response(response)
        return result
    except Exception as e:
        result = self._handle_error(e)  # wraps into LLMResponse, never throws
        return result
    finally:
        self._emit_observability(model=model, messages=messages, tools=tools,
                                  response=result, duration_ms=...)
```

**Reading notes**:

- **SDK vs. hand-rolled**: upstream calls `AsyncAnthropic.messages.create()`; we call `c.HTTPClient.Do()`. The difference is in the abstraction layer, not the protocol. A network capture would show identical JSON.
- **Error model**: upstream packs errors *into* `LLMResponse` (the `finally` block always emits observability, success or failure); we use Go's `(resp, err)`, which makes error handling more explicit at call sites. Both choices are reasonable; the docs note the trade-off.
- **async vs. `context.Context`**: upstream uses `async`/`await` + `await asyncio.sleep` for timeouts; Go uses `context.WithTimeout` to do the same thing without the language-level "which functions can `await`" overhead.
- **`_build_kwargs` is missing in s01**: upstream has an OpenAI-style → Anthropic-style message converter because its `LLMProvider` abstraction is OpenAI-style. s01's request body *is* already Anthropic-shaped, so no conversion. s04 introduces it.
- **`_emit_observability` is missing in s01**: logging is a cross-cutting concern that would obscure the minimum loop. s06 brings it back when the loop becomes interesting enough to need traces.

**Read further**: from `core/providers/anthropic.py`, follow `LLMProvider` (`core/providers/base.py`) → `LLMResponse` to see what s04 will need; follow `_client.messages.create()` into the Python SDK to verify our hand-rolled JSON is byte-identical. Annotated copy: [`upstream-readings/s01-anthropic.py`](../../upstream-readings/s01-anthropic.py).

---

**Next**: s02 introduces a **Tool registry** — right now the agent can only talk; s02 lets it call tools.
