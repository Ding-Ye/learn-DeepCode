---
title: "s04 · LLM Provider 抽象"
chapter: 04
slug: s04-provider-abstraction
est_read_min: 14
---

# s04 · LLM Provider 抽象

> 一个 `Provider` 接口，两个实现（Anthropic 原生 + OpenAI-compatible），一组规范化（canonical）的 `ChatRequest`/`ChatResponse` 类型。这一章把"和模型说话"这件事从 s01 的裸 HTTP 调用升级成可替换的接口——后续每一章都把 LLM 当成 `Provider`，再不碰 wire format。

---

## Problem

s01 写了一段 50 行的 net/http 代码直连 `https://api.anthropic.com/v1/messages`。在第三个 demo 之前，这条路就走不通了：

- **Phase G 要求支持 OpenAI**——planning 用 Claude（推理强），implementation 用 GPT（速度快、token 便宜）。s01 的硬编码不能横切。
- **两个后端的 wire format 完全不同**：
  - Anthropic：`POST /v1/messages`，header `x-api-key`，response 是 `content: [{type:"text"}, {type:"tool_use", input:{...}}]`，`stop_reason: "end_turn" | "tool_use" | "max_tokens"`。
  - OpenAI：`POST /chat/completions`，header `Authorization: Bearer ...`，response 是 `choices[0].message.{content, tool_calls:[{function:{arguments:STRING}}]}`，`finish_reason: "stop" | "tool_calls" | "length"`。
  - **关键差异**：Anthropic 的 tool 参数是 JSON 对象；OpenAI 的是 **JSON 字符串**（要二次解码）。
- **每个后端的 SDK 都有自己的"个性"**——`anthropic` 是 Pydantic 风格，`openai` 是 OpenAPI 生成的，调用习惯完全不同。让上层每次都 `if provider == "anthropic"` 分叉是死路。

上游 `core/providers/` 用 `LLMProvider` ABC + 多个具体实现解决这件事；s04 把这个模式翻成 Go：一个接口，两个 struct，一个工厂，一组规范化类型。

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

三个关键设计决策：

1. **Provider 是单方法接口**：`Chat(ctx, ChatRequest) (ChatResponse, error)`。上游 ABC 还包了 `chat_with_retry` / `chat_stream` / 错误分类等 ~600 行；s04 全砍掉——retry 留给 s06 的 runner，stream 留给上游的现成实现。教学版的接口越窄，越好换。
2. **`ChatResponse` 是规范化的，不是直接传 wire bytes**。两个 `parseXxxResponse` 函数把各自的 JSON 翻译成同一组 Go 类型；调用方拿到的永远是 `Content []ContentBlock` + `FinishReason: "stop" | "tool_calls" | "length" | "error"`。**这就是抽象的意义**——上游加一个新 backend，只需要再写一个 `parseXxx`，调用方一行代码不动。
3. **工厂用 model 字符串路由**：`NewProviderFromSettings` 看 `Provider` / `Model` 字段，包含 `claude` / `anthropic` 走 `AnthropicProvider`，其余走 `OpenAIProvider`。这是 `core/providers/registry.py:find_by_model` 的精简版——s04 只两个后端，不必复刻整张关键词表。

## How It Works

### 一、`provider.go` ——契约层

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

**为什么 `ContentBlock` 是 tagged union 而不是 Go interface？** 因为 JSON marshal/unmarshal 在 struct 上是机械的，在 interface 上需要自定义 `UnmarshalJSON`。教学版的 ContentBlock 牺牲一点字段冗余，换 reflect.DeepEqual 能直接比较——测试代码因此短一半。

四个 `Finish*` 常量是这一章最重要的产物之一：

```go
const (
    FinishStop      = "stop"        // 正常结束（Anthropic "end_turn", OpenAI "stop"）
    FinishToolCalls = "tool_calls"  // 模型要调工具（Anthropic "tool_use", OpenAI "tool_calls"）
    FinishLength    = "length"      // 截断（Anthropic "max_tokens", OpenAI "length"）
    FinishError     = "error"       // 调用失败
)
```

调用方（s06 的 runner、s10 的 workflow）只看这四个值——它们是合同。各 provider 的 `normalizeXxxStop` 负责把 native vocabulary 翻译成这四个之一。

### 二、`anthropic.go` ——Messages API

构造和 s01 一致：

```go
type AnthropicProvider struct {
    BaseURL    string  // "https://api.anthropic.com"
    APIKey     string
    APIVersion string  // "2023-06-01"
    HTTPClient *http.Client
}
```

`Chat` 的 happy path 三步：build body → POST → parse response。请求头永远是 `x-api-key` + `anthropic-version` + `content-type`。

**Body 构造的两个易踩点**：

