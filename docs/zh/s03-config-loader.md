---
title: "s03 · 单 JSON 配置 + 阶段覆盖"
chapter: 03
slug: s03-config-loader
est_read_min: 12
---

# s03 · 单 JSON 配置 + 阶段覆盖

> 一个 `Config` 结构 + 一个 `Load(ctx, path)` + 一个 `Resolve(phase)`。`${ENV_VAR}` 在加载时一次性展开；phase 覆盖在解析后逐字段叠加。整个程序里**只有 `Load` 摸 `os.Environ`**，这是 s03 真正要建立的纪律。

---

## Problem

s01 把 API key 当字面量塞进源码；s02 的工具 schema 全是 hard-code。这两条路在第三次 demo 之前就走不通了：

- 真实部署里，**每个 phase 用不同的 model**——planning 用 Claude Sonnet（推理强），implementation 用 GPT 系列（速度快、token 便宜）。在 main.go 里写 `if phase == "planning" { model = "..." }` 会随 phase 数量爆炸。
- API key 不该出现在 git 仓库里——必须从环境变量来；但又不能让程序里到处散落 `os.Getenv("OPENAI_API_KEY")`，否则没人知道一个 key 被读了几次、谁读的。
- 阶段覆盖是**部分覆盖**——planning 可能只想换 model，maxTokens 和 temperature 沿用 defaults。一个朴素的 "全字段覆盖" 不够用。

上游 `core/config.py` 用 ~250 行 Python（Pydantic + BaseSettings）解决这三件事：

1. 一个 `DeepCodeConfig` 树（`agents` / `providers` / `tools` / `workspace` / `documentSegmentation` / `logger`）；
2. 一个 `_resolve_env_refs` 递归把 `${VAR}` 替换成 `os.environ[VAR]`；
3. 一个 `resolve_phase(phase)`：把 `agents.defaults` 和 `agents.<phase>` 字段级合并。

s03 把这三件事拆给 Go：`config.go`（结构体）/`load.go`（加载 + 替换）/`resolve.go`（合并）。每段都能单独测，没有反射。

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

三个关键设计决策：

1. **`AgentPhase` 字段全是指针**（`*string` / `*int` / `*float64`）。这是为了区分 "JSON 里没写这个字段" 和 "写了，但等于零值"。Python 用 `Optional[T] = None` 做这件事；Go 没有 Optional，但指针的 nil 等价。
2. **`expandEnv` 在原始 JSON 字节上跑正则**，而不是先 Unmarshal 再递归走树。一次性、零反射、所有字符串值都覆盖。代价：理论上一个出现在 JSON key 里的 `${VAR}` 也会被展开——上游 Python 只走 value，但实务里没有人把 `${...}` 当 key，所以我们接受这个略宽的语义。
3. **`Resolve` 是手写的逐字段叠加**，不用反射。四个 `if override.X != nil` 块比 `reflect.Value.FieldByName` 短、好读、易测、报错栈在编辑器里直接能跳转。

## How It Works

### 三个文件，三件事

`config.go` 定义结构体树，每个 `json` tag 对齐上游 camelCase：

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

`AgentsConfig` 定义了一个自定义 `UnmarshalJSON`，把 `"defaults"` 之外的所有 key 都当作 phase override（实测 JSON 里就是 `"planning": {...}`、`"implementation": {...}` 平铺在 `"agents"` 下）：

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

### `expandEnv` —— 唯一摸环境变量的地方

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
        escaped, _ := json.Marshal(val)        // 转义 "
        return escaped[1 : len(escaped)-1]     // 去掉外层引号
    })
    if miss != nil { return nil, miss }
    return out, nil
}
```

**`json.Marshal(val)` 是关键的一步**：如果 secret 里碰巧有 `"`，直接拼回去会破坏外层 JSON 字符串字面量。让 `json.Marshal` 帮我们转义，再剥掉它加上的外层引号——这是 Go 里"安全的字符串嵌入 JSON"的标准做法。

### `Resolve` —— 字段级叠加

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
                                          (nil = 沿用 defaults)

  resolved planning = { auto, claude-sonnet, 32000, 0.1 }
