# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  File: workflows/agents/memory_agent_concise.py  (selected, abridged ~120 lines)
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
# =============================================================================
"""
Concise Memory Agent for Code Implementation Workflow

This memory agent implements a focused approach:
1. Before first file: Normal conversation flow
2. After first file: Keep only system_prompt + initial_plan + current round tool results
3. Clean slate for each new code file generation
"""

# >>> s09: Python uses tiktoken when available and falls back to len(s)
#     when the import fails — that's research-notes anti-pattern #9
#     (implicit budgeting). Our Go port makes the cost model explicit:
#     a `Tokenizer` interface (tokens.go) with a `ByteLengthTokenizer`
#     default. Swap in a real BPE later without touching the agent.
import time
from typing import Dict, Any, List, Optional


# >>> s09: Maps to Go's `MemoryAgent` struct in agent.go. Same constants
#     for max_context_tokens (200000) and token_buffer (10000). What we
#     drop in the Go port: phase parsing, file extraction from disk,
#     LLM-driven summarisation. Those are workflow-level concerns; the
#     agent struct is config-only.
class ConciseMemoryAgent:

    def __init__(
        self,
        initial_plan_content: str,
        logger=None,
        target_directory=None,
        default_models=None,
        code_directory=None,
    ):
        self.initial_plan = initial_plan_content

        # >>> s09: identical defaults survive in MemoryAgent.defaults().
        self.max_context_tokens = 200000
        self.token_buffer = 10000
        self.summary_trigger_tokens = self.max_context_tokens - self.token_buffer

        # Memory state tracking - new logic: trigger after each write_file
        self.last_write_file_detected = False
        self.should_clear_memory_next = False
        self.current_round = 0

        # >>> s09: dropped — the Go agent struct is stateless. The
        #     "round counter", "last_write_file_detected" boolean, and
        #     "should_clear_memory_next" flag are all replaced by a
        #     single pure function that scans the message slice for the
        #     last write_file boundary on every call.

    def record_tool_result(self, tool_name, tool_input, tool_result):
        """Record tool result for current round and detect write_file calls"""

        # >>> s09: write_file is the boundary marker. Same in Go —
        #     findLastWriteFileBoundary() scans for ToolName == "write_file".
        if tool_name == "write_file":
            self.last_write_file_detected = True
            self.should_clear_memory_next = True

        # >>> s09: THIS LIST IS THE SOURCE OF TRUTH for essential.go.
        #     The Go map[string]bool in essential.go contains exactly
        #     these eight names verbatim.
        essential_tools = [
            "read_code_mem",          # Read code summary from implement_code_summary.md
            "read_file",              # Read file contents
            "write_file",             # Write file contents (also the boundary marker)
            "execute_python",         # Execute Python code (for testing/validation)
            "execute_bash",           # Execute bash commands (for build/execution)
            "search_code",            # Search code patterns
            "search_reference_code",  # Search reference code (if available)
            "get_file_structure",     # Get file structure (for understanding project layout)
        ]

        if tool_name in essential_tools:
            self.current_round_tool_results.append({
                "tool_name": tool_name,
                "tool_input": tool_input,
                "tool_result": tool_result,
                "timestamp": time.time(),
            })

    def should_use_concise_mode(self) -> bool:
        # >>> s09: Go has no equivalent — concise mode is implicit. If
        #     ShouldCompact returns true the caller invokes Compact;
        #     otherwise the caller leaves the message slice alone.
        return self.last_write_file_detected

    def create_concise_messages(self, system_prompt, messages, files_implemented):
        """Create concise message list for LLM input
        NEW LOGIC: Always clear after write_file, keep system_prompt +
        initial_plan + current round tools
        """
        if not self.last_write_file_detected:
            # >>> s09: skipped in Go — Compact() is unconditional.
            #     The caller decides via ShouldCompact() whether to call
            #     it at all, but Compact itself doesn't gate on history.
            return messages

        concise_messages = []

        # >>> s09: identical pattern in Go's Compact().
        #     1. Re-emit initial plan as a synthetic user message.
        #     2. Append [system, plan, ...tail-from-write_file].
        initial_plan_message = {
            "role": "user",
            "content": f"""**Task: Implement code based on the following reproduction plan**

**Code Reproduction Plan:**
{self.initial_plan}

**Working Directory:** Current workspace

**Current Status:** {files_implemented} files implemented""",
        }
        concise_messages.append(initial_plan_message)

        # >>> s09: upstream also appends a "knowledge base" message
        #     summarising the latest implemented file. We omit it in
        #     Go — synthesising a summary requires another LLM call,
        #     and Compact must remain a pure function.

        # >>> s09: Go re-uses the existing tool_result blocks from the
        #     kept window directly; upstream re-formats them into one
        #     blob of text. Both encode the same information; the Go
        #     approach preserves provider-native shapes.
        return concise_messages
