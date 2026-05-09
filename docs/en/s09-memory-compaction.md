---
title: "s09 · Memory compaction (clean-slate)"
chapter: 09
slug: s09-memory-compaction
est_read_min: 13
---

# s09 · Memory compaction (clean-slate)

> A pure function `Compact(messages) → messages'`: scan back to the last `write_file`, truncate from there, prepend the system prompt and a freshly-synthesised `initial plan` user message, then drop any tool_use / tool_result pair whose name is not on the whitelist. **No I/O, no goroutines, no side effects.** In s10's per-file workflow loop it shows up as a single line: `messages = agent.Compact(messages)`.

---

## Problem

The s06 runner was happy with ten tool rounds. But s10's workflow runs **per-file iteration** — give it a ten-file plan and the runner does five-to-ten tool rounds on each file, fifty-to-a-hundred total. Every round feeds back a `read_file` / `search_code` / `execute_python` / `get_file_structure` result averaging ~1KB; half an hour in, the transcript is 80KB+, and once you add the LLM's own replies and three re-reads of the plan you're closing on the model's 200K-token context window.

The naive fix is "drop the first K messages" — but `tool_use` and `tool_result` blocks are **paired**, and a blind cut almost always orphans one half ("the tool replied 'package a\\nfunc Stub() {}' to me" without a record of what was asked). Worse, the plan lives at message index 1 — slice it off and the model no longer knows what it's doing.

Upstream's answer in `workflows/agents/memory_agent_concise.py` (a 2,000-line agent): after every `write_file`, **wipe the conversation**, but preserve three things:

1. **System prompt** — index 0, always there;
2. **Initial plan** — synthesised fresh as a user message;
3. **Current-round tool results** — kept from the last `write_file` onward, with a further filter that drops anything whose tool name is not on the whitelist (`read_file` / `write_file` / `execute_python` / `execute_bash` / `search_code` / `search_reference_code` / `get_file_structure` / `read_code_mem`).

**Critical invariant**: dropping a `tool_use` must drop its matching `tool_result` and vice versa — any orphan `ToolUseID` causes a 422 from the provider.

s09 lifts that algorithm out of the surrounding stateful agent and ships it as one ~150-line pure function.

## Solution

```ascii-anim frames=1
                input messages
                       │
                       ▼
                ┌──────────────────┐
                │ Compact(msgs)    │
                └────────┬─────────┘
                         │
   ┌─────────────────────┼─────────────────────┐
   │                     │                     │
   ▼                     ▼                     ▼
keep msgs[0]      synthesise plan       findLastWriteFileBoundary
if Role=="system"  as user message       (scan from end for write_file,
                  with InitialPlan        return paired tool_use's idx)
   │                     │                     │
   └──────► out ◄────────┴──────► out          │
                                               │
                                ┌──────────────┘
                                │
                                ▼
                  for i := boundary..end:
                      filterMessage(msgs[i])
                      // drop tool_use / tool_result
                      // whose ToolName isn't whitelisted
                      // (preserve pairing)
                  append filtered to out
                                │
                                ▼
                            return out
```

Four design decisions worth calling out:

1. **Pure function vs. upstream's stateful agent** — `ConciseMemoryAgent` carries `last_write_file_detected` / `should_clear_memory_next` / `current_round` flags; s10's workflow has to call `record_tool_result` first and pass `files_implemented` into the compaction call. s09 is just `Compact(messages) → messages'`: state-leak bugs are unrepresentable, tests need zero setup. The cost is that Compact rescans for the `write_file` boundary on every call (one O(n) pass); the win is lock-free, stateless, safe for any caller.
2. **Resynthesise the plan, don't preserve the original** — upstream also builds a fresh user message rather than searching the transcript for the original plan turn. The reason: by the time we compact, that original turn could have been compressed, interleaved with tool calls, or replaced by a test fixture. The contract is "the result contains the plan" — independent of what the input contained.
3. **`Tokenizer` interface, not a hardcoded `len(s)/4`** — research-notes anti-pattern #9 calls out upstream's "fall back to `len(s)` if tiktoken is unavailable" as implicit budgeting. Go has no idiomatic tiktoken port, so we make the choice explicit: a `Tokenizer` interface plus a default `ByteLengthTokenizer{}` that returns `len(s)/4` with the bias documented inline. A future BPE library swaps in by replacing the interface implementation; the agent is untouched. Tests assert **monotonicity** + **±20% bound**, never an exact number, because there is no exact number to assert.
4. **Session isolation: do not import s06** — s06 already declares `Message` / `ContentBlock`, but s09's `types.go` redeclares its own. Per-session `go.mod`, independent run, independent test — that's the project rule. Field names match, `IsError` / `ToolName` / `ToolUseID` line up, an s06 Message copies over unchanged.

