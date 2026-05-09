# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  File: core/config.py  (L1-L350, abridged)
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
# =============================================================================
"""DeepCode runtime configuration (single JSON file)."""

# >>> s03: Pydantic + BaseSettings is the upstream toolkit. In Go we use
#     struct tags (`json:"maxTokens"`) plus encoding/json. No reflection
#     code in OUR codebase — the Go stdlib does it for us.
import json
import os
import re
from dataclasses import dataclass
from typing import Any

from pydantic import BaseModel, ConfigDict, Field
from pydantic.alias_generators import to_camel
from pydantic_settings import BaseSettings


# >>> s03: this regex is the heart of `${ENV_VAR}` interpolation. Our Go
#     counterpart is `envRefPattern` in load.go — we tightened it to
#     uppercase + underscore to discourage typos like ${path}.
_ENV_REF_PATTERN = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}")


# =============================================================================
# >>> s03: AgentDefaults — every phase inherits these unless overridden.
#     Go counterpart: `AgentDefaults` in config.go. Concrete fields, not
#     pointers — defaults are always present so a missing field is the zero
#     value, which is fine.
# =============================================================================
class AgentDefaults(BaseModel):
    model_config = ConfigDict(alias_generator=to_camel, populate_by_name=True, extra="ignore")
    provider: str = "auto"
    model: str = "openai/gpt-4o-mini"
    max_tokens: int = 8192
    temperature: float = 0.1
    reasoning_effort: str | None = None
    # Token-policy fields used by the adaptive retry loop.
    base_max_tokens: int | None = None
    retry_max_tokens: int | None = None
    max_tokens_policy: str | None = None
    # Runner ergonomics.
    max_tool_iterations: int = 200
    max_tool_result_chars: int = 16_000
    context_window_tokens: int = 65_536


# =============================================================================
# >>> s03: AgentPhase — overrides only. Every field is `T | None = None`.
#     This is THE reason our Go AgentPhase uses *string / *int / *float64
#     instead of the concrete types: we need to distinguish "field missing
#     from JSON" from "field present with the zero value".
# =============================================================================
class AgentPhase(BaseModel):
    model_config = ConfigDict(alias_generator=to_camel, populate_by_name=True, extra="ignore")
    provider: str | None = None
    model: str | None = None
    max_tokens: int | None = None
    temperature: float | None = None
    reasoning_effort: str | None = None


class AgentsConfig(BaseModel):
    # >>> s03: upstream lists planning + implementation as fixed properties.
    #     Our Go widens to a map so `c.Resolve("review")` works without a
    #     code change — minor enhancement, same defaulting semantics.
    defaults: AgentDefaults = Field(default_factory=AgentDefaults)
    planning: AgentPhase = Field(default_factory=AgentPhase)
    implementation: AgentPhase = Field(default_factory=AgentPhase)


# =============================================================================
# >>> s03: ResolvedAgentSettings — frozen dataclass for the merged view.
#     Our Go counterpart `AgentSettings` (resolve.go) uses concrete fields
#     (no pointers) because by construction every field is set after
#     Resolve().
# =============================================================================
@dataclass(frozen=True, slots=True)
class ResolvedAgentSettings:
    provider: str
    model: str
    max_tokens: int
    temperature: float
    reasoning_effort: str | None
    base_max_tokens: int | None
    retry_max_tokens: int | None
    max_tokens_policy: str | None


# =============================================================================
# >>> s03: ProviderConfig — apiKey may be a literal or a "${ENV_VAR}".
#     `extra_headers` is a free-form dict for routing tweaks (e.g. OpenRouter).
# =============================================================================
class ProviderConfig(BaseModel):
    model_config = ConfigDict(alias_generator=to_camel, populate_by_name=True, extra="ignore")
    api_key: str | None = None
    api_base: str | None = None
    extra_headers: dict[str, str] | None = None


# =============================================================================
# >>> s03: ProvidersConfig — explicit per-provider blocks. Python pins the
#     supported list at the type level; our Go uses `map[string]ProviderConfig`
#     keyed by name. Trade-off: less compile-time safety, more flexibility.
# =============================================================================
class ProvidersConfig(BaseModel):
    custom: ProviderConfig = Field(default_factory=ProviderConfig)
    openrouter: ProviderConfig = Field(default_factory=ProviderConfig)
    anthropic: ProviderConfig = Field(default_factory=ProviderConfig)
    openai: ProviderConfig = Field(default_factory=ProviderConfig)
    deepseek: ProviderConfig = Field(default_factory=ProviderConfig)
    gemini: ProviderConfig = Field(default_factory=ProviderConfig)


