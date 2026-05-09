---
title: "s03 · Single-JSON config + phase overrides"
chapter: 03
slug: s03-config-loader
est_read_min: 12
---

# s03 · Single-JSON config + phase overrides

> One `Config` struct, one `Load(ctx, path)`, one `Resolve(phase)`. `${ENV_VAR}` is expanded once at load time; the phase overlay is a hand-written field-by-field merge after parse. The discipline s03 really installs: **only `Load` ever touches `os.Environ`** — every other file reads from the typed config.

---

## Problem

s01 baked the API key into source as a literal; s02's tool schemas were hard-coded. Both habits collapse before the third demo:

- A real deployment **uses a different model per phase** — Claude Sonnet for planning (strong reasoning), GPT for implementation (fast, cheap tokens). Sprinkling `if phase == "planning" { model = "..." }` across `main.go` doesn't scale past two phases.
- API keys must not live in the git repo — they have to come from the environment. But scattering `os.Getenv("OPENAI_API_KEY")` across every file breaks observability: nobody can tell who reads what when.
- Phase overrides are **partial** — planning may swap only the model; maxTokens and temperature should fall back to defaults. A naive "full-record overwrite" is wrong.

Upstream's `core/config.py` solves all three in ~250 Python lines (Pydantic + `BaseSettings`):

1. A `DeepCodeConfig` tree (`agents` / `providers` / `tools` / `workspace` / ...).
2. `_resolve_env_refs` recursively replaces `${VAR}` with `os.environ[VAR]`.
3. `resolve_phase(phase)` field-merges `agents.<phase>` over `agents.defaults`.

s03 splits that across three Go files: `config.go` (structs), `load.go` (read + expand), `resolve.go` (merge). Each is testable on its own. No reflect.

## Solution

```ascii-anim frames=1
┌──────────────────────────────────────────────────────────────────┐
│  ./deepcode_config.json   (text on disk)                         │
│           │                                                      │
│           ▼   os.ReadFile                                        │
│  raw JSON bytes                                                  │
│           │                                                      │
│           ▼   expandEnv(raw)         ← regex `\${[A-Z_]\w*}`     │
│  expanded JSON bytes                                             │
│           │                                                      │
│           ▼   json.Unmarshal                                     │
│  *Config  { Agents{Defaults, Phases:map}, Providers, Tools,…}   │
│           │                                                      │
│           ▼   c.Resolve("planning")                              │
│  AgentSettings { Provider, Model, MaxTokens, Temperature }       │
└──────────────────────────────────────────────────────────────────┘
```

Three load-bearing design choices:

1. **Every `AgentPhase` field is a pointer** (`*string` / `*int` / `*float64`). This distinguishes "field absent from JSON" from "field present with the zero value". Python uses `Optional[T] = None`; Go has no Optional, but `nil` on a pointer is the structural equivalent.
2. **`expandEnv` runs the regex over raw JSON bytes**, not over the parsed tree. One pass, no reflection, every string value covered. Trade-off: a `${VAR}` that happens to appear inside a JSON key would also be expanded — upstream walks values only, but in practice no key contains `${...}`, so we accept the slightly broader semantics.
3. **`Resolve` is a hand-written field-by-field overlay**, no reflection. Four `if override.X != nil` blocks are shorter, more readable, and easier to test than `reflect.Value.FieldByName`. They also produce useful jump-to-line stack frames when something breaks.

## How It Works

### Three files, three responsibilities

`config.go` declares the struct tree; every `json` tag mirrors upstream's camelCase:

```go
type Config struct {
    Agents               AgentsConfig               `json:"agents"`
    Providers            map[string]ProviderConfig  `json:"providers"`
    Tools                ToolsConfig                `json:"tools"`
    Workspace            WorkspaceConfig            `json:"workspace"`
    DocumentSegmentation DocumentSegmentationConfig `json:"documentSegmentation"`
    Logger               LoggerConfig               `json:"logger"`
}

type AgentDefaults struct {
    Provider    string  `json:"provider"`
    Model       string  `json:"model"`
    MaxTokens   int     `json:"maxTokens"`
    Temperature float64 `json:"temperature"`
}

type AgentPhase struct {
    Provider    *string  `json:"provider,omitempty"`
    Model       *string  `json:"model,omitempty"`
    MaxTokens   *int     `json:"maxTokens,omitempty"`
    Temperature *float64 `json:"temperature,omitempty"`
}
```

`AgentsConfig` ships a custom `UnmarshalJSON` that splits keys: `"defaults"` becomes `Defaults`; everything else (`planning`, `implementation`, plus any future phase) lands in the `Phases` map. This matches upstream's flat layout where phase keys are siblings of `"defaults"` inside `"agents"`:

```go
func (a *AgentsConfig) UnmarshalJSON(b []byte) error {
    var raw map[string]json.RawMessage
    if err := json.Unmarshal(b, &raw); err != nil {
        return err
    }
    a.Phases = map[string]AgentPhase{}
    for k, v := range raw {
        if k == "defaults" {
            json.Unmarshal(v, &a.Defaults)
            continue
        }
        var p AgentPhase
        json.Unmarshal(v, &p)
        a.Phases[k] = p
    }
    return nil
}
```

### `expandEnv` — the only place that touches the environment

```go
var envRefPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

type MissingEnvError struct{ Name string }
func (e *MissingEnvError) Error() string {
    return fmt.Sprintf("environment variable %q referenced in config is not set", e.Name)
}

func expandEnv(b []byte) ([]byte, error) {
    var miss *MissingEnvError
    out := envRefPattern.ReplaceAllFunc(b, func(match []byte) []byte {
        name := string(match[2 : len(match)-1])
        val, ok := os.LookupEnv(name)
        if !ok {
            if miss == nil { miss = &MissingEnvError{Name: name} }
            return match
        }
        escaped, _ := json.Marshal(val)        // escape "
        return escaped[1 : len(escaped)-1]     // strip outer quotes
    })
    if miss != nil { return nil, miss }
    return out, nil
}
```

**`json.Marshal(val)` matters**: if a secret happens to contain a `"`, splicing it raw into the byte stream would break the surrounding JSON string literal. Letting `json.Marshal` escape it (then stripping the outer quotes it adds) is the idiomatic Go way to embed a string into JSON safely.

### `Resolve` — the field-by-field overlay

```go
func (c *Config) Resolve(phase string) AgentSettings {
    d := c.Agents.Defaults
    out := AgentSettings{
        Provider: d.Provider, Model: d.Model,
        MaxTokens: d.MaxTokens, Temperature: d.Temperature,
    }
    override, ok := c.Agents.Phases[phase]
    if !ok { return out }
    if override.Provider != nil    { out.Provider    = *override.Provider }
    if override.Model != nil       { out.Model       = *override.Model }
    if override.MaxTokens != nil   { out.MaxTokens   = *override.MaxTokens }
    if override.Temperature != nil { out.Temperature = *override.Temperature }
    return out
}
```

```ascii-anim frames=1
                  +─── defaults ───+      +── planning override ──+
                  │ provider: auto │      │ provider: nil         │
                  │ model: gpt-5.4 │  ⊕   │ model: claude-sonnet  │  =
                  │ maxTokens:40000│      │ maxTokens: 32000      │
                  │ temp: 0.1      │      │ temp: nil             │
                  +────────────────+      +───────────────────────+
                                          (nil = inherit defaults)

  resolved planning = { auto, claude-sonnet, 32000, 0.1 }
```

**4 non-obvious points**:

1. **JSON decoding is permissive by default** — `encoding/json` ignores unknown fields, so an upstream addition like `reasoningEffort` or `baseMaxTokens` never breaks s03. We get this for free from the stdlib.
2. **`expandEnv` runs before `Unmarshal`** — not after, on each string field. One regex pass beats N walks of the parsed tree, and it covers every string value uniformly.
3. **`Resolve` is a pure function** — calling it twice on the same `*Config` returns `==` structs (the test suite asserts this with both `reflect.DeepEqual` and plain `==`). Callers can cache the result without worrying about hidden state.
4. **`os.LookupEnv` lives only inside `expandEnv`** — no other file in the program reads from the environment. This discipline persists through s10.

## What Changed (vs. s02)

```diff
+ config.go      Config struct tree (agents / providers / tools / workspace / ...)
+ load.go        Load(ctx, path) + expandEnv + *MissingEnvError
+ resolve.go     (c *Config).Resolve(phase) AgentSettings
+ testdata/*.json  trimmed copy of upstream example + minimal ${TEST_VAR} config
+ context.Context enters every I/O entry signature (currently used only for Err())
+ introduces the typed-error pattern: callers errors.As to surface a precise CLI message
- no LLM, no HTTP, no subprocess — pure parse + merge
```

s02 lived in the **catalog layer** (the tool directory). s03 lives in the **configuration layer** (how external knowledge enters the program). Independent. s04 will pass `AgentSettings` to its provider constructors.

## Try It

```bash
cd agents/s03-config-loader

# Resolved settings for the planning phase
OPENAI_API_KEY=sk-test go run . -phase planning testdata/deepcode_config.json

# Defaults
OPENAI_API_KEY=sk-test go run . testdata/deepcode_config.json

# Tests
go test -v ./...
```

