# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  Files: core/providers/base.py + core/providers/anthropic.py
#         + core/providers/openai_compat.py + core/providers/registry.py
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
# =============================================================================
"""DeepCode LLM provider abstraction (subset).

The annotations below ("# >>> s04: ...") map upstream Python lines onto the
Go counterparts in agents/s04-provider-abstraction. Read this side-by-side
with provider.go / anthropic.go / openai.go.
"""

# -----------------------------------------------------------------------------
# core/providers/base.py — the canonical types every backend must produce.
# -----------------------------------------------------------------------------

# >>> s04: ToolCallRequest is the canonical "the model wants to call tool X
#     with these args" structure. Both Anthropic and OpenAI parse responses
#     into a list of these. Go counterpart: provider.go::ToolCallRequest
#     (ID, Name, Args json.RawMessage). Upstream stores extra_content +
#     provider_specific_fields for round-trip; we drop them — s04 is the
#     teaching cut.
@dataclass
class ToolCallRequest:
    """A tool call request from the LLM."""

    id: str
    name: str
    arguments: dict[str, Any]
    extra_content: dict[str, Any] | None = None
    provider_specific_fields: dict[str, Any] | None = None
    function_provider_specific_fields: dict[str, Any] | None = None


# >>> s04: LLMResponse is the canonical chat output. Go counterpart:
#     provider.go::ChatResponse {Content, ToolCalls, FinishReason, Usage}.
#     Upstream's `content: str | None` is a single string; our Go type uses
#     []ContentBlock so a tool_use response carries the call alongside any
#     accompanying text. Functionally equivalent — we just preserve the
#     ordering Anthropic gives us.
@dataclass
class LLMResponse:
    """Response from an LLM provider."""

    content: str | None
    tool_calls: list[ToolCallRequest] = field(default_factory=list)
    finish_reason: str = "stop"   # >>> s04: identical canonical vocabulary
    usage: dict[str, int] = field(default_factory=dict)
    retry_after: float | None = None
    reasoning_content: str | None = None
    thinking_blocks: list[dict] | None = None
    error_status_code: int | None = None
    # >>> s04: error_* fields drive upstream's retry classifier. We don't
    #     port them — s04's FinishReason="error" is the only error signal.
    #     Real retry logic returns in s06+ as a stretch goal.


# >>> s04: LLMProvider ABC. Go counterpart: provider.go::Provider interface.
#     Upstream packs retry, streaming, image-strip, and rate-limit detection
#     into the ABC's helper methods. Our Go interface is one method,
#     `Chat(ctx, ChatRequest) (ChatResponse, error)` — every helper either
#     moves to the caller (retry → s06's runner loop) or stays out of scope.
class LLMProvider(ABC):
    """Base class for LLM providers."""

    @abstractmethod
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
        pass


# -----------------------------------------------------------------------------
# core/providers/anthropic.py — native Messages API.
# -----------------------------------------------------------------------------

# >>> s04: AnthropicProvider construction. Go counterpart:
#     anthropic.go::NewAnthropicProvider — same fields (BaseURL, APIKey,
#     APIVersion, HTTPClient). Upstream uses AsyncAnthropic SDK; we hand-roll
#     net/http to keep the wire format visible (see s01 pattern).
class AnthropicProvider(LLMProvider):
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
        client_kw = {}
        if api_key:
            client_kw["api_key"] = api_key
        if api_base:
            client_kw["base_url"] = api_base
        # >>> s04: max_retries=0 → retry handled by base class. Our Go port
        #     skips retry entirely at this layer.
        client_kw["max_retries"] = 0
        self._client = AsyncAnthropic(**client_kw)

    # >>> s04: _convert_messages does OpenAI chat-format → Messages-API
    #     translation. Go counterpart: anthropic.go::chatRequestBody +
    #     toAnthropicBlocks. Two notes for the Go port:
    #       1. system role is hoisted to a top-level `system` field — the
    #          Messages API doesn't accept role="system" in messages[].
    #       2. tool_result is a content block, not a separate message role.
    def _convert_messages(self, messages):
        """Return ``(system, anthropic_messages)``."""
        system = ""
        raw = []
        for msg in messages:
            role = msg.get("role", "")
            content = msg.get("content")
            if role == "system":
                system = content if isinstance(content, (str, list)) else str(content or "")
                continue
            # ... tool / assistant / user branches ...
        return system, raw

    # >>> s04: parse_response in Python is woven into the streaming loop;
    #     Go counterpart: anthropic.go::parseAnthropicResponse — pure
    #     decode of the unified bytes. The finish-reason normalization
    #     table is end_turn/stop_sequence → "stop", tool_use → "tool_calls",
    #     max_tokens → "length".


