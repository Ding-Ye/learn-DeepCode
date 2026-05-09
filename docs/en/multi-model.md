---
title: "M · Multi-model integration guide"
slug: multi-model
est_read_min: 8
---

# M · Multi-model integration guide (Anthropic / OpenAI / DeepSeek / Qwen / self-hosted…)

> learn-DeepCode lifts the `Provider` abstraction into s04 from day one, so 8 mainstream model backends are supported out of the box. This guide is a checklist + copy-pasteable commands.

---

## Why this guide exists

Most developers in mainland China use DeepSeek / Qwen / self-hosted vLLM; overseas use OpenAI / Anthropic / Groq; researchers mix-and-match via OpenRouter. If learn-DeepCode only worked with Anthropic, half the readers would be locked out.

s04 ([provider-abstraction](./s04-provider-abstraction.md)) already abstracts LLM calls into one interface:

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}
```

And ships two implementations:

- `AnthropicProvider` — speaks `https://api.anthropic.com/v1/messages` (`x-api-key` + `anthropic-version` headers)
- `OpenAIProvider` — speaks `https://<base>/chat/completions` (OpenAI-compatible, `Authorization: Bearer`)

**Every OpenAI-compatible backend** works by changing just the `BaseURL` — no code changes. The 8 common profiles below come with one-line examples.

## 8 provider profiles

| Profile | BaseURL | Default model | API-key env var |
|---|---|---|---|
| anthropic | `https://api.anthropic.com` | `claude-sonnet-4-20250514` | `ANTHROPIC_API_KEY` |
| openai | `https://api.openai.com/v1` | `gpt-4o-mini` | `OPENAI_API_KEY` |
| deepseek | `https://api.deepseek.com/v1` | `deepseek-chat` | `DEEPSEEK_API_KEY` |
| moonshot | `https://api.moonshot.cn/v1` | `moonshot-v1-8k` | `MOONSHOT_API_KEY` |
| qwen | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-plus` | `DASHSCOPE_API_KEY` |
| groq | `https://api.groq.com/openai/v1` | `llama-3.3-70b-versatile` | `GROQ_API_KEY` |
| openrouter | `https://openrouter.ai/api/v1` | `openai/gpt-4o-mini` | `OPENROUTER_API_KEY` |
| local | `http://localhost:8000/v1` | `local-model` | `OPENAI_API_KEY` (any non-empty string) |

> Of these 8, **only anthropic uses the native protocol**; the other 7 all use OpenAI-compatible.
> Bottom line: writing `OpenAIProvider` once gives you 7 backends.

## How to use: s04's factory

s04 provides `NewProviderFromSettings(s AgentSettings) (Provider, error)`:

```go
import s04 "github.com/Ding-Ye/learn-DeepCode/agents/s04-provider-abstraction"

p, err := s04.NewProviderFromSettings(s04.AgentSettings{
    Provider: "deepseek",
    Model:    "deepseek-chat",
    APIKey:   os.Getenv("DEEPSEEK_API_KEY"),
    BaseURL:  "https://api.deepseek.com/v1",
})
if err != nil { log.Fatal(err) }

resp, err := p.Chat(ctx, s04.ChatRequest{
    Model:    "deepseek-chat",
    Messages: []s04.Message{ /* ... */ },
})
```

> ⚠️ Cross-session imports are NOT the normal pattern in learn-DeepCode (project rule: each chapter has its own `go.mod`).
> If you want s04 in your own project, copy the 4 files (`provider.go` `anthropic.go` `openai.go` `factory.go`) into your own package.

## How to use: swap backends inside s01

s01's main.go hardcodes Anthropic. To support all 8 profiles you'd add ~30 lines:

1. A `providerProfiles` map
2. A `-provider` flag + profile lookup
3. Switch the client implementation by profile

But **the better path** is to read s04 directly — that's what it's there for.

## Command examples

```bash
# Default Anthropic (s01)
export ANTHROPIC_API_KEY=sk-ant-...
go run ./agents/s01-minimum-loop "explain agent loop"

# DeepSeek via s04
export DEEPSEEK_API_KEY=sk-...
go run ./agents/s04-provider-abstraction \
    -provider deepseek \
    -model deepseek-chat \
    "explain agent loop"

# Qwen via s04
export DASHSCOPE_API_KEY=sk-...
go run ./agents/s04-provider-abstraction \
    -provider qwen \
    -model qwen-plus \
    "explain agent loop"

# Local vLLM via s04
go run ./agents/s04-provider-abstraction \
    -provider local \
    -base-url http://localhost:8000/v1 \
    -model qwen2.5-coder-14b \
    "explain agent loop"
```

## Backend-specific quirks

| Backend | Known quirk | How s04 handles it |
|---|---|---|
| Anthropic | Uses `x-api-key`, not `Authorization`; `anthropic-version` header is required; `tool_use` is nested in the `content` array | `AnthropicProvider` is a separate implementation |
| OpenAI | `tool_calls` lives in `choices[0].message`; arguments are JSON strings, not objects | `OpenAIProvider` unmarshals string args back into `json.RawMessage` |
| DeepSeek | Fully OpenAI-compatible; `deepseek-reasoner` rejects system role outside messages[0] | Uses `OpenAIProvider`; s04 puts system content into messages[0] |
| Moonshot/Kimi | OpenAI-compatible; `moonshot-v1-128k` is much pricier than 8k/32k | Use `OpenAIProvider`; pick model by need |
| Qwen | OpenAI-compatible via DashScope; some Qwen models reject `temperature=0` (min 0.1) | Use `OpenAIProvider`; ChatRequest doesn't force a floor — caller chooses |
| Groq | OpenAI-compatible; very fast but tight quotas; open-source models only (Llama/Mixtral/etc.) | Use `OpenAIProvider` |
| OpenRouter | OpenAI-compatible; model names use `provider/` prefix (e.g. `anthropic/claude-3.5-sonnet`) | Use `OpenAIProvider`; pass the full namespaced model id |
| Self-hosted vLLM/SGLang | Fully OpenAI-compatible; `chat_template` differences for some models | Use `OpenAIProvider` + your deployed base URL |

## Testing across backends

All s04 tests use `httptest.Server` replays — they never hit the wire. To validate a real backend, the simplest sanity check is curl:

```bash
curl -s "https://api.deepseek.com/v1/chat/completions" \
    -H "Authorization: Bearer $DEEPSEEK_API_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"ping"}]}' | head -50
```

If you get 200, s04's OpenAIProvider will work. If 4xx, fix the backend first.

## Why no separate Gemini implementation?

Gemini has a native SDK but also exposes an OpenAI-compatible endpoint (`https://generativelanguage.googleapis.com/v1beta/openai/`). For new code, prefer the compatible path — don't write yet another implementation just for one backend. If you specifically need native-Gemini features (grounded search, live API), use s04's AnthropicProvider as a template and write a `GeminiProvider` — this is the natural extension of [Appendix B exercise #1](./appendix-b-upstream-map.md).

## Upstream comparison

DeepCode's multi-provider abstraction lives in:

- `core/providers/base.py` — the `LLMProvider` ABC
- `core/providers/registry.py` — keyword-based provider routing (covers OpenAI / Anthropic / Deepseek / Moonshot / Qwen / Groq / OpenRouter / Gemini)
- `core/config.py` — loads API keys from config via `${ENV_VAR}` substitution

learn-DeepCode's version is leaner (no `supports_prompt_caching` / `thinking_style` fields), but the core "keyword routing + 8-profile catalog" is 1:1.
