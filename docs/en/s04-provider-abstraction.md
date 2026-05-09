---
title: "s04 · LLM provider abstraction"
chapter: 04
slug: s04-provider-abstraction
est_read_min: 14
---

# s04 · LLM provider abstraction

> One `Provider` interface, two implementations (native Anthropic + OpenAI-compatible), a single canonical `ChatRequest` / `ChatResponse` shape. This chapter promotes "talking to a model" from s01's bare HTTP call into a swappable interface — every later session sees the LLM as a `Provider` and never touches the wire format again.

---

## Problem

s01 wrote 50 lines of net/http to talk straight to `https://api.anthropic.com/v1/messages`. That works for one demo and breaks before the third:

- **Phase G demands OpenAI** — Claude for planning (strong reasoning), GPT for implementation (cheap, fast). s01's hard-coded path can't carry both.
- **The two backends' wire formats are completely different**:
  - Anthropic: `POST /v1/messages`, header `x-api-key`, response `content: [{type:"text"}, {type:"tool_use", input:{...}}]`, `stop_reason: "end_turn" | "tool_use" | "max_tokens"`.
  - OpenAI: `POST /chat/completions`, header `Authorization: Bearer ...`, response `choices[0].message.{content, tool_calls:[{function:{arguments:STRING}}]}`, `finish_reason: "stop" | "tool_calls" | "length"`.
  - **Critical wrinkle**: Anthropic ships tool arguments as a JSON object; OpenAI ships them as a **JSON-encoded string** (a second decode step).
- **Each backend's SDK has its own personality** — `anthropic` is Pydantic-flavored, `openai` is OpenAPI-generated. Letting the call site branch on `if provider == "anthropic"` is a dead end.

Upstream's `core/providers/` solves this with an `LLMProvider` ABC plus concrete implementations. s04 ports the same shape to Go: one interface, two structs, a small factory, and a tight set of canonical types.

## Solution

```ascii-anim frames=1
                       ┌───────────────────────────┐
   AgentSettings ─────►│ NewProviderFromSettings   │
   (from s03)          └─────────────┬─────────────┘
                                     │ name contains "claude" / "anthropic"?
                              ┌──────┴──────┐
                              ▼             ▼
                  ┌─────────────────┐  ┌─────────────────┐
                  │ AnthropicProvider│  │ OpenAIProvider  │
                  │  /v1/messages    │  │  /chat/comple…  │
                  │  x-api-key       │  │  Authorization  │
                  └────────┬─────────┘  └────────┬────────┘
                           │                     │
                  parseAnthropicResponse  parseOpenAIResponse
                           │                     │
                           └─────────┬───────────┘
                                     ▼
                        ┌──────────────────────────┐
                        │  ChatResponse (canonical)│
                        │  Content[] / ToolCalls[] │
                        │  FinishReason / Usage    │
                        └──────────────────────────┘
```

Three load-bearing design choices:

1. **`Provider` is a single-method interface**: `Chat(ctx, ChatRequest) (ChatResponse, error)`. Upstream's ABC also bundles `chat_with_retry`, `chat_stream`, error classification — about 600 LOC. s04 strips all of it: retry belongs to s06's runner; streaming is out-of-scope. A narrower interface is a more swappable interface.
2. **`ChatResponse` is canonical, not raw wire bytes.** Two `parseXxxResponse` functions translate each backend's JSON into the same Go types. Callers always see `Content []ContentBlock` plus `FinishReason: "stop" | "tool_calls" | "length" | "error"`. **That's the whole point of the abstraction** — adding a new backend is a new `parseXxx`; the call site doesn't move.
3. **The factory routes by model string.** `NewProviderFromSettings` looks at `Provider` / `Model` fields: anything containing `claude` / `anthropic` returns `AnthropicProvider`; everything else is `OpenAIProvider`. This is a pruned `core/providers/registry.py:find_by_model` — with two backends we don't need the full keyword table.

## How It Works

### 1. `provider.go` — the contract

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

type ChatRequest struct {
    Model       string
    Messages    []Message
    Tools       []ToolSchema
    MaxTokens   int
    Temperature float64
}

type ChatResponse struct {
    Content      []ContentBlock
    ToolCalls    []ToolCallRequest
    FinishReason string  // FinishStop | FinishToolCalls | FinishLength | FinishError
    Usage        Usage
}