Expected stdout for `-phase planning`:

```json
{
  "Provider": "auto",
  "Model": "anthropic/claude-sonnet-4-20250514",
  "MaxTokens": 32000,
  "Temperature": 0.1
}
```

Observe: **Model + MaxTokens come from the planning override; Provider + Temperature inherit from defaults** — that's the phase merge.

Tests: 5 PASS, <1s, no network.

## Upstream Source Reading

```upstream:core/config.py#L72-L117
class AgentDefaults(_Base):
    provider: str = "auto"
    model: str = "openai/gpt-4o-mini"
    max_tokens: int = 8192
    temperature: float = 0.1
    reasoning_effort: str | None = None
    # ... base_max_tokens, retry_max_tokens, max_tokens_policy ...

class AgentPhase(_Base):
    provider: str | None = None
    model: str | None = None
    max_tokens: int | None = None
    temperature: float | None = None
    reasoning_effort: str | None = None

class AgentsConfig(_Base):
    defaults: AgentDefaults = Field(default_factory=AgentDefaults)
    planning: AgentPhase    = Field(default_factory=AgentPhase)
    implementation: AgentPhase = Field(default_factory=AgentPhase)

@dataclass(frozen=True, slots=True)
class ResolvedAgentSettings:
    provider: str
    model: str
    max_tokens: int
    temperature: float
    # ...
```

```upstream:core/config.py#L320-L347
def resolve_phase(self, phase: str = "default") -> ResolvedAgentSettings:
    defaults = self.agents.defaults
    if phase == "planning":         override = self.agents.planning
    elif phase == "implementation": override = self.agents.implementation
    else:                           override = None

    def _pick(name: str) -> Any:
        if override is not None:
            value = getattr(override, name)
            if value is not None: return value
        return getattr(defaults, name)

    return ResolvedAgentSettings(
        provider=_pick("provider"),
        model=_pick("model"),
        max_tokens=_pick("max_tokens"),
        temperature=_pick("temperature"),
        # ...
    )
```

```upstream:core/config.py#L462-L516
def _resolve_env_refs(value: Any, *, path: str = "") -> Any:
    if isinstance(value, str):
        def _replace(match):
            name = match.group(1)
            env_value = os.environ.get(name)
            if env_value is None:
                raise ValueError(
                    f"Environment variable '{name}' referenced in deepcode_config.json"
                    f" at {path} is not set")
            return env_value
        return _ENV_REF_PATTERN.sub(_replace, value)
    if isinstance(value, dict):
        return {k: _resolve_env_refs(v, path=f"{path}.{k}") for k, v in value.items()}
    if isinstance(value, list):
        return [_resolve_env_refs(item, path=f"{path}[{i}]") for i, item in enumerate(value)]
    return value

def load_config(config_path) -> DeepCodeConfig:
    raw = json.load(open(config_path)) if os.path.exists(config_path) else {}
    raw = _resolve_env_refs(raw)
    return DeepCodeConfig.model_validate(raw)
```

**Reading notes**:

- **Pydantic's `alias_generator=to_camel` + `populate_by_name=True` is the camelCase ↔ snake_case bridge.** Upstream's `_Base` lets the JSON spell `maxTokens` and Python read `max_tokens`. Go has no such tension — the field is `MaxTokens`, the json tag is `"maxTokens"`, one-to-one and explicit.
- **`_resolve_env_refs` walks dict / list / str because that's what JSON-decoded data looks like in Python.** Decode first, then walk. We invert: regex-pass on raw bytes, then decode. One traversal versus two.
- **`BaseSettings` also auto-reads `DEEPCODE_*` env vars** (`env_prefix="DEEPCODE_"`) as overlays on top of the JSON. We deliberately don't port that: `${VAR}` already covers 99% of "inject a secret from outside" use-cases, and a second mechanism on top makes debugging harder. If a learner truly needs it, Appendix B points at the hook site.
- **`make_llm_provider` (L552-L618) does the "resolved settings → instantiated Provider" step** — that's s04's job. s03's `AgentSettings` is its input.

**Read further**: from `load_config` follow into `make_llm_provider` (L552), then into `core/providers/registry.py`'s `PROVIDERS` list — how upstream maps a model string like "openai/gpt-5.4" to a concrete `ProviderSpec`. Annotated copy: [`upstream-readings/s03-config.py`](../../upstream-readings/s03-config.py).

---

**Next**: s04 takes `AgentSettings` as a constructor input and builds two concrete `Provider` implementations (Anthropic + OpenAI), upgrading s01's bare HTTP call into a typed interface.
