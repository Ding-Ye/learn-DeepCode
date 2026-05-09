# learn-DeepCode

> A progressive Go re-implementation of [HKUDS/DeepCode](https://github.com/HKUDS/DeepCode)'s core mechanisms — one mechanism per chapter, from a minimum agent loop to end-to-end integration.
>
> 📘 中文版本: [README.md](README.md)

## What is this

[HKUDS/DeepCode](https://github.com/HKUDS/DeepCode) is a 57k-line Python multi-agent coding system (Paper2Code / Text2Web / Text2Backend) that uses LLMs (Anthropic Claude / OpenAI / Gemini) to translate research papers and natural-language requirements into running code. It contains:

- **Provider abstraction** — multi-model swap (native Anthropic + OpenAI-compatible + Gemini)
- **Tool registry + agent runner** — tool-call loop, MCP stdio subprocess lifecycle
- **Workflow context** — immutable per-task state shared across 11 phases
- **Code implementation workflow** — iterative file-by-file generation with loop detection / memory compaction / planning checkpoints
- **Plugin system** — BEFORE/AFTER human-in-loop hooks for each phase

`learn-DeepCode` decomposes that architecture into **10 Go chapters + 1 integration chapter + 2 appendices**. Every chapter has its own `go.mod`, unit tests, and bilingual docs. By chapter 10 you can run a tiny "YAML plan → Go file sequence" workflow end-to-end.

## Curriculum

| # | Slug | Title (zh) | Title (en) | Status |
|---|---|---|---|---|
| s01 | minimum-loop | 最小智能体回路 | Minimum agent loop | ✅ |
| s02 | tool-registry | 工具注册表 | Tool registry | ⏳ |
| s03 | config-loader | 单 JSON 配置 + 阶段覆盖 | Single-JSON config + phase overrides | ⏳ |
| s04 | provider-abstraction | LLM Provider 抽象 | LLM provider abstraction | ⏳ |
| s05 | workflow-context | 不可变工作流上下文 | Immutable workflow context | ⏳ |
| s06 | tool-capable-runner | 可调用工具的 Runner | Tool-capable agent runner | ⏳ |
| s07 | planning-runtime | 规划检查点 + JSONL 尝试日志 | Planning checkpoint + JSONL attempts | ⏳ |
| s08 | loop-detector | 循环探测器 + 停滞 vs LLM 偏移 | Loop detector + stall vs LLM offset | ⏳ |
| s09 | memory-compaction | 记忆压缩（清空式） | Memory compaction (clean-slate) | ⏳ |
| s10 | code-impl-workflow | 文件级代码实现工作流 | File-by-file code implementation workflow | ⏳ |
| s_full | integration | 端到端集成 | End-to-end integration | ⏳ |
| App. A | multi-agent-philosophy | 附录 A · 多智能体编排哲学 | Appendix A · Multi-agent orchestration philosophy | ⏳ |
| App. B | upstream-map | 附录 B · 上游源码导读地图 | Appendix B · Upstream source-reading map | ⏳ |

## Quickstart

```bash
# Prep
export ANTHROPIC_API_KEY=sk-ant-...

# Run s01: minimum loop (one Anthropic Messages API call)
cd agents/s01-minimum-loop
go run . "Explain multi-agent orchestration in one sentence"
```

Expected: a one-line answer from Claude, plus a stderr line with `provider`, `model`, byte counts.

No API key? All tests replay pre-recorded JSON fixtures via `httptest.Server`:

```bash
go test ./...
```

## Design principles

- **One mechanism per chapter, no cross-imports** — every `agents/sNN-*/` has its own `go.mod`. Reading s05 doesn't require reading s04.
- **`context.Context` everywhere** — replaces upstream's `async/await`. Go idiom.
- **Zero global state** — everything is dependency-injected. Upstream's `get_runtime()` module-level singleton is explicitly listed as an anti-pattern.
- **Types before code** — every chapter ships its struct/interface declarations before the implementation. This is the biggest stylistic difference from the Python upstream.
- **`testdata/` is all real fixtures** — JSON captured from real Anthropic / OpenAI APIs, replay-able offline.

## Suggested reading orders

1. **New to agent loops** → s01 → s02 → s06 → s_full (one week)
2. **Curious about LLM abstraction** → s01 → s04 → Appendix B §1 (half day)
3. **Interested in robustness** → s07 → s08 → s09 → s10 (two weeks)
4. **Full course** → s01..s10 + s_full + Appendix A + Appendix B (three weeks)

## Acknowledgments

- Upstream: [HKUDS/DeepCode](https://github.com/HKUDS/DeepCode) (MIT, © 2025 Data Intelligence Lab @ HKU)
- Pedagogy: [shareAI-lab/learn-claude-code](https://github.com/shareAI-lab/learn-claude-code)
- Automation skeleton: [learn-repo-generator](https://github.com/anthropics/claude-code) skill

## License

MIT — see [LICENSE](LICENSE). This repo is not a fork of DeepCode; the Go code is independently re-implemented. Upstream source quotations under `upstream-readings/` are reproduced under fair-use for educational commentary.