type ContentBlock struct {
    Type      string // "text" | "tool_use" | "tool_result"
    Text      string
    ToolUseID string
    ToolName  string
    Input     json.RawMessage
    Output    string
}
```

**Why is `ContentBlock` a tagged union instead of a Go interface?** Because `encoding/json` handles structs mechanically; interfaces require a custom `UnmarshalJSON`. The teaching version trades a few unused fields for the ability to compare values with `reflect.DeepEqual` — test code is half as long.

The four `Finish*` constants are arguably the most important deliverable of this chapter:

```go
const (
    FinishStop      = "stop"        // normal completion (Anthropic "end_turn", OpenAI "stop")
    FinishToolCalls = "tool_calls"  // model wants tools (Anthropic "tool_use", OpenAI "tool_calls")
    FinishLength    = "length"      // truncated (Anthropic "max_tokens", OpenAI "length")
    FinishError     = "error"       // call failed
)
```

Callers (s06's runner, s10's workflow) only ever read these four values — they're the contract. Each provider's `normalizeXxxStop` is responsible for translating native vocabulary into one of them.

### 2. `anthropic.go` — Messages API

Construction matches s01:

```go
type AnthropicProvider struct {
    BaseURL    string  // "https://api.anthropic.com"
    APIKey     string
    APIVersion string  // "2023-06-01"
    HTTPClient *http.Client
}
```

`Chat`'s happy path is three steps: build body → POST → parse response. The request always carries `x-api-key`, `anthropic-version`, and `content-type`.

**Two non-obvious bits in body construction**:

1. **`system` must be hoisted out of the messages array.** The Messages API rejects `role: "system"` inside `messages[]` — system prompts are a **top-level field**. `chatRequestBody` concatenates the text blocks of any `Message{Role:"system"}` into the top-level `system` string.
2. **`tool_result` is a content block, not a message role.** OpenAI uses `role:"tool"`; Anthropic embeds tool results inside a user message's content array. `toAnthropicBlocks` dispatches on three Type values: text / tool_use / tool_result. The caller passes tool_result as a user-content block (which is exactly what s06's runner does).

**Response parsing (`parseAnthropicResponse`)** — the focus is stop_reason normalization:

```go
func normalizeAnthropicStop(stop string) string {
    switch stop {
    case "end_turn", "stop_sequence":
        return FinishStop
    case "tool_use":
        return FinishToolCalls
    case "max_tokens":
        return FinishLength
    default:
        return stop  // unknown values pass through so logs still show them
    }
}
```

A subtle point: Anthropic can return **both** a text block and a tool_use block in one response (the "explain, then call a tool" pattern). `parseAnthropicResponse` adds both to `Content` and copies the tool_use entry into `ToolCalls`. Callers check `len(ToolCalls)>0` to decide whether to dispatch tools — they don't need to walk `Content` again.

### 3. `openai.go` — Chat Completions API

Construction is similarly direct:

```go
type OpenAIProvider struct {
    BaseURL    string  // "https://api.openai.com/v1"
    APIKey     string
    HTTPClient *http.Client
}
```

Headers are `Authorization: Bearer <key>` plus `content-type`. The only header shared with Anthropic: `content-type: application/json`.

**Two non-obvious bits in body construction**:

1. **OpenAI hangs `tool_calls` off the assistant message**, unlike Anthropic where they're flat in the content array. `toOpenAIMessage` sees a canonical `ContentBlock{Type:"tool_use"}` and converts it to `openAIToolCall`, appending to `message.ToolCalls`.
2. **`arguments` must be a JSON-encoded string.** OpenAI rejects `arguments: {"text":"hi"}`; it requires `arguments: "{\"text\":\"hi\"}"`. We already store canonical `Input` as `json.RawMessage` (i.e. "the raw bytes of an object"), so on the way out `string(b.Input)` is the wire string. **And** on the way back in, we treat `tc.Function.Arguments` (a string) as `json.RawMessage` (also the raw bytes of an object) — these representations are identical:

```go
args := json.RawMessage(tc.Function.Arguments)
if !json.Valid(args) {
    args = json.RawMessage(`{}`)  // defensive fallback
}
```

`json.Valid` is the stdlib's "are these bytes legal JSON?" — cheaper than `json.Unmarshal`-and-discard. Upstream uses `json_repair` (a forgiving parser that fixes half-broken JSON) for some flaky-format models; stdlib is enough for s04's standard-shape fixtures.

`normalizeOpenAIStop` mirrors the Anthropic table:

```go
switch stop {
case "stop":                              return FinishStop
case "tool_calls", "function_call":       return FinishToolCalls
case "length":                            return FinishLength
default:                                  return stop
}
```

### 4. `factory.go` — routing

```go
func NewProviderFromSettings(s AgentSettings) (Provider, error) {
    if s.APIKey == "" { return nil, ErrMissingAPIKey }
    if isAnthropicSettings(s) {
        p := NewAnthropicProvider(s.APIKey)
        if s.BaseURL != "" { p.BaseURL = s.BaseURL }
        return p, nil
    }
    p := NewOpenAIProvider(s.APIKey)
    if s.BaseURL != "" { p.BaseURL = s.BaseURL }
    return p, nil
}

