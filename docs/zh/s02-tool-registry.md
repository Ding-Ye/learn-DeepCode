---
title: "s02 · 工具注册表"
chapter: 02
slug: s02-tool-registry
est_read_min: 10
---

# s02 · 工具注册表

> 一个类型安全的 `Tool` 接口加一个并发安全的 `Registry`，缓存 schema、显式管理子进程生命周期。s06 会把它和 s04 的 Provider 组合成完整的 tool-using agent。

---

## Problem

s01 的 agent 只能"说话"——一个 prompt 进，一段文本出。但真正有用的 agent 必须能**调工具**：读文件、跑 shell、查数据库、调 MCP 服务器……每个工具有自己的 schema，每个工具可能持有外部资源（subprocess、文件句柄），关闭顺序错了就会泄漏。

`core/agent_runtime/tools/registry.py` 用一个 `ToolRegistry` 类承担三件事：① 按名字存工具，② 缓存 LLM 看到的 schema 列表（builtins 在前、`mcp_*` 在后，组内字母序），③ 通过 `AsyncExitStack` 管理所有它"拥有"的 MCP stdio 子进程。这三件事如果分散在 agent loop 里写，复杂度会爆炸。s02 把这三件事抽出来，独立测试。

## Solution

我们用 ~250 行 Go 实现一个 `Registry`：

1. **`Tool` 是 interface，不是 base class**——Go 没有继承。任何实现 `Name() / Schema() / Run()` 的类型都是 Tool。
2. **`io.Closer` 复用 stdlib 接口**——上游 Python 用 `AsyncExitStack`；我们用 Go 标准库的 `io.Closer`，注册时检测，`Registry.Close()` 时 `errors.Join` 聚合错误。
3. **schema 缓存用 `nil` 哨兵 + `sync.Mutex`**——`r.cached = nil` 表示需要重建；并发安全靠 mutex。这是 Python `_cached_definitions: list | None = None` 的直译。

理解这三个 trade-off，你就能在 s06 看见 runner 怎么调 `r.Get(name)` → `tool.Run(ctx, args)`，而 r.Close() 在 main 退出时收尾。

## How It Works

```ascii-anim frames=2
┌─────────────────────────────────────────────────────────┐
│  Register(EchoTool{})                                   │
│  Register(NowTool{})                                    │
│  Register(MCPSubprocessTool{"demo"})  ← CloserTool      │
│         │                                               │
│         ▼                                               │
│  Registry{                                              │
│    tools: { "echo":Tool, "now":Tool, "mcp_demo":Tool }  │
│    cached: nil  ← invalidated                           │
│    closers: [ MCPSubprocessTool ]                       │
│  }                                                      │
│         │                                               │
│         ▼  List()                                       │
│  [ echo, now, mcp_demo ]   ← builtins first, sorted     │
│         │                                               │
│         ▼  Run                                          │
│  tool.Run(ctx, json.RawMessage(args))                   │
│         │                                               │
│         ▼  Close()                                      │
│  errors.Join(c.Close() for c in closers)                │
└─────────────────────────────────────────────────────────┘
```

核心 ~50 行（节选自 [`agents/s02-tool-registry/registry.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s02-tool-registry/registry.go)）：

```go
func (r *Registry) Register(t Tool) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.tools[t.Name()] = t
    r.cached = nil
    if c, ok := t.(CloserTool); ok {
        r.closers = append(r.closers, c)
    }
}

func (r *Registry) List() []ToolSchema {
    r.mu.Lock()
    defer r.mu.Unlock()
    if r.cached != nil {
        out := make([]ToolSchema, len(r.cached))
        copy(out, r.cached)
        return out
    }
    builtins := make([]ToolSchema, 0, len(r.tools))
    mcps := make([]ToolSchema, 0)
    for _, t := range r.tools {
        s := t.Schema()
        if strings.HasPrefix(s.Name, "mcp_") {
            mcps = append(mcps, s)
        } else {
            builtins = append(builtins, s)
        }
    }
    sort.Slice(builtins, func(i, j int) bool { return builtins[i].Name < builtins[j].Name })
    sort.Slice(mcps, func(i, j int) bool { return mcps[i].Name < mcps[j].Name })
    r.cached = append(builtins, mcps...)
    out := make([]ToolSchema, len(r.cached))
    copy(out, r.cached)
    return out
}

