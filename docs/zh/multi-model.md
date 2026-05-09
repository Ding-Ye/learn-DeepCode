---
title: "M · 多模型接入指南"
slug: multi-model
est_read_min: 8
---

# M · 多模型接入指南（Anthropic / OpenAI / DeepSeek / Qwen / 自托管…）

> learn-DeepCode 一开始就把 `Provider` 抽象写在 s04，所以从第一天起就支持 8 套主流模型后端。本指南列清单 + 给可直接复制粘贴的命令。

---

## 为什么有这一节

国内开发者大多走 DeepSeek / Qwen / 自托管 vLLM；海外走 OpenAI / Anthropic / Groq；研究侧走 OpenRouter 拼多 model。如果 learn-DeepCode 只能跑 Anthropic，那就把一半读者拒之门外。

s04（[provider-abstraction](./s04-provider-abstraction.md)）已经把 LLM 调用抽成了一个 interface：

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}
```

并且内置两个实现：

- `AnthropicProvider` —— 直接走 `https://api.anthropic.com/v1/messages` 的原生协议（`x-api-key` 头 + `anthropic-version` 头）
- `OpenAIProvider` —— 走 `https://<base>/chat/completions` 的 OpenAI 兼容协议（`Authorization: Bearer` 头）

**所有 OpenAI-兼容协议的供应商**只要换 `BaseURL` 就能用，不需要改一行代码。下面列 8 个常用 profile，每个都给 1 行命令。

## 8 个 provider profile

| Profile | BaseURL | 默认 model | API Key 环境变量 |
|---|---|---|---|
| anthropic | `https://api.anthropic.com` | `claude-sonnet-4-20250514` | `ANTHROPIC_API_KEY` |
| openai | `https://api.openai.com/v1` | `gpt-4o-mini` | `OPENAI_API_KEY` |
| deepseek | `https://api.deepseek.com/v1` | `deepseek-chat` | `DEEPSEEK_API_KEY` |
| moonshot | `https://api.moonshot.cn/v1` | `moonshot-v1-8k` | `MOONSHOT_API_KEY` |
| qwen | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-plus` | `DASHSCOPE_API_KEY` |
| groq | `https://api.groq.com/openai/v1` | `llama-3.3-70b-versatile` | `GROQ_API_KEY` |
| openrouter | `https://openrouter.ai/api/v1` | `openai/gpt-4o-mini` | `OPENROUTER_API_KEY` |
| local | `http://localhost:8000/v1` | `local-model` | `OPENAI_API_KEY` (任意非空字符串) |

> 这 8 个里面 **只有 anthropic 走原生协议**；其它 7 个全部走 OpenAI 兼容协议。
> 这意味着：你写一个 `OpenAIProvider`，就同时拥有了 7 个 backend。

## 怎么用：s04 的 factory

s04 提供 `NewProviderFromSettings(s AgentSettings) (Provider, error)`，从配置里挑实现：

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

> ⚠️ 跨 session import 不是 learn-DeepCode 的常规模式（项目规则：每节独立 `go.mod`）。
> 如果你只想在自己的项目里用 s04，照搬 s04 的 4 个文件（`provider.go` `anthropic.go` `openai.go` `factory.go`）到你自己的包即可。

## 怎么用：s01 直接换 backend

s01 的 main.go 写死了 Anthropic。如果你想把它改成支持上面 8 个 profile，只要 ~30 行：

