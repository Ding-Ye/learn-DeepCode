# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  File: core/agent_runtime/runner.py  (L1-L575, abridged)
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
# =============================================================================
"""Shared execution loop for tool-using agents."""

# >>> s06: This file is the upstream archetype the Go runner ports. The
#     ~1065-line original handles streaming, hooks, injection cycles,
#     length recovery, empty-content retries, micro-compaction, and
#     orphan tool_result repair. The Go port (~150 LOC) keeps only the
#     three branches that define the loop: tool-call → dispatch + continue;
#     final-text → return; iteration-cap → apology. Everything else is
#     either a different chapter (s08 loop detector, s09 memory) or an
#     "out of scope" pointer in s06's README.

from __future__ import annotations

import asyncio
import inspect
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

# >>> s06: Constants. The Go counterparts:
#     - _DEFAULT_MAX_ITERATIONS_MESSAGE → runner.go::defaultMaxIterationsMessage
#     - _DEFAULT_ERROR_MESSAGE → not ported (s06 returns the underlying error
#       directly via the `error` return value).
#     - _MAX_EMPTY_RETRIES / _MAX_LENGTH_RECOVERIES / _MAX_INJECTIONS_PER_TURN /
#       _MAX_INJECTION_CYCLES → all out of scope; documented in s06 README.
_DEFAULT_ERROR_MESSAGE = "Sorry, I encountered an error calling the AI model."
_DEFAULT_MAX_ITERATIONS_MESSAGE = (
    "I reached the maximum number of tool call iterations ({max_iterations}) "
    "without completing the task. You can try breaking the task into smaller steps."
)
_MAX_EMPTY_RETRIES = 2
_MAX_LENGTH_RECOVERIES = 3
_MAX_INJECTIONS_PER_TURN = 3
_MAX_INJECTION_CYCLES = 5
_SNIP_SAFETY_BUFFER = 1024
_MICROCOMPACT_KEEP_RECENT = 10
_MICROCOMPACT_MIN_CHARS = 500


# >>> s06: AgentRunSpec dataclass. Go counterpart: spec.go::RunSpec.
#     Only the first five fields are ported — they're the load-bearing ones:
#       initial_messages    → InitialMessages
#       tools               → Tools (s06's minimal Registry, not s02's)
#       model               → Model
#       max_iterations      → MaxIterations
#       max_tool_result_chars → MaxToolBytes
#     All the "callback / hook / session_key / progress_callback" bag are
#     orchestration concerns that belong in s10's workflow, not the loop.
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
    hook: AgentHook | None = None  # >>> s06: dropped — runner is hook-free.
    error_message: str | None = _DEFAULT_ERROR_MESSAGE
    max_iterations_message: str | None = None  # >>> s06: dropped (template inlined).
    concurrent_tools: bool = False  # >>> s06: dropped — sequential is fine.
    fail_on_tool_error: bool = False  # >>> s06: behaviour fixed: tool errors
    #     become tool_result blocks with IsError=true, loop continues.
    workspace: Path | None = None
    session_key: str | None = None
    context_window_tokens: int | None = None
    context_block_limit: int | None = None
    provider_retry_mode: str = "standard"
    progress_callback: Any | None = None
    retry_wait_callback: Any | None = None
    checkpoint_callback: Any | None = None
    injection_callback: Any | None = None
    llm_timeout_s: float | None = None


# >>> s06: AgentRunResult. Go counterpart: spec.go::RunResult.
#     Field map:
#       final_content (str)        → FinalMessage (Message — we keep the role+blocks)
#       messages (list[dict])      → AllMessages
#       stop_reason (str)          → StopReason (constants StopDone / StopMaxIterations / StopError)
#       tools_used / usage / tool_events / had_injections → all dropped.
#     Upstream's `final_content: str | None` is a single string; Go's Message
#     preserves block structure so a future caller can inspect e.g. tool_use
#     residue from the final turn.
@dataclass(slots=True)
class AgentRunResult:
    """Outcome of a shared agent execution."""

    final_content: str | None
    messages: list[dict[str, Any]]
    tools_used: list[str] = field(default_factory=list)
    usage: dict[str, int] = field(default_factory=dict)
    stop_reason: str = "completed"
    error: str | None = None
    tool_events: list[dict[str, str]] = field(default_factory=list)
    had_injections: bool = False


