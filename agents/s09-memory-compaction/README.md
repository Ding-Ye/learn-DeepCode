# s09 — memory-compaction

> A pure function `Compact(messages) → messages'` that scans for the last `write_file` event, keeps everything from that boundary onward, drops the rest, and re-emits the system prompt + initial plan in front. Whitelist of essential tools matches upstream `memory_agent_concise.py:1586-1595` verbatim.

## What this is

Upstream's `workflows/agents/memory_agent_concise.py` is a 2,000-line stateful agent. About 80 of those lines are the actual compaction algorithm — the rest is filename extraction, phase parsing, and LLM-driven knowledge-base summarisation. s09 extracts the algorithm and ships it as one stateless function over a `[]Message` slice.

The mechanism in three sentences:

1. After every `write_file`, the agent loop calls `Compact(messages)`.
2. The result is `[system, synthetic-plan, ...messages-from-last-write_file-onward]`, with non-essential tool calls (e.g. `web_fetch`) filtered out by name.
3. Pairing is preserved: dropping a `tool_use` always drops its matching `tool_result` and vice versa, so the model never sees an orphan.

## Run it

```bash
cd agents/s09-memory-compaction

go run .
```

Output:

```
MemoryAgent.Compact demo
------------------------
input  messages:  49  est-tokens:    368
output messages:  17  est-tokens:    138  (kept system + plan + last write_file round)
compaction ratio: 37.5%
```

## Test it

```bash
go test -v ./...
```

5 PASS in <1s. All tests are pure-function — no I/O, no goroutines, no network.

| # | Test | Verifies |
|---|---|---|
| 1 | `TestCompact_50MessagesPreservesSystemAndPlan` | result[0].Role=="system", result[1] is the synthetic plan |
| 2 | `TestCompact_LastWriteFileBoundaryPreserved` | both halves of the last write_file pair survive |
| 3 | `TestCompact_NonEssentialToolsDropped` | a `web_fetch` tool_use/tool_result pair is removed |
| 4 | `TestCompact_ToolPairingInvariant` | every tool_use has a matching tool_result in the result |
| 5 | `TestTokenizer_MonotonicAndBounded` | byte tokenizer is monotonic and within ±20% of `len(s)/4` |

## Files

- `types.go` — minimal redeclaration of `Message` and `ContentBlock` (s09 does NOT import s06; each session is isolated per project rule)
- `tokens.go` — `Tokenizer` interface + `ByteLengthTokenizer{}` (returns `len(s)/4`, documented bias)
- `essential.go` — the eight-tool whitelist matching upstream verbatim
- `agent.go` — `MemoryAgent` struct + `Compact` + `ShouldCompact`
- `main.go` — CLI demo that loads `testdata/long_conversation.json`
- `agent_test.go` — five tests
- `testdata/long_conversation.json` — recorded ~50-message run with three `write_file` events and four `web_fetch` calls

## Why a byte-length tokenizer

There is no idiomatic Go port of `tiktoken`. The `Tokenizer` interface is one method (`CountTokens(s string) int`) and the default implementation returns `len(s) / 4` — the conventional rule of thumb for English text under cl100k / o200k_base. Bias: code (especially CJK) compresses worse than prose, so this proxy underestimates real token counts on dense source files. Tests assert monotonicity and a ±20% bound rather than exact counts, so a future swap-in BPE library is held to the same accuracy contract without breaking the suite.

## What s09 explicitly does NOT do

- No goroutines, no I/O. Compact is `func(MemoryAgent, []Message) []Message`.
- No LLM-driven summarisation. Synthesising a "summary of prior conversation" requires another model call; that turns Compact into an async I/O-bound operation. We keep it pure and let s10's workflow loop decide when summarisation is worth the round-trip.
- No phase parsing, no file extraction. The upstream agent walks the initial plan to enumerate files-to-implement; that responsibility lives in s10.
- No mutation of the input slice. Compact returns a fresh slice every call.

## Dependencies

None. The `go.mod` declares `module github.com/Ding-Ye/learn-DeepCode/agents/s09-memory-compaction` and lists no requires. s09 is a leaf in the dependency graph; s10 will redeclare the parts it needs.

## Upstream reading

Annotated extract: [`upstream-readings/s09-memory.py`](../../upstream-readings/s09-memory.py).
