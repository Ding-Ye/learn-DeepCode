# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  File: core/providers/anthropic.py  (lines L1-L55, L526-L561 — abridged)
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
#
#  Reading guide for s01-minimum-loop:
#    Upstream wraps the SDK; we hand-roll the wire format. The same JSON
#    body crosses the wire either way — that is the lesson of s01.
# =============================================================================

# >>> s01: upstream imports — note the 3rd-party `anthropic` SDK is the only
#     import that talks to the network. Our Go counterpart uses *just* net/http.
"""Anthropic provider — direct SDK integration for Claude models."""

from anthropic import AsyncAnthropic

from core.observability import log_llm_call
from core.providers.base import LLMProvider, LLMResponse, ToolCallRequest


# =============================================================================
# >>> s01: AnthropicProvider — analogous to our Client struct in anthropic.go.
#     Upstream stores the SDK client; we store *http.Client. Same idea, less
#     mystery.
# =============================================================================
class AnthropicProvider(LLMProvider):
    """LLM provider using the native Anthropic SDK for Claude models.

    Handles message format conversion (OpenAI -> Anthropic Messages API),
    prompt caching, extended thinking, tool calls, and streaming.
    """

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

        # >>> s01: SDK construction — our equivalent is
        #     NewClient(apiKey) returning &Client{...}. The "max_retries=0" is
        #     because retries live one layer up in LLMProvider._run_with_retry.
        client_kw: dict[str, Any] = {}
        if api_key:
            client_kw["api_key"] = api_key
        if api_base:
            client_kw["base_url"] = api_base
        if extra_headers:
            client_kw["default_headers"] = extra_headers
        client_kw["max_retries"] = 0
        self._client = AsyncAnthropic(**client_kw)


# =============================================================================
# >>> s01: chat() — the main async entry point. We map this to
#     Client.SendMessage(ctx, MessageRequest) in anthropic.go. Differences:
#       - upstream is async/await; ours uses context.Context for cancel.
#       - upstream catches & wraps errors via _handle_error; ours returns
#         a typed *APIError.
#       - upstream emits observability/log_llm_call; we keep s01 free of
#         logging (s06 will reintroduce it).
# =============================================================================
async def chat(
    self,
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None = None,
    model: str | None = None,
    max_tokens: int = 4096,
    temperature: float = 0.7,
    reasoning_effort: str | None = None,
    tool_choice: str | dict[str, Any] | None = None,
) -> LLMResponse:
    # >>> s01: _build_kwargs converts OpenAI-style messages to Anthropic
    #     Messages API kwargs. We don't have this conversion in s01 because
    #     our wire format IS the Anthropic format already. s04 introduces
    #     the abstraction that needs translation.
    kwargs = self._build_kwargs(
        messages, tools, model, max_tokens, temperature, reasoning_effort, tool_choice
    )
    started = time.monotonic()
    result: LLMResponse | None = None
    try:
        # >>> s01: this is the actual network call. Equivalent in our Go code:
        #     resp, err := c.HTTPClient.Do(httpReq)
        response = await self._client.messages.create(**kwargs)
        # >>> s01: _parse_response translates SDK objects -> our LLMResponse
        #     dataclass. In Go we json.Unmarshal directly into MessageResponse.
        result = self._parse_response(response)
        return result
    except Exception as e:
        # >>> s01: errors are wrapped into LLMResponse so callers always get
        #     a value; we prefer Go's (resp, err) idiom — *APIError on the
        #     error path, *MessageResponse on success. Two return values, no
        #     hidden state.
        result = self._handle_error(e)
        return result
    finally:
        self._emit_observability(
            model=model, messages=messages, tools=tools,
            response=result, duration_ms=int((time.monotonic() - started) * 1000),
        )

# =============================================================================
# Read further:
#   1. core/providers/base.py    — LLMProvider abstract base (becomes our s04).
#   2. core/providers/registry.py — multi-provider keyword routing
#      (also s04, plus s03 for config-driven model selection).
#   3. core/agent_runtime/runner.py — the agent loop that calls chat() in a
#      while-loop with tool dispatch. That's s06.
# =============================================================================