# -----------------------------------------------------------------------------
# core/providers/openai_compat.py — Chat Completions / OpenAI-compatible.
# -----------------------------------------------------------------------------

# >>> s04: OpenAICompatProvider construction. Go counterpart:
#     openai.go::NewOpenAIProvider. Upstream's class is a dispatcher across
#     OpenAI proper, OpenRouter, DeepSeek, Gemini-compat, vLLM, Ollama, Kimi
#     thinking, ... — ~2000 LOC of branches. Our Go port targets the
#     intersection: POST /chat/completions with Authorization: Bearer.
class OpenAICompatProvider(LLMProvider):
    def __init__(
        self,
        api_key: str | None = None,
        api_base: str | None = None,
        default_model: str = "gpt-4o",
        extra_headers: dict[str, str] | None = None,
        spec=None,   # >>> s04: ProviderSpec — we don't port the registry
    ):
        super().__init__(api_key, api_base)
        self.default_model = default_model
        self.extra_headers = extra_headers or {}
        self._spec = spec
        # >>> s04: AsyncOpenAI client with default_headers (session-affinity
        #     + OpenRouter attribution when applicable). Our Go port writes
        #     headers explicitly per request — clearer for a teaching cut.
        self._client = AsyncOpenAI(
            api_key=api_key or "no-key",
            base_url=api_base,
            max_retries=0,
            timeout=180.0,
        )

    # >>> s04: extract_tool_calls iterates choices[0].message.tool_calls and
    #     decodes each `function.arguments` string with json_repair (a
    #     forgiving parser). Go counterpart: openai.go::parseOpenAIResponse
    #     uses stdlib json.Valid as the "is this parseable?" check and falls
    #     back to "{}" — DeepCode's json_repair is the right industrial
    #     choice; for a teaching cut, stdlib is enough.

    # >>> s04: finish-reason normalization. OpenAI vocabulary maps
    #     "stop" → FinishStop, "tool_calls" / "function_call" → FinishToolCalls,
    #     "length" → FinishLength. Go counterpart: openai.go::normalizeOpenAIStop.


# -----------------------------------------------------------------------------
# core/providers/registry.py — the keyword routing table.
# -----------------------------------------------------------------------------

# >>> s04: ProviderSpec drives `find_by_name` / `find_by_model`. Go
#     counterpart: factory.go::isAnthropicSettings — we condense the full
#     keyword scan to "model contains 'claude' or 'anthropic'" because s04
#     ships only two backends. Adding a third (Gemini, DeepSeek) is an
#     Appendix-B exercise that re-introduces this table verbatim.
@dataclass(frozen=True)
class ProviderSpec:
    name: str
    keywords: tuple[str, ...]
    env_key: str
    backend: str = "openai_compat"

PROVIDERS = (
    ProviderSpec(name="anthropic",
                 keywords=("anthropic", "claude"),
                 env_key="ANTHROPIC_API_KEY",
                 backend="anthropic"),
    ProviderSpec(name="openai",
                 keywords=("openai", "gpt"),
                 env_key="OPENAI_API_KEY",
                 backend="openai_compat"),
    # >>> s04: ... openrouter, deepseek, gemini, zhipu, dashscope, vllm,
    #     ollama — all `backend="openai_compat"`. The pattern: one
    #     OpenAI-compatible class, many ProviderSpecs. Our Go port keeps
    #     two named structs; the multi-provider story arrives in Appendix B
    #     exercise #1.
)


def find_by_model(model, available_provider_names=None):
    """Match a provider spec by model name keywords."""
    if not model:
        return None
    model_lower = model.lower()
    # >>> s04: prefix match (model="anthropic/claude-haiku") then keyword
    #     scan over spec.keywords. Our Go simplification:
    #         strings.Contains(model, "claude") || strings.Contains(model, "anthropic")
    #     For a real port, see Appendix B exercise #1.
    for spec in PROVIDERS:
        if any(kw in model_lower for kw in spec.keywords):
            return spec
    return None


# =============================================================================
# Reading order suggestion:
#   1. base.py — see the contract (LLMProvider ABC + LLMResponse)
#   2. anthropic.py L26-200 — see one impl
#   3. openai_compat.py L1-300 — see the same shape with a different wire
#   4. registry.py — see how strings become impl choices
# =============================================================================
