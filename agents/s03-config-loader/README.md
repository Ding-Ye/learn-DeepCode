# s03 — config-loader

> A typed JSON config tree with `${ENV_VAR}` resolution and phase-override merging. The single source of truth s04+ consumes to build providers and runners.

## What this is

DeepCode's `core/config.py` collapses everything a real run needs — provider keys, per-phase models, MCP server commands, workspace root — into one `deepcode_config.json`. Two small but load-bearing tricks:

1. **`${ENV_VAR}` interpolation** at load time — secrets stay out of the JSON file, env vars stay out of the rest of the program.
2. **Phase override merge** — `agents.defaults` is the base; `agents.planning` / `agents.implementation` override only the fields they specify.

s03 ports both to ~250 lines of Go. No reflect, no magic — just struct tags, a regex, and a hand-written field-by-field overlay.

## Run it

```bash
cd agents/s03-config-loader

# Print resolved settings for the planning phase
OPENAI_API_KEY=sk-test go run . -phase planning testdata/deepcode_config.json

# Default phase (returns agents.defaults)
OPENAI_API_KEY=sk-test go run . testdata/deepcode_config.json

# Implementation phase
OPENAI_API_KEY=sk-test go run . -phase implementation testdata/deepcode_config.json
```

Stdout is the resolved `AgentSettings` JSON; stderr shows a one-line summary. Exit codes: `0` ok, `1` parse error, `2` usage, `3` missing env var.

## Test it

```bash
go test -v ./...
```

5 PASS, well under 1s. No network, no temp dirs outside `t.TempDir()`.

## File map

- [`config.go`](config.go) — struct tree (`Config`, `AgentsConfig`, `AgentPhase`, `ProviderConfig`, `ToolsConfig`, `WorkspaceConfig`, ...)
- [`load.go`](load.go) — `Load(ctx, path)` + `expandEnv` + `*MissingEnvError`
- [`resolve.go`](resolve.go) — `(c *Config) Resolve(phase string) AgentSettings`
- [`main.go`](main.go) — small CLI that prints resolved settings for a phase
- [`load_test.go`](load_test.go) — 5 hermetic tests
- [`testdata/deepcode_config.json`](testdata/deepcode_config.json) — trimmed copy of upstream's example
- [`testdata/with_env.json`](testdata/with_env.json) — minimal config for the env-substitution test

## What's deliberately absent

| Feature | Where it shows up |
|---|---|
| Pydantic-style validation (typed errors per field) | s04 — provider construction does field-level checks where it matters |
| `_match_provider` (model-name → provider auto-detect) | s04 — once we have real providers it's a 30-line decision tree |
| Walking up parent dirs for `deepcode_config.json` | Out of scope — we always require an explicit path |
| MCP server schema parsing | Out of scope — we keep `mcpServers` as `map[string]json.RawMessage` so a future chapter can deserialize it |
| `documentSegmentation` consumer logic | Configured here, used by upstream's L4 phase (not in our curriculum) |

## Upstream reference

- `core/config.py:1-250` — full `DeepCodeConfig` BaseSettings tree (`AgentDefaults`, `AgentPhase`, `ProviderConfig`, `ResolvedAgentSettings`, `_resolve_env_refs`, `load_config`).
- `deepcode_config.json.example:1-152` — the canonical layout we mirror.
- See [`docs/zh/s03-config-loader.md`](../../docs/zh/s03-config-loader.md) and [`docs/en/s03-config-loader.md`](../../docs/en/s03-config-loader.md) for the lesson.
- Annotated upstream: [`upstream-readings/s03-config.py`](../../upstream-readings/s03-config.py).