## How It Works

### 1. `types.go` — minimal redeclared shape

About 25 lines: `Message` + `ContentBlock`. `ContentBlock.Type` selects which fields are read:

```go
type ContentBlock struct {
    Type      string // "text" | "tool_use" | "tool_result"
    Text      string
    ToolUseID string
    ToolName  string  // tool_result also carries this — denormalised
    Input     string
    Output    string
    IsError   bool
}
```

Carrying `ToolName` on `tool_result` blocks is the **one redundancy** s09 introduces vs. s06 — s06 derives the name from the paired `tool_use`. s09 wants O(1) drop decisions inside Compact, so we store the name on both sides. Trade-off: a little duplicated data for one linear scan instead of two.

### 2. `tokens.go` — Tokenizer interface

```go
type Tokenizer interface {
    CountTokens(s string) int
}

type ByteLengthTokenizer struct{}

func (ByteLengthTokenizer) CountTokens(s string) int {
    return len(s) / 4
}

func MessagesTokens(t Tokenizer, msgs []Message) int {
    if t == nil { t = ByteLengthTokenizer{} }
    total := 0
    for _, m := range msgs {
        for _, b := range m.Content {
            total += t.CountTokens(b.Text)
            total += t.CountTokens(b.Input)
            total += t.CountTokens(b.Output)
        }
    }
    return total
}
```

`MessagesTokens` deliberately ignores Role / Type framing overhead — what matters is the **payload size**, which is what dominates real context use.

### 3. `essential.go` — the upstream whitelist, verbatim

```go
var EssentialTools = map[string]bool{
    "read_file":             true, // upstream L1588
    "write_file":            true, // upstream L1589 (also boundary marker)
    "execute_python":        true, // upstream L1590
    "execute_bash":          true, // upstream L1591
    "search_code":           true, // upstream L1592
    "search_reference_code": true, // upstream L1593
    "get_file_structure":    true, // upstream L1594
    "read_code_mem":         true, // upstream L1587
}
```

Each entry's comment cites the upstream line — adding or removing a name is a behavioural change worth noting in the chapter docs.

### 4. `agent.go` — the algorithm

```go
type MemoryAgent struct {
    InitialPlan      string
    EssentialTools   map[string]bool
    Tokenizer        Tokenizer
    MaxContextTokens int  // default 200000
    TokenBuffer      int  // default 10000
}

func (a *MemoryAgent) Compact(messages []Message) []Message {
    a.defaults()
    out := make([]Message, 0, len(messages)+2)

    // 1. preserve the system prompt
    if len(messages) > 0 && messages[0].Role == "system" {
        out = append(out, messages[0])
    }

    // 2. always synthesise a plan turn
    out = append(out, Message{
        Role: "user",
        Content: []ContentBlock{{Type: "text", Text: a.InitialPlan}},
    })

    // 3. find the last write_file boundary (returns the EARLIER half =
    //    the tool_use index, not the tool_result)
    boundary := findLastWriteFileBoundary(messages)
    if boundary < 0 {
        return out
    }

    // 4. filter messages from the boundary onward
    dropped := map[string]bool{}
    for i := boundary; i < len(messages); i++ {
        filtered := a.filterMessage(messages[i], dropped)
        if len(filtered.Content) == 0 {
            continue
        }
        out = append(out, filtered)
    }
    return out
}
```