# =============================================================================
# >>> s03: DeepCodeConfig — the root. BaseSettings auto-reads env-prefixed
#     overrides (DEEPCODE_AGENTS__DEFAULTS__MODEL=...). We don't replicate
#     that env-prefix mechanism in Go — `${VAR}` interpolation inside the
#     JSON covers the same ground with one less layer of magic.
# =============================================================================
class DeepCodeConfig(BaseSettings):
    agents: AgentsConfig = Field(default_factory=AgentsConfig)
    providers: ProvidersConfig = Field(default_factory=ProvidersConfig)
    # ... tools, workspace, documentSegmentation, logger, llmLogger ...

    # =========================================================================
    # >>> s03: resolve_phase — the algorithm we port verbatim. For each
    #     field name, prefer the override if set, else fall back to defaults.
    #     Our Go does this without reflection: each field gets its own
    #     `if override.X != nil { out.X = *override.X }` block.
    # =========================================================================
    def resolve_phase(self, phase: str = "default") -> ResolvedAgentSettings:
        defaults = self.agents.defaults
        if phase == "planning":
            override = self.agents.planning
        elif phase == "implementation":
            override = self.agents.implementation
        else:
            override = None

        def _pick(name: str) -> Any:
            if override is not None:
                value = getattr(override, name)
                if value is not None:
                    return value
            return getattr(defaults, name)

        return ResolvedAgentSettings(
            provider=_pick("provider"),
            model=_pick("model"),
            max_tokens=_pick("max_tokens"),
            temperature=_pick("temperature"),
            reasoning_effort=_pick("reasoning_effort"),
            base_max_tokens=defaults.base_max_tokens,
            retry_max_tokens=defaults.retry_max_tokens,
            max_tokens_policy=defaults.max_tokens_policy,
        )


# =============================================================================
# >>> s03: _resolve_env_refs — recursive walk over the parsed dict.
#     Our Go version is simpler: we run the regex over the raw JSON BYTES
#     before Unmarshal. Trade-off:
#       - PRO: one pass, no recursive walk, no per-type branching.
#       - CON: a "${VAR}" inside a JSON KEY (not just a value) gets expanded.
#         Upstream Python only walks values; we accept the broader behaviour
#         because in practice no key ever contains a "${...}" pattern.
# =============================================================================
def _resolve_env_refs(value: Any, *, path: str = "") -> Any:
    if isinstance(value, str):
        def _replace(match: re.Match[str]) -> str:
            name = match.group(1)
            env_value = os.environ.get(name)
            if env_value is None:
                where = f" at {path}" if path else ""
                raise ValueError(
                    f"Environment variable '{name}' referenced in deepcode_config.json{where} is not set"
                )
            return env_value
        return _ENV_REF_PATTERN.sub(_replace, value)
    if isinstance(value, dict):
        return {k: _resolve_env_refs(v, path=f"{path}.{k}" if path else k) for k, v in value.items()}
    if isinstance(value, list):
        return [_resolve_env_refs(item, path=f"{path}[{i}]") for i, item in enumerate(value)]
    return value


# =============================================================================
# >>> s03: load_config — read JSON, expand env refs, validate.
#     Our Go counterpart `Load(ctx, path)` is the same shape minus the
#     "walk up parent dirs to find deepcode_config.json" convenience —
#     we always require an explicit path so tests are unambiguous.
# =============================================================================
def load_config(config_path: str | None = None) -> DeepCodeConfig:
    raw: dict[str, Any] = {}
    if config_path and os.path.exists(config_path):
        with open(config_path, "r", encoding="utf-8") as fh:
            raw = json.load(fh) or {}
    raw = _resolve_env_refs(raw)
    return DeepCodeConfig.model_validate(raw)


# =============================================================================
# Read further:
#   1. core/config.py:520-620 — `make_llm_provider`. Resolves a phase, picks
#      the matching ProviderSpec, instantiates Anthropic/OpenAICompat. That
#      logic is split across s04 (provider abstraction) in our curriculum.
#   2. core/providers/registry.py — the ProviderSpec list + `find_by_name`.
#      We don't port the whole registry; s04 picks impl by model-prefix.
#   3. nanobot/config/schema.py — the upstream-of-upstream: DeepCode borrowed
#      the camelCase + alias_generator pattern from nanobot.
# =============================================================================
