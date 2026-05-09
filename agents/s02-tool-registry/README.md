# s02 — tool-registry

> A typed `Tool` interface + thread-safe `Registry` with cached schema and explicit `Close()` lifecycle. The plumbing that lets s06+ dispatch tool calls.

## What this is

DeepCode's `core/agent_runtime/tools/registry.py` does three things:

1. **Holds tools by name.** `register(tool)` / `get(name)` / `unregister(name)`.
2. **Caches the JSON-Schema list** the LLM sees, sorted (builtins first, `mcp_*` second, alphabetical within each group).
3. **Owns subprocess lifetime.** When an MCP stdio server is registered, the registry is responsible for tearing it down via `aclose()`.

s02 ports those three responsibilities to ~250 lines of Go. The runner in s06 will compose this with the provider in s04 to make a full tool-using agent.

## Run it

```bash
cd agents/s02-tool-registry
go run . -v echo '{"text":"hello tools"}'
go run . now
go run . -v mcp_demo '{}'
```

Verbose mode lists registered schemas to stderr.

## Test it

```bash
go test -v ./...
```

5 PASS, ~1s. No network calls.

## File map

- [`tool.go`](tool.go) — `Tool` interface, `ToolSchema`, `CloserTool`, `ErrToolNotFound`
- [`registry.go`](registry.go) — `Registry` with cached `List()`, aggregating `Close()`
- [`builtins.go`](builtins.go) — `EchoTool`, `NowTool`, `MCPSubprocessTool` (closer demo)
- [`registry_test.go`](registry_test.go) — five tests covering the round-trip, ordering, lifecycle, replacement, and unknown-tool dispatch

## What's deliberately absent

| Feature | Where it shows up |
|---|---|
| Real MCP stdio (subprocess + JSON-RPC framing) | Appendix B exercise #5 — kept here as `MCPSubprocessTool` simulation |
| `prepare_call` validation (param casting / errors) | s06 — the runner layer |
| `execute()` with [Analyze the error above…] hint | s06 — that's loop-level, not registry-level |
| MCP `attach_server_stack` / `aclose` proxy | Out of scope — the `CloserTool` interface is the abstraction; real per-server stacks are an Appendix B extension |

## Upstream reference

- `core/agent_runtime/tools/registry.py:11-130` — full `ToolRegistry` class.
- See [`docs/zh/s02-tool-registry.md`](../../docs/zh/s02-tool-registry.md) and [`docs/en/s02-tool-registry.md`](../../docs/en/s02-tool-registry.md) for the lesson.
- Annotated upstream: [`upstream-readings/s02-registry.py`](../../upstream-readings/s02-registry.py).