func isAnthropicSettings(s AgentSettings) bool {
    if strings.ToLower(strings.TrimSpace(s.Provider)) == "anthropic" { return true }
    m := strings.ToLower(s.Model)
    return strings.Contains(m, "claude") || strings.Contains(m, "anthropic")
}
```

**Why don't we import s03's `AgentSettings` directly?** Project rule: each session has its own `go.mod` and never imports another session — so every chapter runs and tests in isolation, and a reader who skips around never trips on package paths. The `AgentSettings` here is a slimmed copy with two extra fields (`APIKey` + `BaseURL`); s03's `Resolve` populates the same names, the user pastes them through.

**Four non-obvious points**:

1. **A narrow interface is a strength.** `Provider` has one method. Adding a method (`Stream`, `CountTokens`) adds a mock burden everywhere. s06 only calls `Chat`; s10 only calls `Chat` — narrow turns out to be enough.
2. **Canonical types are the real portability currency.** Every later session swaps in fake providers via plain struct literals (`return ChatResponse{Content: []ContentBlock{{Type:"text", Text:"ok"}}}, nil`) instead of `httptest`, because the types are ordinary structs.
3. **`FinishReason` is the control-flow hub.** s06's runner loop checks `FinishReason == FinishToolCalls` to decide whether to dispatch a tool, and `FinishStop` to exit. Replacing two native vocabularies with four constants is this chapter's biggest gift to later sessions.
4. **Each provider holds its own `*http.Client`** — no package-level singleton. Tests can run concurrently with separate `httptest.Server` instances and they don't pollute one another. Upstream's module-level `AsyncAnthropic` client is one of the anti-patterns we explicitly fix here.

## What Changed (vs. s03)

```diff
+ provider.go    Provider interface + ChatRequest/Response/Message/ContentBlock/...
+ anthropic.go   AnthropicProvider (/v1/messages) + stop_reason normalization
+ openai.go      OpenAIProvider (/chat/completions) + finish_reason normalization
+ factory.go     NewProviderFromSettings + slim AgentSettings (with APIKey/BaseURL)
+ testdata/*.json  4 golden fixtures (text + tool_use, one pair per backend)
+ introduces "canonical types as currency" — ChatResponse becomes the shared lingua of every later session
- no more bare net/http — s01's SendMessage retires into AnthropicProvider.Chat as an internal detail
```

s03 was the **configuration layer** — strings → typed structs. s04 is the **protocol layer** — typed structs → bytes on the wire and back. Independent, separately testable, separately evolvable. s06 is the chapter that puts them together: s02's tool registry + s04's provider, both inside the runner.

## Try It

```bash
cd agents/s04-provider-abstraction

# Anthropic path (default)
export ANTHROPIC_API_KEY=sk-ant-...
go run . -model claude-sonnet-4-20250514 "ping"

# OpenAI path (explicit)
export OPENAI_API_KEY=sk-...
go run . -provider openai -model gpt-4o-mini "ping"

# Auto-routing by model name ("claude" → Anthropic)
go run . -model claude-haiku-4-5-20251001 "summarize Go modules in one line"

# Tests (no API key needed; httptest is fully offline)
go test -v ./...
```

All five tests pass in <1s with no network:

| # | Test | Asserts |
|---|---|---|
| 1 | TestDecodeAnthropicToolUse | wire bytes → 1 ToolCallRequest, FinishReason="tool_calls" |
| 2 | TestDecodeOpenAIToolCall | same canonical shape, OpenAI fixture |
| 3 | TestFactoryRouting | 4 sub-tests: claude / gpt / explicit anthropic / "anthropic/..." prefix |
| 4 | TestRoundTripAnthropicHeaders + TestRoundTripOpenAIHeaders | real httptest hit; assert each backend sets the right auth header (x-api-key vs Bearer) |
| 5 | TestFinishReasonNormalization | 5 sub-tests: end_turn/tool_use/max_tokens/stop/tool_calls each map to the right canonical value |

## Upstream Source Reading

```upstream:core/providers/base.py#L100-L186
class LLMProvider(ABC):
    """Base class for LLM providers."""

    def __init__(self, api_key=None, api_base=None):
        self.api_key = api_key
        self.api_base = api_base
        self.generation = GenerationSettings()

    @abstractmethod
    async def chat(
        self,
        messages: list[dict[str, Any]],
        tools: list[dict[str, Any]] | None = None,
        model: str | None = None,
        max_tokens: int = 4096,
        temperature: float = 0.7,
        ...
    ) -> LLMResponse: pass
```

```upstream:core/providers/anthropic.py#L26-L122
class AnthropicProvider(LLMProvider):
    """LLM provider using the native Anthropic SDK for Claude models."""

    def __init__(self, api_key=None, api_base=None,
                 default_model="claude-sonnet-4-20250514",
                 extra_headers=None):
        super().__init__(api_key, api_base)
        self.default_model = default_model
        self.extra_headers = extra_headers or {}

        from anthropic import AsyncAnthropic
        client_kw = {"max_retries": 0}
        if api_key:  client_kw["api_key"]    = api_key
        if api_base: client_kw["base_url"]   = api_base
        if extra_headers: client_kw["default_headers"] = extra_headers
        self._client = AsyncAnthropic(**client_kw)
```

```upstream:core/providers/registry.py#L156-L201
def find_by_name(name):
    normalized = _to_snake(name.replace("-", "_"))
    for spec in PROVIDERS:
        if spec.name == normalized:
            return spec
    return None

def find_by_model(model, available_provider_names=None):
    if not model: return None
    model_lower = model.lower()
    model_prefix = model_lower.split("/", 1)[0] if "/" in model_lower else ""

    for spec in PROVIDERS:
        if model_prefix and model_prefix.replace("-","_") == spec.name:
            return spec
    for spec in PROVIDERS:
        if any(kw in model_lower for kw in spec.keywords):
            return spec
    return None
```

**Reading notes**:

- **`LLMProvider._run_with_retry` (base.py L694-L782) is the genuinely complex chunk of upstream.** It classifies transient vs persistent errors, parses `retry-after` headers, and uses heartbeat-aware sleeps. We deliberately drop the lot: s04's `Chat()` returns `FinishReason="error"` after one failure, and retry becomes s06's runner-level concern. A teaching cut needs the success contract to be crisp first; layering reliability on top is easier when the success path is short.
- **`AnthropicProvider._convert_messages` (anthropic.py L133-L181) does OpenAI-chat-format → Messages-API translation.** Upstream uses the OpenAI shape as an internal lingua franca and translates at the Anthropic boundary. Our Go canonical `Message + ContentBlock` is provider-neutral, so each backend translates outward — one fewer indirection.
- **`OpenAICompatProvider` (openai_compat.py, full file) is ~2000 LOC.** It handles Responses API circuit breakers, Kimi thinking, OpenRouter attribution headers, prompt caching markers, function-call vs. tool-call legacy, and more. Our Go implementation runs in ~200 LOC because s04 only commits to the **minimum subset**. Appendix B's exercise "add a third provider" reintroduces the `ProviderSpec` table when richer routing is actually needed.
- **`registry.py:find_by_model`'s two passes** — first an exact prefix match (`"openai/gpt-5.4"` → `"openai"`), then a keyword scan (`"claude"` inside `"claude-sonnet-..."`). Our Go uses `strings.Contains` for an equivalent keyword scan; prefix exact-match isn't load-bearing in the two-backend world.

**Read further**: from `core/providers/base.py:LLMProvider._run_with_retry` (L694) follow into `_extract_retry_after` (L595) and `_to_retry_seconds` (L613) to see how upstream extracts retry hints from error text. s06's runner will rebuild that semantic specifically around `FinishReason="error"`. Annotated copy: [`upstream-readings/s04-providers.py`](../../upstream-readings/s04-providers.py).

---

**Next**: s05 pivots to "task state" — an immutable `WorkflowContext` packing paper path, task id, and workspace root into one struct that all 11 phases read but never mutate. s04's Provider re-emerges in s06, where it ships alongside s05 + s02 inside a real Runner.