func (r *Registry) Close() error {
    r.mu.Lock()
    closers := r.closers
    r.closers = nil
    r.mu.Unlock()
    var errs []error
    for _, c := range closers {
        if err := c.Close(); err != nil {
            errs = append(errs, err)
        }
    }
    return errors.Join(errs...)
}
```

**4 个非显然点**：

1. **`List()` 返回拷贝而不是 slice 别名**——避免调用方改了 schema 影响内部缓存。Go 切片是 ref-type，不防御一下迟早被改。
2. **`Close()` 的"once-only"语义**——`r.closers = nil` 后再调 Close 不会重复关闭。同一个 closer 注册两次的语义保持："注册过的全都要关掉"，这和上游 `AsyncExitStack` 的语义一致。
3. **`mcp_` 前缀是约定，不是类型**——和上游一样，名字里带 `mcp_` 就排到后面。这让 LLM 看到的 prompt 更可预测：先是熟悉的 builtin，再是 dynamic 的远程工具。
4. **`CloserTool` 复合 `Tool + io.Closer`**——而不是定义一个全新的 interface。这让 EchoTool（不需要关闭）保持简单 struct，只有真正持有资源的工具才升格为 CloserTool。Go 的 interface 满足是结构的，不是声明的。

## What Changed (vs. s01)

```diff
+ tool.go        引入 Tool interface + ToolSchema struct
+ registry.go    并发安全 map + 缓存 + 生命周期管理
+ builtins.go    EchoTool / NowTool（无状态）+ MCPSubprocessTool（CloserTool）
- 不依赖任何 LLM —— 这一节是纯数据结构 + 生命周期
+ 测试用 customEcho local struct 验证替换语义
```

s01 关注**协议层**；s02 关注**catalog 层**。两层独立，s06 把它们拼起来。

## Try It

```bash
cd agents/s02-tool-registry

# 列出 schema + 调一个 echo
go run . -v echo '{"text":"hello tools"}'

# 不带参数的 now
go run . now

# 模拟 MCP 工具
go run . -v mcp_demo '{}'

# 测试
go test -v ./...
```

期望 stdout（echo 调用）：

```
hello tools
```

期望 stderr（`-v`）：

```
[s02] 3 tool(s) registered:
  - echo
  - now
  - mcp_demo
```

测试：5 PASS。

## Upstream Source Reading

```upstream:core/agent_runtime/tools/registry.py#L11-L66
class ToolRegistry:
    def __init__(self):
        self._tools: dict[str, Tool] = {}
        self._cached_definitions: list[dict[str, Any]] | None = None
        self._exit_stack: AsyncExitStack = AsyncExitStack()
        self._owned_server_stacks: dict[str, AsyncExitStack] = {}

    def register(self, tool: Tool) -> None:
        self._tools[tool.name] = tool
        self._cached_definitions = None

    def unregister(self, name: str) -> None:
        self._tools.pop(name, None)
        self._cached_definitions = None

    def get(self, name: str) -> Tool | None:
        return self._tools.get(name)

    def get_definitions(self) -> list[dict[str, Any]]:
        if self._cached_definitions is not None:
            return self._cached_definitions
        definitions = [tool.to_schema() for tool in self._tools.values()]
        builtins, mcp_tools = [], []
        for schema in definitions:
            name = self._schema_name(schema)
            if name.startswith("mcp_"):
                mcp_tools.append(schema)
            else:
                builtins.append(schema)
        builtins.sort(key=self._schema_name)
        mcp_tools.sort(key=self._schema_name)
        self._cached_definitions = builtins + mcp_tools
        return self._cached_definitions
```

**阅读笔记**：

- **`AsyncExitStack` 的精髓**：上游用一个 stack 装所有"被持有的"东西，最后一次性 `await aclose_all()`。我们简化成 `[]CloserTool` + `errors.Join`——**没有**入栈/出栈顺序的语义，因为 Go 的 io.Closer 不依赖 async 上下文管理器协议。
- **`_owned_server_stacks` per-server stack**：上游可以为每个 MCP server attach 一个独立的 stack（`attach_server_stack`），方便 partial teardown。我们的 Go 模型也支持——把多个 closer 注册时分组就行——但 s02 暂不演示这种细粒度，留给 Appendix B 练习。
- **`prepare_call` + `execute` 的归属**：上游把 catalog 和 dispatcher 写在同一个类里。我们的 Go 把 `prepare_call` / `execute` 全部移到 s06 (runner 层)，让 Registry 只承担 catalog 责任，依赖反转更清晰。
- **`tool.cast_params` 和 `tool.validate_params`**：上游通过 Tool ABC 的两个方法做参数转换 / 校验。我们的 `Tool.Run(ctx, json.RawMessage)` 把这两步交给每个 Tool 自己的 `Run` 实现——`json.Unmarshal` 已经是 Go 的"cast + validate"标准做法。
- **schema 缓存为什么必要**：每次 LLM 请求都把 schema 数组放进 body，N 个工具就要序列化 N 次。Builtin 工具的 schema 完全不变，缓存一次能省掉每轮一次序列化。这是性能而不是正确性优化。

**继续读**：从 `registry.py` 进 `core/agent_runtime/tools/base.py` 看 Tool ABC（schema/cast/validate 三方法）——它们在 Go 里被压扁进单个 `Run`。沿 `prepare_call` 进 `core/agent_runtime/runner.py:200+` 看真正的 dispatch（这就是 s06 要重写的代码）。注解版：[`upstream-readings/s02-registry.py`](../../upstream-readings/s02-registry.py)。

---

**下一章**：s03 引入**单 JSON 配置**——provider keys、phase models、MCP servers 全部从 `deepcode_config.json` 加载，含 `${ENV_VAR}` 替换和 phase 覆盖合并。
