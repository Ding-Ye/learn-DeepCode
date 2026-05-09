---
title: "s06 · Tool-capable runner"
chapter: 06
slug: s06-tool-capable-runner
est_read_min: 16
---

# s06 · Tool-capable runner

> One `Provider`, one `Registry`, one `for` loop. The model speaks; the runner detects `tool_calls`; the registry dispatches; the result feeds back; repeat — until the model gives a final text answer or `MaxIterations` runs out. This is the heartbeat that fuses s01, s02, and s04.

---

## Problem

s01 was a **one-shot**: one prompt → one response → print. Thirty seconds to write, but it solves no real task — because being an agent means going **back-and-forth**:

- Model says: "I want to call `read_file('/etc/hosts')`"
- Us: execute the tool, push the output back
- Model: now I've seen the content, "next call `grep`"
- Execute, push back…
- Eventually: model says "done", emits final text.

That's the loop in `core/agent_runtime/runner.py` — all 1,065 lines of it. But the bulk of that file is **not the loop itself**. It's the hard cases that surround it:

- What if the model returns blank text? (`_MAX_EMPTY_RETRIES`)
- What if `finish_reason="length"` truncates output? (`_MAX_LENGTH_RECOVERIES`)
- What if a tool result is huge? (`max_tool_result_chars` + truncation)
- What if tool-use IDs don't pair correctly? (`_drop_orphan_tool_results`)
- What if the tool itself raises? (wrap as `tool_result` with `is_error: true`)
- What if context overflows? (`_microcompact` + `_snip_history`)

s06 isolates the **loop skeleton** as one chapter. The skeleton has just three branches: tool_use → dispatch + continue; final text → return; cap hit → apology template. Everything else is "later chapters" or "upstream stretch goal" — the reader sees ~150 lines of Go and walks away with the agent's core rhythm in their head.

## Solution

```ascii-anim frames=1
        InitialMessages (copy)
                │
                ▼
        ┌────────────────────────────┐
        │  for i := 0..MaxIterations │
        └─────────────┬──────────────┘
                      │
                      ▼
        ┌────────────────────────────┐
        │ Provider.Chat(ctx, req)    │
        └─────────────┬──────────────┘
                      │
        ┌─────────────┴──────────────┐
        │                            │
   len(ToolCalls)>0?            else (final text)
        │                            │
        ▼                            ▼
  dispatchToolCall              append assistant
   (registry.Get + tool.Run     return RunResult{
    + truncate + is_error)         StopReason: StopDone
        │                          }
   append tool_use blocks
   append tool_result blocks
        │
        └──────► continue
                      │
        (loop exhausts)│
                      ▼
        return RunResult{
            StopReason:   StopMaxIterations
            FinalMessage: apology template
        }
```

Four key design decisions:

1. **Three-branch loop, not upstream's twenty-branch state machine** — the upstream `run()` interleaves empty-retry / length-recovery / injection-cycles / orphan-repair / micro-compact / hook-callbacks (7+ parallel control flows). Each is correct; each loses the first-time reader. s06 cuts to `if len(ToolCalls) > 0` / `else (final text)` / `for ... else (max_iterations)`. Once the skeleton is clear, the reader can layer on the rest.
2. **`tool_result.IsError` keeps the loop alive on tool failures** — tool errors are routine (path doesn't exist, HTTP 502, parse failed). Upstream wraps them as `tool_result` blocks with `is_error: true` so the model sees the failure and tries something else. s06 inherits this verbatim: `dispatchToolCall` packages a `tool.Run` error as `ContentBlock{Type:"tool_result", IsError:true, Output: errMsg}` and the loop continues. **Only `Provider.Chat` failures yield `StopError`** — those are infrastructure problems, not something the model can recover from.
3. **Truncation lives in the dispatcher, not the tool** — upstream's `_normalize_tool_result` clips strings at the runner layer. s06 does the same: `tool.Run` returns the full string; the runner consults `MaxToolBytes` to decide whether to chop. This means **tool code never has to know about context budgets** — the runner is the single place that owns "fits / doesn't fit".
4. **Session-isolation: s06 does not import s02 or s04** — per project rule, every chapter has its own `go.mod` and re-declares the minimal subset of `Provider` / `Tool` / `Message` / `ContentBlock` it needs. The shapes are byte-compatible (a Tool from s02 copy-pasted in compiles unchanged), but every chapter is grep-friendly, independently testable, and a reader who jumps straight here doesn't have to chase a dependency graph.

## How It Works

### 1. `types.go` — minimal redeclared contract

s06 imports nothing from sibling sessions. `Provider`, `Tool`, `Message`, `ContentBlock`, `ToolCallRequest`, `ChatRequest`, `ChatResponse`, `Usage` all live in this directory. Same shapes as s04:

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

type Tool interface {
    Name() string
    Schema() ToolSchema
    Run(ctx context.Context, args json.RawMessage) (string, error)
}

type ContentBlock struct {
    Type      string // "text" | "tool_use" | "tool_result"
    Text      string
    ToolUseID string
    ToolName  string
    Input     json.RawMessage
    Output    string
    IsError   bool   // only for tool_result
}
```

`IsError` is the **only** field s06 adds vs. s04 — s04 didn't need to distinguish "successful tool_result" from "failed tool_result" because that chapter never invoked tools. s06 needs the flag the moment the loop touches a registry.

### 2. `registry.go` — the trim version

s02's Registry has `sync.Mutex`, `Close()` lifecycle, builtins-vs-mcp ordering — those are registry concerns. s06 only needs `Get(name)` for dispatch and `List()` to gather schemas to feed the model:

```go
type Registry struct {
    tools map[string]Tool
}

func (r *Registry) Register(t Tool) { r.tools[t.Name()] = t }
func (r *Registry) Get(name string) (Tool, bool) { t, ok := r.tools[name]; return t, ok }
func (r *Registry) List() []ToolSchema { /* sort by name */ }
```

40 lines. Re-inventing the registry is intentional — keeps the chapter focused on the **loop**, not on registry plumbing. For the full story, the reader goes back to s02.

### 3. `spec.go` — RunSpec / RunResult / StopReason

```go
const (
    StopDone          = "done"           // model returned final text
    StopMaxIterations = "max_iterations" // hit the cap, apology emitted
    StopError         = "error"          // Provider.Chat failed; tool errors don't count
)

type RunSpec struct {
    InitialMessages []Message
    Tools           *Registry  // nil = no tools
    Model           string
    MaxIterations   int
    MaxToolBytes    int        // <=0 = no truncation
    MaxTokens       int
    Temperature     float64
}

type RunResult struct {
    FinalMessage Message     // last assistant turn the loop terminated on
    AllMessages  []Message   // full transcript
    StopReason   string      // one of Stop*
    Iterations   int         // how many provider calls were made
}
```

`StopReason` is the **control-flow hub** s10 will extend — s10 adds `loop_detected` (from s08) and `max_time`, but they all branch off these three primitives.

### 4. `dispatch.go` — every rule for one tool call

```go
const truncationMarker = "… [truncated]"

func dispatchToolCall(ctx context.Context, reg *Registry, call ToolCallRequest, maxBytes int) ContentBlock {
    if reg == nil { /* IsError=true, "no registry configured" */ }

    tool, ok := reg.Get(call.Name)
    if !ok { /* IsError=true, "tool X not found" */ }

    out, err := tool.Run(ctx, call.Args)
    if err != nil { /* IsError=true, "tool X failed: <err>" */ }

    return ContentBlock{
        Type:      "tool_result",
        ToolUseID: call.ID,
        Output:    truncate(out, maxBytes),
    }
}

func truncate(s string, maxBytes int) string {
    if maxBytes <= 0 || len(s) <= maxBytes { return s }
    keep := maxBytes - len(truncationMarker)
    return s[:keep] + truncationMarker
}
```

Why a separate file? Because **tool-failure handling** is the part of s06 that's easiest to underweight. Three failure modes (registry nil, tool not found, `tool.Run` error) all share one shape: wrap as `IsError=true` tool_result, **let the loop continue**. Inlining them in `runner.go` would entangle them with the main control flow; pulled out, dispatch.go is 70 lines and the contract is obvious.

### 5. `runner.go` — the loop itself

Strip the doc-comments and you have ~70 lines:

```go
func (r *Runner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
    messages := append([]Message(nil), spec.InitialMessages...)  // copy
    schemas := []ToolSchema(nil)
    if spec.Tools != nil { schemas = spec.Tools.List() }

    for i := 0; i < spec.MaxIterations; i++ {
        resp, err := r.Provider.Chat(ctx, ChatRequest{...})
        if err != nil {
            return RunResult{StopReason: StopError, ...}, err
        }

        // Branch 1: model wants tools
        if len(resp.ToolCalls) > 0 {
            assistant := Message{Role: "assistant", Content: resp.Content}
            assistant.Content = ensureToolUseBlocks(assistant.Content, resp.ToolCalls)
            messages = append(messages, assistant)

            results := make([]ContentBlock, 0, len(resp.ToolCalls))
            for _, call := range resp.ToolCalls {
                results = append(results, dispatchToolCall(ctx, spec.Tools, call, spec.MaxToolBytes))
            }
            messages = append(messages, Message{Role: "user", Content: results})
            continue
        }

        // Branch 2: final text
        final := Message{Role: "assistant", Content: resp.Content}
        messages = append(messages, final)
        return RunResult{FinalMessage: final, StopReason: StopDone, ...}, nil
    }

    // Branch 3: cap hit
    apology := Message{Role: "assistant", Content: []ContentBlock{{
        Type: "text",
        Text: fmt.Sprintf(defaultMaxIterationsMessage, spec.MaxIterations),
    }}}
    return RunResult{FinalMessage: apology, StopReason: StopMaxIterations, ...}, nil
}
```

`ensureToolUseBlocks` is a small fixup: Anthropic responses already inline `tool_use` blocks in `Content`; OpenAI responses put them in a sibling `ToolCalls` field but **not** `Content`. The transcript needs both merged so the assistant turn shows what the model "decided", otherwise the next iteration the model sees a turn that says nothing about tools and may re-issue the call.

### 6. `replay.go` — fake Provider for tests

```go
type ReplayProvider struct {
    Responses []ChatResponse
    calls     int
}

func (p *ReplayProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
    if p.calls >= len(p.Responses) {
        return ChatResponse{}, errors.New("ReplayProvider: queue empty")
    }
    r := p.Responses[p.calls]
    p.calls++
    return r, nil
}
```

30 lines to solve "I want to test multi-round but I don't want httptest". Each test queues N ChatResponses, the runner pops one per call, behaviour is deterministic. s10 will reuse this pattern for its three-file workflow integration test.

### Four non-obvious points

1. **Tool failure is not runner failure.** This is the contract upstream shares with us: a `tool.Run` error becomes a `tool_result` with `IsError=true` and the loop carries on so the model sees "my read_file got ENOENT" and retries differently. **Only `Provider.Chat` failure yields `StopError`** — that's network/auth/server, the model can't fix it. Readers often expect "any error should propagate"; s06 is the counter-example.
2. **Truncation is not "≤ N bytes", it's "exactly N bytes".** `truncate(s, 200)` always returns a 200-byte string (13 bytes marker, 187 bytes payload) — context budgets accumulate exactly. If we left it as "no more than 200" we'd waste a few bytes per call and drift over a 10-round session. The test `TestRun_Truncation` asserts `len(found.Output) == 200`, not `<=`.
3. **`MaxIterations=N` runs exactly N rounds.** Upstream uses `for iteration in range(spec.max_iterations)` — that's 0..N-1, N rounds, then `for/else` fires the apology. Go's `for i := 0; i < N; i++` matches; `TestRun_MaxIterations` sets `MaxIterations=1`, the provider is called **once**, then the loop ends and the apology is emitted. Readers sometimes assume "one extra round to wrap up" — there isn't; the apology *is* the wrap-up.
4. **The assistant message must include `tool_use` blocks or the transcript is corrupt.** Anthropic's content already carries them; OpenAI's may not. `ensureToolUseBlocks` is an idempotent 8-line patch — already-present blocks aren't duplicated, missing ones get appended from `ToolCalls`. Skip this on OpenAI and the next iteration the model sees a turn that doesn't reflect what it just decided, and will most likely issue the same tool call again.

## What Changed (vs. s05)

```diff
+ types.go        Re-declared Provider / Tool / Message / ContentBlock /
+                 ToolCallRequest / ChatRequest / ChatResponse / Usage
+                 (ContentBlock gains an IsError field — s04 didn't have it)
+ registry.go     Trim Registry (Register / Get / List only, no Close lifecycle)
+ spec.go         RunSpec + RunResult + three Stop* constants
+ dispatch.go     dispatchToolCall + truncate (IsError wrapping, "… [truncated]" marker)
+ runner.go       Runner.Run (three-branch loop + ensureToolUseBlocks fixup)
+ replay.go       ReplayProvider (queue-driven fake Provider for tests + demo)
+ main.go         3-round replay demo (ask echo → result → ask echo again → result → final text)
+ runner_test.go  5 tests (one-round / two-round / max_iterations / truncation / tool_error)
+ Introduces StopReason as a control-flow hub — s10 will extend it with
+ loop_detected and max_time
- No more "naked Provider call" — s01's one-shot Chat is now one iteration
  inside the runner
```

s05 is **immutable task state**; s06 is **mutable conversation state + control flow**. The two chapters are orthogonal: one tracks "who am I, where am I", the other tracks "who do I call next". s10 will reference both.

## Try It

```bash
cd agents/s06-tool-capable-runner

# Demo: 3-round replay, no network, no API key
go run .

# Tests (5 PASS, <1s)
go test -count=1 -v ./...
```

5 PASS:

| # | Test | Verifies |
|---|---|---|
| 1 | `TestRun_OneRound` | provider returns text immediately → `StopDone`, no tool dispatch |
| 2 | `TestRun_TwoRound` | tool_use → text; `tool.Run` invoked exactly once; final text matches |
| 3 | `TestRun_MaxIterations` | `MaxIterations=1` + repeated tool_use → `StopMaxIterations`, apology contains `(1)` |
| 4 | `TestRun_Truncation` | 5000-byte output + `MaxToolBytes=200` → marker present, length == 200 |
| 5 | `TestRun_ToolError` | `tool.Run` returns error → `tool_result.IsError=true`, loop continues |

## Upstream Source Reading

```upstream:core/agent_runtime/runner.py#L69-L110
@dataclass(slots=True)
class AgentRunSpec:
    """Configuration for a single agent execution."""

    initial_messages: list[dict[str, Any]]
    tools: ToolRegistry
    model: str
    max_iterations: int
    max_tool_result_chars: int
    temperature: float | None = None
    max_tokens: int | None = None
    reasoning_effort: str | None = None
    hook: AgentHook | None = None
    error_message: str | None = _DEFAULT_ERROR_MESSAGE
    max_iterations_message: str | None = None
    concurrent_tools: bool = False
    fail_on_tool_error: bool = False
    workspace: Path | None = None
    session_key: str | None = None
    ...
```

```upstream:core/agent_runtime/runner.py#L239-L320
async def run(self, spec: AgentRunSpec) -> AgentRunResult:
    hook = spec.hook or AgentHook()
    messages = list(spec.initial_messages)
    final_content: str | None = None
    ...

    for iteration in range(spec.max_iterations):
        # context governance: orphan repair, microcompact, snip ...
        response = await self._request_model(spec, messages_for_model, hook, context)

        if response.should_execute_tools:
            assistant_message = build_assistant_message(...)
            messages.append(assistant_message)
            results, new_events, fatal_error = await self._execute_tools(
                spec, response.tool_calls, external_lookup_counts,
            )
            for tool_call, result in zip(response.tool_calls, results):
                tool_message = {
                    "role": "tool",
                    "tool_call_id": tool_call.id,
                    "name": tool_call.name,
                    "content": self._normalize_tool_result(...),
                }
                messages.append(tool_message)
            if fatal_error is not None:
                ...
            continue
        # final-text branch ...
```

```upstream:core/agent_runtime/runner.py#L548-L575
        else:
            stop_reason = "max_iterations"
            template = spec.max_iterations_message or _DEFAULT_MAX_ITERATIONS_MESSAGE
            final_content = template.format(max_iterations=spec.max_iterations)
            self._append_final_message(messages, final_content)
            ...

        return AgentRunResult(
            final_content=final_content,
            messages=messages,
            tools_used=tools_used,
            usage=usage,
            stop_reason=stop_reason,
            error=error,
            tool_events=tool_events,
            had_injections=had_injections,
        )
```

**Reading notes**:

- **`AgentRunSpec` has ~20 fields, s06's `RunSpec` has 7** — what we removed: `hook` / `injection_callback` / `progress_callback` / `checkpoint_callback` / `retry_wait_callback` / `session_key` / `workspace`. None of these are loop responsibilities — they're orchestration concerns and belong in s10's workflow. Leaving them on the runner makes the "skeleton" look like an onion you can never reach the core of.
- **`should_execute_tools` is a property on `LLMResponse`** that combines `finish_reason == "tool_calls"` AND `len(tool_calls) > 0`. The Go port simplifies to `len(resp.ToolCalls) > 0`; if some provider returned tool_calls without a tool_calls finish reason, s06 would still execute — more permissive than upstream. Doesn't matter for our fixtures, but production code might want to add `&& resp.FinishReason == FinishToolCalls` for safety.
- **`_normalize_tool_result` also patches "if the tool returned an empty string, replace with 'OK'"** — we did not port that. An empty string can propagate; the model seeing "I called X and got nothing back" usually handles it correctly. This is upstream anti-pattern #5 ("magic-value defaults instead of explicit None").
- **`for/else` is Python syntax** — the else branch fires when the for loop completes without a break. Go has no equivalent; we fall through after the loop body to the apology. Logically identical, but easy to miss on first read; s06's README calls it out explicitly.

**Keep reading**: `core/agent_runtime/runner.py:393-540` covers empty-content retries and length-recovery — see exactly how complex the parts s06 deliberately skipped really are. Annotated extract: [`upstream-readings/s06-runner.py`](../../upstream-readings/s06-runner.py).

---

**Next chapter**: s07 turns back toward persistence — three on-disk artifacts (atomic JSON checkpoint, JSONL attempts log, result meta). The Runner from s06 will be wired together with s07's `PlanningRuntime`, s08's `LoopDetector`, and s09's `MemoryAgent` inside `CodeImplementationWorkflow` in s10.