class AgentRunner:
    """Run a tool-capable LLM loop without product-layer concerns."""

    # >>> s06: Constructor. Go counterpart: NewRunner(p Provider) *Runner.
    #     Stateless beyond the provider — every Run call is independent.
    def __init__(self, provider: LLMProvider):
        self.provider = provider

    # >>> s06: The main loop. This is the heart of the chapter — six pages of
    #     Python compress into ~70 lines of Go. The Go counterpart
    #     (runner.go::Runner.Run) keeps only the three branches: tool-call,
    #     final-text, iteration-cap. Everything below marked "dropped" is
    #     either out-of-scope or implemented elsewhere.
    async def run(self, spec: AgentRunSpec) -> AgentRunResult:
        hook = spec.hook or AgentHook()  # >>> s06: dropped (no hooks).
        messages = list(spec.initial_messages)  # >>> s06: same idea — copy
        #     so caller's slice is never mutated.
        final_content: str | None = None
        tools_used: list[str] = []
        usage: dict[str, int] = {"prompt_tokens": 0, "completion_tokens": 0}
        error: str | None = None
        stop_reason = "completed"
        tool_events: list[dict[str, str]] = []
        external_lookup_counts: dict[str, int] = {}
        empty_content_retries = 0
        length_recovery_count = 0
        had_injections = False
        injection_cycles = 0

        for iteration in range(spec.max_iterations):
            try:
                # >>> s06: Context governance — orphan repair, micro-compact,
                #     tool-result budgeting, history snipping. All dropped in
                #     the Go port; s09 (memory compaction) handles the long-
                #     conversation case at a higher level.
                messages_for_model = self._drop_orphan_tool_results(messages)
                messages_for_model = self._backfill_missing_tool_results(messages_for_model)
                messages_for_model = self._microcompact(messages_for_model)
                messages_for_model = self._apply_tool_result_budget(spec, messages_for_model)
                messages_for_model = self._snip_history(spec, messages_for_model)
                messages_for_model = self._drop_orphan_tool_results(messages_for_model)
                messages_for_model = self._backfill_missing_tool_results(messages_for_model)
            except Exception:
                # >>> s06: minimal-repair fallback also dropped.
                messages_for_model = messages

            # >>> s06: One LLM call per iteration. Go counterpart:
            #     resp, err := r.Provider.Chat(ctx, ChatRequest{...}).
            response = await self._request_model(spec, messages_for_model, hook, context)

            # >>> s06: BRANCH 1 — tool calls. The Go runner does the same:
            #     append the assistant turn (with tool_use blocks), dispatch
            #     each call, append a single user message with one
            #     tool_result block per call, continue. Note upstream's
            #     should_execute_tools includes the finish_reason check;
            #     the Go port simplifies to `len(resp.ToolCalls) > 0`.
            if response.should_execute_tools:
                assistant_message = build_assistant_message(
                    response.content or "",
                    tool_calls=[tc.to_openai_tool_call() for tc in response.tool_calls],
                )
                messages.append(assistant_message)
                tools_used.extend(tc.name for tc in response.tool_calls)

                # >>> s06: tool dispatch. Go counterpart: dispatch.go::dispatchToolCall.
                #     Upstream emits one Python dict per tool result; we emit
                #     one ContentBlock with Type="tool_result". Truncation
                #     and is_error wrapping live in the same helper.
                results, new_events, fatal_error = await self._execute_tools(
                    spec, response.tool_calls, external_lookup_counts,
                )

                for tool_call, result in zip(response.tool_calls, results):
                    tool_message = {
                        "role": "tool",
                        "tool_call_id": tool_call.id,
                        "name": tool_call.name,
                        "content": self._normalize_tool_result(spec, tool_call.id, tool_call.name, result),
                    }
                    messages.append(tool_message)
                # >>> s06: Anthropic flavor differs — tool_result blocks live
                #     on a *user* message in Anthropic's API, not a "tool"-role
                #     message. The Go port targets the Anthropic shape; OpenAI
                #     translation is s04's responsibility.

                if fatal_error is not None:
                    # >>> s06: dropped — tool errors become tool_result with
                    #     IsError=true; the loop continues. Aborting on tool
                    #     error is a workflow-level decision (s10's
                    #     fail_on_tool_error policy), not a runner-level one.
                    error = f"Error: {type(fatal_error).__name__}: {fatal_error}"
                    final_content = error
                    stop_reason = "tool_error"
                    break

                continue  # next iteration

            # >>> s06: BRANCH 2 — final text. The Go runner returns here.
            #     Empty-content / length-recovery / finalization-retry are
            #     all out of scope; we trust whatever's in resp.Content.
            clean = hook.finalize_content(context, response.content)
            if response.finish_reason != "error" and is_blank_text(clean):
                empty_content_retries += 1
                if empty_content_retries < _MAX_EMPTY_RETRIES:
                    continue  # >>> s06: dropped.

            if response.finish_reason == "length" and not is_blank_text(clean):
                length_recovery_count += 1
                if length_recovery_count <= _MAX_LENGTH_RECOVERIES:
                    messages.append(build_length_recovery_message())
                    continue  # >>> s06: dropped — model returns FinishLength,
                    #     runner treats it as final text. Stretch goal.

            messages.append(build_assistant_message(clean))
            final_content = clean
            stop_reason = "completed"
            break  # >>> s06: same — we return RunResult{StopDone}.
        else:
            # >>> s06: BRANCH 3 — for/else fires when the for loop ran to
            #     completion without break. The Go port handles this after
            #     the loop body falls through. Identical template:
            #     "I reached the maximum number of tool call iterations
            #     ({max_iterations}) without completing the task..."
            stop_reason = "max_iterations"
            template = spec.max_iterations_message or _DEFAULT_MAX_ITERATIONS_MESSAGE
            final_content = template.format(max_iterations=spec.max_iterations)
            self._append_final_message(messages, final_content)

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

    # >>> s06: _build_request_kwargs is the wire-format adapter — it picks
    #     which fields get sent based on what's set. Go's strongly-typed
    #     ChatRequest has all fields always present (zero-values omitted by
    #     the provider impls in s04), so this helper has no Go counterpart.
    def _build_request_kwargs(self, spec, messages, *, tools):
        kwargs = {
            "messages": messages,
            "tools": tools,
            "model": spec.model,
            "retry_mode": spec.provider_retry_mode,
            "on_retry_wait": spec.retry_wait_callback,
        }
        if spec.temperature is not None:
            kwargs["temperature"] = spec.temperature
        if spec.max_tokens is not None:
            kwargs["max_tokens"] = spec.max_tokens
        if spec.reasoning_effort is not None:
            kwargs["reasoning_effort"] = spec.reasoning_effort
        return kwargs


# >>> s06: Continue reading from line 400 onwards in the upstream file for
#     the full empty-content/length/finalization-retry branches and the
#     injection-cycle plumbing. None of it is in scope for s06; the loop
#     skeleton above is what every reader needs to grok before s10 wires
#     in the loop detector (s08) and memory compactor (s09).