1. 加一张 `providerProfiles` map（参考 [SKILL 模板](https://github.com/Ding-Ye/learn-DeepCode/blob/main/.learn/sksill-template-not-shipped))
2. 加 `-provider` flag 解析 + profile lookup
3. 根据 profile 选 client 实现

但**更推荐**直接读 s04 —— 这才是设计意图。

## 命令示例

```bash
# 默认 Anthropic（s01）
export ANTHROPIC_API_KEY=sk-ant-...
go run ./agents/s01-minimum-loop "explain agent loop"

# 在 s04 里跑 DeepSeek
export DEEPSEEK_API_KEY=sk-...
go run ./agents/s04-provider-abstraction \
    -provider deepseek \
    -model deepseek-chat \
    "explain agent loop"

# 在 s04 里跑 Qwen
export DASHSCOPE_API_KEY=sk-...
go run ./agents/s04-provider-abstraction \
    -provider qwen \
    -model qwen-plus \
    "explain agent loop"

# 在 s04 里跑本地 vLLM
go run ./agents/s04-provider-abstraction \
    -provider local \
    -base-url http://localhost:8000/v1 \
    -model qwen2.5-coder-14b \
    "explain agent loop"
```

## 不同 backend 的 quirks

| Backend | 已知 quirk | 在 s04 里怎么处理 |
|---|---|---|
| Anthropic | `x-api-key` 而不是 `Authorization`；`anthropic-version` 头必填；`tool_use` 块嵌在 `content` 数组 | `AnthropicProvider` 单独实现 |
| OpenAI | `tool_calls` 在 `choices[0].message`；arguments 是字符串而不是对象 | `OpenAIProvider` 把字符串 unmarshal 成 `json.RawMessage` |
| DeepSeek | 完全 OpenAI 兼容；`deepseek-reasoner` 模型不接受 system role 在 messages[0] 之外 | 用 `OpenAIProvider`；s04 的 ChatRequest 把 system 拼进 messages[0] |
| Moonshot/Kimi | OpenAI 兼容；`moonshot-v1-128k` 上下文比 8k/32k 贵很多 | 用 `OpenAIProvider`；按需选 model |
| Qwen | OpenAI 兼容；DashScope 域名；某些 Qwen 模型不支持 `temperature=0`（最小 0.1） | 用 `OpenAIProvider`；s04 的 ChatRequest 不强制下限，使用方按需调 |
| Groq | OpenAI 兼容；很快但 quota 紧；只支持 Llama / Mixtral 等开源模型 | 用 `OpenAIProvider` |
| OpenRouter | OpenAI 兼容；model 名带 `provider/` 前缀（如 `anthropic/claude-3.5-sonnet`） | 用 `OpenAIProvider`；model 字段直接传完整名字 |
| 自托管 vLLM/SGLang | 完全 OpenAI 兼容；某些模型 tokenizer 行为略不同（`chat_template`） | 用 `OpenAIProvider` + 你部署的 base URL |

## 测试时怎么验证 backend？

s04 的全部测试都用 `httptest.Server` 重放 fixture，不调真 API。但要验证一个 backend 能跑通真实流量，最简单是：

```bash
# 用 curl 先验证 backend 活着
curl -s "https://api.deepseek.com/v1/chat/completions" \
    -H "Authorization: Bearer $DEEPSEEK_API_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"ping"}]}' | head -50
```

如果 200 就走 s04 的 OpenAIProvider；如果 4xx，先修 backend 再说。

## 为什么没有 Gemini 单独 implementation？

Gemini 有原生 SDK 但也提供 OpenAI 兼容 endpoint（`https://generativelanguage.googleapis.com/v1beta/openai/`）。新代码建议直接用 OpenAI 兼容路径，不要为了一个 backend 多写一个 implementation。如果你有 native-Gemini 的需求（比如 grounded search、live API），建议参考 s04 的 AnthropicProvider 写一个 `GeminiProvider`，作为 [Appendix B 练习 #1](./appendix-b-upstream-map.md) 的延伸。

## 上游对照

DeepCode 的多 provider 抽象在：

- `core/providers/base.py` —— `LLMProvider` ABC
- `core/providers/registry.py` —— provider 关键字路由（包含 OpenAI / Anthropic / Deepseek / Moonshot / Qwen / Groq / OpenRouter / Gemini）
- `core/config.py` —— 通过 `${ENV_VAR}` 替换从配置文件加载 API keys

我们 learn-DeepCode 的版本简化了一些（没有 supports_prompt_caching / thinking_style 等字段），但核心的"keyword 路由 + 8 套 profile"是 1:1 对照。