`findLastWriteFileBoundary` is the easiest place to write a wrong loop: scanning from the end, **the first hit on a `write_file` block is not necessarily the message you want to keep**. The transcript is `tool_use → tool_result`; reverse-scanning hits the `tool_result` first (assistant already wrote the file, user already returned the result). Returning that index slices off the preceding `tool_use`, breaks pairing, and the next provider call 422s. The fix: when we hit a `tool_result`, walk one more step back to find the matching `tool_use` by `ToolUseID` and return *that* earlier index.

`filterMessage` keeps a running `dropped` map of `ToolUseID`s we already dropped on the `tool_use` side. When we later see the matching `tool_result`, drop it too — pairing never escapes.

### 4 non-obvious points

1. **`boundary` is the index of the `tool_use` half, not the position of the latest `write_file` block**. Reverse-scan must walk back further when it lands on a `tool_result`; otherwise you slice off the `tool_use` half and the provider rejects the next request.
2. **Drop messages whose Content emptied out**. After filtering, a `{Role:"user", Content:[]}` is invalid for most providers (they 422). Compact's `if len(filtered.Content) == 0 { continue }` purges them.
3. **`Compact` does not consult `ShouldCompact`** — the latter is the caller's gate for whether to invoke Compact at all; once inside Compact, we always compact. This avoids any "compact internally checks whether to compact" loop. s10's workflow uses the canonical sequence: `if agent.ShouldCompact(msgs) { msgs = agent.Compact(msgs) }`.
4. **Standalone `tool_result` blocks (no paired `tool_use`) are also filtered against the whitelist**. If someone constructs a transcript with an unpaired `tool_result` whose `ToolName` is non-essential, we drop it. This is consistency: **no whitelisted tool's result enters the output**, paired or not.

### 5. `main.go` — the demo

Reads `testdata/long_conversation.json` (49 messages, three `write_file` events, four `web_fetch` calls), runs Compact once, prints before/after:

```
input  messages:  49  est-tokens:    368
output messages:  17  est-tokens:    138  (kept system + plan + last write_file round)
compaction ratio: 37.5%
```

## What Changed (vs. s08)

```diff
+ types.go        redeclares Message + ContentBlock (s09 does NOT import s06;
+                 ContentBlock denormalises ToolName onto tool_result blocks,
+                 trading minor data duplication for a single-pass filter)
+ tokens.go       Tokenizer interface + ByteLengthTokenizer + MessagesTokens
+ essential.go    EssentialTools whitelist (8 names, one comment per name
+                 citing the upstream line they're copied from)
+ agent.go        MemoryAgent + Compact (pure function) + ShouldCompact +
+                 boundary/pairing scan
+ main.go         loads fixture, runs Compact, prints before/after
+ agent_test.go   5 tests (system+plan kept / boundary / non-essential dropped /
+                 pairing invariant / tokenizer monotonic+bounded)
+ testdata/long_conversation.json  49 messages with 3 write_file + 4 web_fetch
+ Introduces the "pure function over a message slice" pattern — s10 reuses
+ the same shape inside its per-file loop
- No more "stateful per-file agent" — upstream's round counter / write_file
  flag collapse into a single linear scan
```

s08 is a **pure-logic safety net** (loop / timeout / stall); s09 is a **pure-logic data transform** (`messages → messages'`). The two chapters are orthogonal: s08 decides "should we keep going?", s09 decides "what do we send next?". s10 wraps both in a single per-file body.

## Try It

```bash
cd agents/s09-memory-compaction

# Demo: load the 49-message fixture, run one Compact, no API key needed
go run .

# Tests (5 PASS, <1s)
go test -count=1 -v ./...
```

All 5 tests PASS:

| # | Test | Verifies |
|---|---|---|
| 1 | `TestCompact_50MessagesPreservesSystemAndPlan` | result[0].Role=="system" and result[1] is the synthetic plan |
| 2 | `TestCompact_LastWriteFileBoundaryPreserved` | both halves of the last write_file pair survive |
| 3 | `TestCompact_NonEssentialToolsDropped` | a `web_fetch` tool_use/tool_result pair is removed cleanly |
| 4 | `TestCompact_ToolPairingInvariant` | every tool_use in the result has a matching tool_result, vice versa |
| 5 | `TestTokenizer_MonotonicAndBounded` | `len(a)<=len(b)` ⇒ `count(a)<=count(b)`, all within ±20% of `len(s)/4` |

## Upstream Source Reading

```upstream:workflows/agents/memory_agent_concise.py#L1567-L1605
def record_tool_result(self, tool_name, tool_input, tool_result):
    # Detect write_file calls to trigger memory clearing
    if tool_name == "write_file":
        self.last_write_file_detected = True
        self.should_clear_memory_next = True

    # Only record specific tools that provide essential information
    essential_tools = [
        "read_code_mem",          # Read code summary from implement_code_summary.md
        "read_file",              # Read file contents
        "write_file",             # Write file contents (important for tracking implementations)
        "execute_python",         # Execute Python code (for testing/validation)
        "execute_bash",           # Execute bash commands (for build/execution)
        "search_code",            # Search code patterns
        "search_reference_code",  # Search reference code (if available)
        "get_file_structure",     # Get file structure (for understanding project layout)
    ]

    if tool_name in essential_tools:
        tool_record = {
            "tool_name": tool_name,
            "tool_input": tool_input,
            "tool_result": tool_result,
            "timestamp": time.time(),
        }
        self.current_round_tool_results.append(tool_record)
```

```upstream:workflows/agents/memory_agent_concise.py#L1616-L1700
def create_concise_messages(self, system_prompt, messages, files_implemented):
    if not self.last_write_file_detected:
        return messages

    concise_messages = []

    # 1. Add initial plan message (always preserved)
    initial_plan_message = {
        "role": "user",
        "content": f"""**Task: Implement code based on the following reproduction plan**

**Code Reproduction Plan:**
{self.initial_plan}
...""",
    }
    concise_messages.append(initial_plan_message)

    # 2. Add knowledge base + current tool results (omitted in s09)
    ...
    return concise_messages
```

**Reading notes**:

- **`last_write_file_detected` is a boolean flag in upstream**, maintained by ordered `record_tool_result` calls; s09 trades the flag for "rescan messages for the last write_file boundary on every Compact call". Compute vs. cache: upstream picked cache for speed and paid in state-leak bugs; we picked recompute for correctness and pay one O(n) pass (where n is always a few dozen).
- **The whitelist is a local variable in upstream**, not a class attribute — meaning every `record_tool_result` rebuilds the list. The Go port lifts it to a package-level `var EssentialTools`, both saving the rebuild and making "extend the whitelist" a single public modification site.
- **The "knowledge base" message** (a summary of the most-recently-implemented file) is not ported — it would require another LLM call inside Compact, which would break the pure-function contract. If s10 wants the same effect, it can run a separate summarisation step at the workflow layer and inject the result as a user message before Compact runs.
- **Token-budget placement** — upstream uses `summary_trigger_tokens = max_context_tokens - token_buffer` to decide when to switch to concise mode; s09 uses the same formula but only inside `ShouldCompact`. **Compact itself never consults the budget**, which keeps the function deterministic over its input.

**Keep reading**: `core/agent_runtime/runner.py:393-540` for upstream's `_microcompact` and `_snip_history` — that's a different layer of compaction (per-LLM-round, inside the runner). s09's clean-slate compaction and microcompact are complementary: coarse (per file) vs. fine (per round). Annotated extract: [`upstream-readings/s09-memory.py`](../../upstream-readings/s09-memory.py).

---

**Next**: s10 composes s06's Runner, s07's PlanningRuntime, s08's LoopDetector, and s09's MemoryAgent into the `CodeImplementationWorkflow` — read a plan, generate code file-by-file, call Compact after each `write_file`. The architectural climax of the book.
