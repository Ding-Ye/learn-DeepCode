---
title: "s01 · 最小智能体回路"
chapter: 01
slug: s01-minimum-loop
est_read_min: 12
---

# s01 · 最小智能体回路

> 用 ~150 行 Go 把"prompt → JSON 请求 → JSON 响应 → 文本"这条最短回路完整跑通。后续每章都在这条回路上加一层。

---

## Problem

智能体（agent）这个词在 2025 年被用得很滥，新人读 DeepCode 这种 5 万行的多智能体系统，第一感受常常是：抽象层太多，找不到"真东西"在哪里。`workflows/agent_orchestration_engine.py` 协调七个 specialized agent；`core/agent_runtime/runner.py` 是 1065 行的循环；`core/providers/anthropic.py` 又封装了 SDK；`core/llm_runtime.py` 再做了一层 phase 选择 —— 一层套一层，到底"调用 LLM"是什么样？

这种困惑会让所有后续抽象都变成"folklore"：你只是相信 `agent.run()` 会做对的事，但说不出对在哪里。本章把抽象全剥掉，让你**亲眼看见一次 agent 调用的字节流**：一个 HTTP POST，一份 JSON 请求体，一份 JSON 响应体。这就是协议，这就是 agent 在最底层做的事。

## Solution

我们用 Go 标准库的 `net/http` 直接发一次 Anthropic Messages API 调用，不引入任何 LLM SDK。要点是：

1. **没有 SDK** —— 上游用 `AsyncAnthropic`，但 SDK 是协议的*包装*，不是协议本身。手写 `http.NewRequestWithContext` + `json.Marshal` 让请求体一字不漏地展示在你面前。
2. **没有循环** —— 一次请求，一次响应，立刻退出。`for` 循环、tool dispatch、memory compaction 全部留给后面的章节。
3. **没有全局状态** —— `Client` 显式持有 `*http.Client`、`APIKey`、`APIVersion`。测试用 `httptest.Server` 即可注入，不需要 monkey-patch。

理解了 s01 的这三点，s06（带 tool 的循环）就是"在 s01 外面套一个 `for`"，s04（多 provider）就是"在 s01 上面盖一个 interface"。一切复杂性都从这里长出来。

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

核心 ~50 行（节选自 [`agents/s01-minimum-loop/anthropic.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s01-minimum-loop/anthropic.go)）：

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

**4 个非显然点**：

1. **`anthropic-version` 是必填头** —— 不是"随便填新版本就好"。这个头锚定了请求体的 schema，后续 Anthropic 改版会通过引入新版本号而不是悄悄改字段。我们 hardcode `"2023-06-01"`，这是 Messages API 的稳定版本。
2. **`x-api-key` 不是 `Authorization: Bearer`** —— OpenAI / Gemini 用 Bearer，Anthropic 偏不。s04 引入 `Provider` interface 时这种差异就藏到了实现里；s01 让你**看见**它。
3. **错误路径返回 `*APIError` 而不是 `error`** —— `errors.As` 可以拆出 status / code / message。这是 Go 风格的"类型化错误"：调用方想区分 401 和 500 不需要解析字符串。上游 Python 把错误也包进 `LLMResponse`，那是 async 模式下的折中。
4. **`io.ReadAll` 之后才解析 JSON** —— 不是 `json.NewDecoder(resp.Body).Decode(...)`。理由：错误路径需要把 raw body 包进 `APIError.Message` 显示给人看；流式 decode 会把 body 吃光，错误信息就丢了。

## What Changed (vs. 上一章)

s01 是第一章 —— 没有上一章。但建立的几个约定影响所有后面的章节：

```diff
+ agents/sNN-<slug>/  独立 go.mod，不跨章 import
+ testdata/*.json     每章配 fixture，httptest.Server 离线重放
+ context.Context     第一参数贯穿所有 I/O
+ 自带类型 *APIError   错误是显式契约，不是 string
+ README.md           每章的"读这个就够了"入口
```

## Try It

```bash
# 真调用（需要 API key）
export ANTHROPIC_API_KEY=sk-ant-...
cd agents/s01-minimum-loop

# 默认 sonnet-4
go run . "用一句话解释什么是 multi-agent orchestration"

# 详细模式：打印 model / token 用量到 stderr
go run . -v "解释 Go 的 module 是什么"

# 切换到 Haiku 4.5
go run . -model claude-haiku-4-5-20251001 -v "summarize"

# 离线测试（不需要 API key，全部从 fixture 重放）
go test -v ./...
```

期望输出形态（stdout）：

```
Multi-agent orchestration coordinates several specialized AI agents
(planner, coder, reviewer) so each focuses on a sub-task while a
conductor merges their outputs into one coherent result.
```

期望 stderr（`-v` 模式）：

```
[s01] provider=anthropic model=claude-sonnet-4-20250514 prompt_bytes=44
[s01] stop_reason=end_turn in_tokens=14 out_tokens=38
```

测试期望：5 个 PASS，约 0.5s。

## Upstream Source Reading

DeepCode 的 LLM 调用入口在 `core/providers/anthropic.py`。它走的是 SDK，但 SDK 内部跨网络发的还是同一份 JSON。本章选 `__init__` + `chat()` 两处对照阅读。

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

**阅读笔记**：

- **SDK vs. 手写**：上游用 `AsyncAnthropic.messages.create()`，我们用 `c.HTTPClient.Do()`。差别在抽象层，不在协议层。如果你抓 SDK 的网络包会看到一模一样的 JSON。
- **错误模型**：上游把错误**也**包进 `LLMResponse`（finally 里调 `_emit_observability`，无论成功失败都记一次）；我们用 Go 的 `(resp, err)`，调用方对错误的处理更显式。两种模式各有道理，文档里要明确。
- **async 和 context.Context**：upstream 用 `async`/`await` + `await asyncio.sleep` 做超时；Go 用 `context.WithTimeout` 做同样的事，少一个语言层的"哪些函数能 await"心智负担。
- **`_build_kwargs` 不在 s01**：上游有一个 OpenAI-style → Anthropic-style 的消息转换器，因为它的 `LLMProvider` 抽象是 OpenAI-style。s01 的请求体本就是 Anthropic 格式，这层转换我们留到 s04。
- **`_emit_observability` 不在 s01**：log/trace 是 cross-cutting concern，第一章就引入会让最小回路看不清。s06 引入循环时再加。

**继续读**：从 `core/providers/anthropic.py` 出发，沿 `LLMProvider`（`core/providers/base.py`）→ `LLMResponse` 这条链能走到 s04；沿 `_client.messages.create()` 进 SDK 源码可以验证我们手写的 JSON 一字不差地匹配。注解版： [`upstream-readings/s01-anthropic.py`](../../upstream-readings/s01-anthropic.py)。

---

**下一章**：s02 引入 **Tool 注册表** —— 现在 agent 只能说话，s02 让它能调工具。