```

**4 个非显然点**：

1. **JSON 解码可以"宽松"**——`encoding/json` 默认忽略未知字段，所以未来上游加 `reasoningEffort` / `baseMaxTokens` 不会让 s03 失败。这是 stdlib 给的默认行为，不是我们写的。
2. **`expandEnv` 在 Unmarshal 前跑**——不是在每个字符串字段后处理。一遍 regex 比 N 遍递归走树快、简单、对所有字符串字段都生效。
3. **`Resolve` 是 pure function**——同一个 `*Config` 调两次返回 `==` 的结果（测试里用 `reflect.DeepEqual` + 普通 `==` 双重断言）。这意味着调用方可以放心缓存结果，不必担心隐式状态变化。
4. **唯一一处 `os.LookupEnv` 在 `expandEnv` 里**——程序里其它任何地方都不应该读环境变量。这条纪律到 s10 还要继续守。

## What Changed (vs. s02)

```diff
+ config.go      Config 结构体树（agents / providers / tools / workspace / ...）
+ load.go        Load(ctx, path) + expandEnv + *MissingEnvError
+ resolve.go     (c *Config).Resolve(phase) AgentSettings
+ testdata/*.json  trimmed 版上游 example + 单字段 ${TEST_VAR} 配置
+ context.Context 进入 I/O 入口签名（即使目前只用作 Err() 检查）
+ 引入"typed error"模式：MissingEnvError 让调用方 errors.As 后给出精确 CLI 报错
- 没有 LLM、没有 HTTP、没有 subprocess —— 纯解析 + 合并
```

s02 关心 **catalog 层**（工具的目录），s03 关心 **配置层**（怎么把外部知识喂进来）。两层解耦。s04 会把 s03 的 `AgentSettings` 当 ctor 参数传给 Provider。

## Try It

```bash
cd agents/s03-config-loader

# 看看 planning 阶段的 resolved settings
OPENAI_API_KEY=sk-test go run . -phase planning testdata/deepcode_config.json

# defaults
OPENAI_API_KEY=sk-test go run . testdata/deepcode_config.json

# 跑测试
go test -v ./...
```

`-phase planning` 期望 stdout：

```json
{
  "Provider": "auto",
  "Model": "anthropic/claude-sonnet-4-20250514",
  "MaxTokens": 32000,
  "Temperature": 0.1
}
```

观察：**Model + MaxTokens 来自 planning 覆盖；Provider + Temperature 沿用 defaults**——这就是 phase 合并。

测试：5 PASS，<1s，无网络。

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

**阅读笔记**：

- **Pydantic 的 alias_generator + populate_by_name 是 camelCase ↔ snake_case 桥**：上游 `_Base` 用 `to_camel` 让 JSON 写 `maxTokens`、Python 读 `max_tokens`。Go 没这个矛盾——结构体字段是 `MaxTokens`，json tag 直接写 `maxTokens`，一一对应。
- **`_resolve_env_refs` 走 dict / list / str 三种情况是因为 Python 解析后的对象就是这三类**：先 Unmarshal、再走树。我们 Go 反过来：先在 raw JSON bytes 上跑 regex、再 Unmarshal。一次代价 vs. 两次代价的取舍。
- **`BaseSettings` 还会自动读 `DEEPCODE_*` 环境变量**（`env_prefix="DEEPCODE_"`）作为对 JSON 的覆盖。我们故意不复刻这个机制——`${VAR}` 已经覆盖了 99% 的 "把 secret 从外面注入进来" 场景，多一层 magic 反而难调试。如果学习者真的需要，Appendix B 会给出 hook 的位置。
- **`make_llm_provider` 在 L552-L618** 做的是"已 resolve 的 settings → 实例化 Provider"——那是 s04 的作业。s03 的 AgentSettings 是它的输入。

**继续读**：从 `load_config` 进 `make_llm_provider`（L552），再进 `core/providers/registry.py` 的 `PROVIDERS` 列表——看上游怎么把"openai/gpt-5.4" 这种 model 字符串映射到具体 ProviderSpec。注解版：[`upstream-readings/s03-config.py`](../../upstream-readings/s03-config.py)。

---

**下一章**：s04 把 `AgentSettings` 当 ctor 参数，构造 Anthropic + OpenAI 两个 `Provider` 实现，把 s01 的裸 HTTP 调用升级成接口。
