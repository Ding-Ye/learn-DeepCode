# learn-DeepCode

> 用 Go 渐进式重写 [HKUDS/DeepCode](https://github.com/HKUDS/DeepCode) 的核心机制 —— 每章一个机制，从最小回路到端到端集成。
>
> *(English: Build a tiny re-implementation of HKUDS/DeepCode in Go, one mechanism per chapter.)*
>
> 📘 English version: [README.en.md](README.en.md)

## 这是什么 / What is this

[HKUDS/DeepCode](https://github.com/HKUDS/DeepCode) 是一个 5.7 万行 Python 的多智能体编码系统（Paper2Code / Text2Web / Text2Backend），用 Anthropic Claude / OpenAI / Gemini 等 LLM 把研究论文或自然语言需求自动翻译成可运行的代码工程。它内含：

- **Provider 抽象** —— 多模型切换（Anthropic 原生 + OpenAI 兼容 + Gemini）
- **Tool registry + agent runner** —— 工具调用循环、MCP stdio 子进程生命周期管理
- **Workflow context** —— 不可变任务上下文，11 个 phase 共享
- **Code implementation workflow** —— 文件级迭代生成，含 loop detection / memory compaction / planning checkpoint
- **Plugin system** —— BEFORE/AFTER 各 phase 的人工介入点

`learn-DeepCode` 把这套架构拆成 **10 章 Go 代码 + 1 章端到端集成 + 2 个附录**，每章独立 `go.mod`、配独立单元测试和双语文档（中英对照）。读者按章节顺序学，到第 10 章就能跑通一个迷你版的"YAML plan → Go 文件序列"工作流。

## 课程 / Curriculum

| # | Slug | 章节标题（zh） | Title (en) | 状态 |
|---|---|---|---|---|
| s01 | minimum-loop | 最小智能体回路 | Minimum agent loop | ✅ |
| s02 | tool-registry | 工具注册表 | Tool registry | ✅ |
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

## 快速开始 / Quickstart

```bash
# 准备
export ANTHROPIC_API_KEY=sk-ant-...

# 跑 s01：最小回路（一个 Anthropic Messages API 调用）
cd agents/s01-minimum-loop
go run . "用一句话解释什么是 multi-agent orchestration"
```

预期输出：Claude 返回的一段中文解释，外加一行 stderr 标明 `provider`、`model`、用的字节数。

不想付钱也想看代码跑起来？所有测试都用本地 `httptest.Server` 重放预录的 JSON fixture：

```bash
go test ./...
```

## 设计原则 / Design principles

- **每章一节，互不依赖**：每个 `agents/sNN-*/` 都有自己的 `go.mod`。读 s05 不需要先看 s04。
- **`context.Context` 贯穿全文**：替代上游的 `async/await` —— Go 的惯用模型。
- **零全局状态**：所有依赖通过构造器传入。上游 `get_runtime()` 风格的 module-level singleton 是被显式列入 anti-pattern 的。
- **类型先于代码**：每章先写 struct/interface，再写实现 —— 这是和 Python 上游最大的风格差异。
- **`testdata/` 全是真实 fixture**：从 Anthropic / OpenAI 实际 API 抓的 JSON，离线即可重放。

## 阅读顺序建议 / Suggested reading order

1. **不熟 agent loop** → 顺序读 s01 → s02 → s06 → s_full（一周）
2. **想看 LLM 抽象** → s01 → s04 → 附录 B 第 1 段（半天）
3. **关心 robustness** → s07 → s08 → s09 → s10（两周）
4. **想完整学一遍** → s01..s10 + s_full + 附录 A + 附录 B（三周）

## 致谢 / Acknowledgments

- 上游：[HKUDS/DeepCode](https://github.com/HKUDS/DeepCode) (MIT, © 2025 Data Intelligence Lab @ HKU)
- 教学法启发：[shareAI-lab/learn-claude-code](https://github.com/shareAI-lab/learn-claude-code)
- 自动化骨架：[learn-repo-generator](https://github.com/anthropics/claude-code) skill

## 许可 / License

MIT — 详见 [LICENSE](LICENSE)。本仓库不是 DeepCode 的 fork，源码独立实现；上游源码摘录置于 `upstream-readings/` 仅作教学评论。
