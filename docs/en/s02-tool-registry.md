---
title: "s02 · Tool registry"
chapter: 02
slug: s02-tool-registry
est_read_min: 10
---

# s02 · Tool registry

> A typed `Tool` interface plus a thread-safe `Registry` with cached schema and explicit subprocess lifecycle. The plumbing s06 will compose with s04's Provider into a full tool-using agent.

---

## Problem

The s01 agent can only "talk" — one prompt in, one text out. A useful agent must **call tools**: read files, run shells, query DBs, hit MCP servers. Each tool has its own schema; some hold external resources (subprocesses, file handles); closing them in the wrong order leaks.

`core/agent_runtime/tools/registry.py` packs three responsibilities into one `ToolRegistry` class: ① stores tools by name, ② caches the JSON-Schema list the LLM sees (builtins first, `mcp_*` second, alphabetical within each group), ③ owns MCP stdio subprocess lifetime via `AsyncExitStack`. If you scatter these into the agent loop, complexity explodes. s02 carves them out as a stand-alone, testable abstraction.

## Solution

We build a `Registry` in ~250 lines of Go:

1. **`Tool` is an interface, not a base class** — Go has no inheritance. Anything implementing `Name() / Schema() / Run()` is a Tool. Implicit satisfaction means `EchoTool` doesn't have to declare anything special.
2. **`io.Closer` reuses stdlib** — upstream Python uses `AsyncExitStack`; we use Go's standard `io.Closer`, detected at registration, drained by `Registry.Close()` with `errors.Join`. No new abstraction invented.
3. **schema cache uses `nil` sentinel + `sync.Mutex`** — `r.cached = nil` means "rebuild on next List()". Concurrency safety via the mutex. Direct translation of Python's `_cached_definitions: list | None = None`.

Once these three trade-offs land, you'll see in s06 how the runner does `r.Get(name)` → `tool.Run(ctx, args)` and `r.Close()` happens at main exit.

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

The core ~50 lines (excerpt from [`agents/s02-tool-registry/registry.go`](https://github.com/Ding-Ye/learn-DeepCode/blob/main/agents/s02-tool-registry/registry.go)):

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

**4 non-obvious points**:

1. **`List()` returns a copy, not a slice alias** — Go slices are reference types; without copying, callers could mutate the cache. Defense in depth.
2. **`Close()` is once-only** — after `r.closers = nil`, subsequent calls are no-ops. Re-registering the same closer registers it again, matching upstream `AsyncExitStack` semantics: "close everything ever attached".
3. **`mcp_` prefix is a convention, not a type** — same as upstream. A name starting with `mcp_` sorts to the back. The LLM's prompt becomes more predictable: builtins first, dynamic remote tools second.
4. **`CloserTool` composes `Tool + io.Closer`** — instead of inventing a new interface. `EchoTool` (no resources) stays a plain struct; only resource-holding tools rise to `CloserTool`. Go's interface satisfaction is structural, not declared.

## What Changed (vs. s01)

```diff
+ tool.go        introduces Tool interface + ToolSchema struct
+ registry.go    thread-safe map + cache + lifecycle
+ builtins.go    EchoTool / NowTool (stateless) + MCPSubprocessTool (CloserTool)
- no LLM dependency — this chapter is pure data + lifecycle
+ tests use a local customEcho struct to verify replacement semantics
```

s01 was about the **protocol layer**; s02 is about the **catalog layer**. They're independent; s06 joins them.

## Try It

```bash
cd agents/s02-tool-registry

# List schemas + call echo
go run . -v echo '{"text":"hello tools"}'

# No-arg now
go run . now

# Simulated MCP tool
go run . -v mcp_demo '{}'

# Tests
go test -v ./...
```

Expected stdout (echo):

```
hello tools
```

Expected stderr (`-v`):

```
[s02] 3 tool(s) registered:
  - echo
  - now
  - mcp_demo
```

Tests: 5 PASS.

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

**Reading notes**:

- **What `AsyncExitStack` buys you**: upstream stacks "owned" things and drains them all in one `await aclose_all()`. We collapse that into `[]CloserTool` + `errors.Join` — **no** stack-based ordering semantics, because Go's `io.Closer` doesn't piggyback on the async-context-manager protocol.
- **`_owned_server_stacks` per-server stack**: upstream can attach a stack per MCP server (`attach_server_stack`) for partial teardown. Our Go model also supports this — group closers at registration — but s02 doesn't demonstrate fine-grained partial teardown. That's an Appendix B exercise.
- **`prepare_call` + `execute` belong to s06**: upstream packs catalog + dispatcher into one class. We split `prepare_call` / `execute` into s06's runner layer so `Registry` is only responsible for the catalog. Cleaner dependency direction.
- **`tool.cast_params` / `tool.validate_params`**: upstream's Tool ABC has two methods for type coercion + validation. We collapse these into each Tool's own `Run(ctx, json.RawMessage)` because `json.Unmarshal` is Go's "cast + validate" idiom — no new abstraction needed.
- **Why caching matters**: every LLM round serializes the schema array into the request body. With N tools, that's N serializations per round. Builtin schemas don't change; caching cuts a per-round serialization. This is a perf win, not a correctness fix.

**Read further**: from `registry.py` follow into `core/agent_runtime/tools/base.py` for the Tool ABC (schema/cast/validate trio) — those collapse into our single `Run` in Go. Follow `prepare_call` into `core/agent_runtime/runner.py:200+` for the real dispatch (that's the code s06 will rewrite). Annotated copy: [`upstream-readings/s02-registry.py`](../../upstream-readings/s02-registry.py).

---

**Next**: s03 introduces a **single-JSON config** — provider keys, per-phase models, MCP servers all loaded from `deepcode_config.json` with `${ENV_VAR}` resolution and phase override merging.