1. **system 必须 hoist 出 messages 数组**。Anthropic Messages API 不接受 `role: "system"` 的 message——system prompt 是 **顶层字段**。我们的 `chatRequestBody` 把 canonical `Message{Role:"system"}` 的 text 块拼成顶层 `system` 字符串。
2. **tool_result 是 content block，不是 message role**。OpenAI 用 `role:"tool"`，Anthropic 把 tool_result 嵌在某个 user message 的 content 数组里。我们的 `toAnthropicBlocks` 在三种 type 里分发：text / tool_use / tool_result，调用方只要把 tool_result 当成 user content block 传进来就行（s06 那一章 runner 就是这么干的）。

**Response 解析（`parseAnthropicResponse`）**——重点是 stop_reason normalization：

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
        return stop  // 让陌生值透传，方便日志里看到
    }
}
```

注意：Anthropic 在同一个 response 里可以**同时**包 text 块和 tool_use 块（"先解释再调工具"模式）。`parseAnthropicResponse` 把它们都加到 `Content`，并把 tool_use 的同一份信息也复制进 `ToolCalls`——调用方看 `len(ToolCalls)>0` 决定要不要派发工具，不必再走一遍 `Content`。

### 三、`openai.go` ——Chat Completions API

构造同样直白：

```go
type OpenAIProvider struct {
    BaseURL    string  // "https://api.openai.com/v1"
    APIKey     string
    HTTPClient *http.Client
}
```

请求头 `Authorization: Bearer <key>` + `content-type`。和 Anthropic 唯一一个相同的字段：`content-type: application/json`。

**Body 构造的两个易踩点**：

1. **OpenAI 把 tool_calls 挂在 assistant message 上**，不像 Anthropic 是平铺在 content 数组里。`toOpenAIMessage` 看到 canonical `ContentBlock{Type:"tool_use"}` 就把它转成 `openAIToolCall`，加到 message.ToolCalls。
2. **arguments 必须是 JSON-encoded 字符串**——OpenAI 不接受 `arguments: {"text":"hi"}`，只接受 `arguments: "{\"text\":\"hi\"}"`。我们已经在 canonical 里把 Input 存成 `json.RawMessage`（即"对象的 raw bytes"），所以序列化的时候直接 `string(b.Input)` 就拿到 wire 字符串。**反过来，response 解析时**要把 `tc.Function.Arguments`（一个字符串）当 `json.RawMessage`（也是对象的 raw bytes）回填——刚好是恒等转换：

```go
args := json.RawMessage(tc.Function.Arguments)
if !json.Valid(args) {
    args = json.RawMessage(`{}`)  // 防御性兜底
}
```

`json.Valid` 是 stdlib 给的"这串字节是合法 JSON 吗"——比 `json.Unmarshal` 然后扔掉结果便宜。上游用 `json_repair`（一个能修补半截 JSON 的库）来兼容某些模型的格式抖动；s04 的 stdlib 路径足够覆盖标准响应。

`normalizeOpenAIStop` 同样是四个 case：

```go
switch stop {
case "stop":                              return FinishStop
case "tool_calls", "function_call":       return FinishToolCalls
case "length":                            return FinishLength
default:                                  return stop
}
```

### 四、`factory.go` ——路由

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

**为什么不直接 import s03 的 `AgentSettings`？** 项目纪律：每个 session 自己的 go.mod，不互相 import——这样每章可以独立运行、独立测试，读者跳着读不会撞到包路径。`AgentSettings` 在 s04 里是一个简化版结构（多了 `APIKey` + `BaseURL` 两个字段），s03 的 `Resolve` 拼到这个结构里就行了。

**4 个非显然点**：

1. **接口窄是优势**——Provider 只有一个方法。多一个方法（`Stream`、`CountTokens`），多一份 mock 负担。s06 也只调 `Chat`，s10 也只调 `Chat`，事实证明窄就够了。
2. **canonical 类型是真正的 portability 货币**——后面任何 session 写测试都用 fake provider 替代 httptest，因为类型是普通 struct，能随手构造：`return ChatResponse{Content: []ContentBlock{{Type:"text", Text:"ok"}}}, nil`。
3. **finish reason 是控制流的 hub**——s06 的 runner loop 看 `FinishReason == FinishToolCalls` 决定是否派发工具，看 `FinishStop` 决定退出循环。用四个常量替代两套 native vocabulary 是这一章对后续章节最大的贡献。
4. **每个 Provider 持自己的 `*http.Client`**——没有 package-level singleton。测试可以并发跑，每个 `httptest.Server` 配一个独立 provider，互不污染。这是上游（用 module-level `AsyncAnthropic` 客户端）的反模式之一，我们这里就修掉了。

## What Changed (vs. s03)

```diff
+ provider.go    Provider 接口 + ChatRequest/Response/Message/ContentBlock/...
+ anthropic.go   AnthropicProvider（/v1/messages）+ stop_reason 归一化
+ openai.go      OpenAIProvider（/chat/completions）+ finish_reason 归一化
+ factory.go     NewProviderFromSettings + 简化版 AgentSettings（含 APIKey/BaseURL）
+ testdata/*.json  4 份 golden fixture（text + tool_use，两个后端各一对）
+ 引入"canonical types as currency"——ChatResponse 是后续所有 session 的共享语言
- 不再有"裸 net/http"——s01 的 SendMessage 退役为 AnthropicProvider.Chat 内部细节
```

s03 是 **配置层**——把字符串变成 typed struct。s04 是 **协议层**——把 typed struct 变成可以打到模型并解码回来的接口。两层独立测试，独立演化。s06 会拼起来：把 s02 的 Tool registry + s04 的 Provider 装进 Runner，跑一个真正会迭代的 agent loop。

## Try It

```bash
cd agents/s04-provider-abstraction

# Anthropic 路径（默认）
export ANTHROPIC_API_KEY=sk-ant-...
go run . -model claude-sonnet-4-20250514 "ping"

# OpenAI 路径（显式）
export OPENAI_API_KEY=sk-...
go run . -provider openai -model gpt-4o-mini "ping"

# 用 model 名自动路由（"claude" → Anthropic）
go run . -model claude-haiku-4-5-20251001 "summarize Go modules in one line"

# 跑测试（无需 API key，httptest 全离线）
go test -v ./...
```

测试 5 个全部 PASS，<1s，无网络：

| # | 测试 | 验证 |
|---|---|---|
| 1 | TestDecodeAnthropicToolUse | wire 字节 → 1 个 ToolCallRequest，FinishReason="tool_calls" |
| 2 | TestDecodeOpenAIToolCall | 同上，但走 OpenAI fixture（同样的 canonical shape） |
| 3 | TestFactoryRouting | 4 个 sub-test：claude / gpt / 显式 anthropic / "anthropic/..." 前缀 |
| 4 | TestRoundTripAnthropicHeaders + TestRoundTripOpenAIHeaders | httptest 真打一次，断言 header 对（x-api-key vs Bearer） |
| 5 | TestFinishReasonNormalization | 5 个 sub-test：end_turn/tool_use/max_tokens/stop/tool_calls 各自映射正确 |

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

**阅读笔记**：

- **`LLMProvider._run_with_retry`（base.py L694-L782）是上游真正复杂的部分**——它把 transient/persistent 错误分类、解析 `retry-after` header、用 heartbeat 阻塞睡眠。我们把它整段砍掉了：s04 的 `Chat()` 失败一次就返回 `FinishReason="error"`，retry 是 s06 runner 的职责。教学版要先把"一次成功调用"的契约讲清楚，再分层加可靠性——上游把它们焊在一个 ABC 里，会让首次阅读者迷失。
- **`AnthropicProvider._convert_messages`（anthropic.py L133-L181）做的是 OpenAI-chat-format → Messages-API 转换**——上游内部统一用 OpenAI 的格式当 lingua franca，到 Anthropic 这一层再翻译。我们的 Go canonical `Message+ContentBlock` 是中性的，两个 provider 各自往自己的 wire format 翻——少一层间接，一目了然。
- **`OpenAICompatProvider`（openai_compat.py 全文）有 ~2000 LOC**：处理 Responses API circuit breaker、Kimi thinking、OpenRouter attribution header、prompt caching marker、function-call vs tool-call legacy……我们的 Go 实现 ~200 行就跑得通，因为 s04 只承诺**最小子集**。Appendix B 给了"加第三个 provider（Gemini / DeepSeek）"作为练习——届时会把 ProviderSpec 这张表请回来。
- **`registry.py:find_by_model` 的两遍扫描**：先看 `model_prefix` 精确匹配（"openai/gpt-5.4" 的 "openai"），再走 keyword 扫描（"claude" 在 "claude-sonnet-..." 里）。我们 Go 用 `strings.Contains` 做了等价的 keyword 扫描；prefix exact-match 在两个后端的小世界里用不上。

**继续读**：从 `core/providers/base.py:LLMProvider._run_with_retry`（L694）进 `_extract_retry_after`（L595）和 `_to_retry_seconds`（L613）——看上游怎么从错误文本里抠 retry hint。s06 的 runner 会把这套语义重新搭一遍，但只针对 `FinishReason="error"`。注解版：[`upstream-readings/s04-providers.py`](../../upstream-readings/s04-providers.py)。

---

**下一章**：s05 转向"task state"——一个不可变的 `WorkflowContext`，把 paper path / task id / workspace root 等 11 个 phase 都要看的字段封进一个 struct。s04 的 Provider 会在 s06 那一章和 s05 + s02 一起被装进 Runner。
