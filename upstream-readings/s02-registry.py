# =============================================================================
#  Annotated extract from HKUDS/DeepCode @ b9ece6035ea3f3582e6c503c517206b23c09ad09
#  File: core/agent_runtime/tools/registry.py  (L1-L130, abridged)
#  License: MIT (Copyright 2025 Data Intelligence Lab @ HKU)
# =============================================================================
"""Tool registry for dynamic tool management (ported from nanobot)."""

# >>> s02: Python uses AsyncExitStack for MCP-server lifetime.
#     In Go we use a slice of io.Closer + Registry.Close() — the standard-library
#     io.Closer is the natural analogue.
import asyncio
import os
from contextlib import AsyncExitStack
from typing import Any

from core.agent_runtime.tools.base import Tool


# =============================================================================
# >>> s02: ToolRegistry — our Go counterpart is `Registry` in registry.go.
#     Same fields, different ergonomics:
#       - Python uses dict + None-sentinel for cache invalidation.
#         Go uses map + nil-slice — same idea.
#       - Python uses AsyncExitStack (one stack drains all owned subprocesses).
#         Go uses []io.Closer + errors.Join in Registry.Close().
#       - Python is async-only (aclose, await execute). Go is sync + ctx.
# =============================================================================
class ToolRegistry:
    """Registry for agent tools with lazy MCP-server lifecycle ownership."""

    def __init__(self):
        self._tools: dict[str, Tool] = {}
        self._cached_definitions: list[dict[str, Any]] | None = None
        self._exit_stack: AsyncExitStack = AsyncExitStack()
        self._owned_server_stacks: dict[str, AsyncExitStack] = {}

    # >>> s02: register/unregister — straightforward.
    #     Cache invalidation: setting _cached_definitions to None.
    #     Our Go code does the same: r.cached = nil.
    def register(self, tool: Tool) -> None:
        self._tools[tool.name] = tool
        self._cached_definitions = None

    def unregister(self, name: str) -> None:
        self._tools.pop(name, None)
        self._cached_definitions = None

    def get(self, name: str) -> Tool | None:
        return self._tools.get(name)

    # =========================================================================
    # >>> s02: get_definitions — the cached schema accessor.
    #     KEY POINT: builtins come first (sorted), then mcp_ tools (sorted).
    #     Our Go List() preserves this ordering EXACTLY.
    # =========================================================================
    def get_definitions(self) -> list[dict[str, Any]]:
        if self._cached_definitions is not None:
            return self._cached_definitions

        definitions = [tool.to_schema() for tool in self._tools.values()]
        builtins: list[dict[str, Any]] = []
        mcp_tools: list[dict[str, Any]] = []
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

    # =========================================================================
    # >>> s02: prepare_call + execute — these belong to the runner layer.
    #     We KEEP them out of s02 — they reappear in s06 (tool-capable-runner).
    #     Python conflates "registry" and "dispatcher"; we factor them apart
    #     in Go so s02 stays focused on the catalog responsibility.
    # =========================================================================
    def prepare_call(self, name: str, params: dict[str, Any]) -> tuple[Tool | None, dict[str, Any], str | None]:
        # ... (validation, error-message construction) — see upstream
        # for full. Our Go split puts this in s06's dispatch.go.
        ...

    async def execute(self, name: str, params: dict[str, Any]) -> Any:
        # ... (calls tool.execute with a "[Analyze and try again]" hint)
        # — also belongs to s06 in our split.
        ...

    # =========================================================================
    # >>> s02: attach_server_stack / aclose — MCP subprocess lifetime.
    #     Our Go counterpart uses a simpler model: registered CloserTool's
    #     are tracked; Close() walks them with errors.Join.
    #     Real MCP stdio framing (os/exec + JSON-RPC) is left as Appendix B
    #     exercise #5 to keep s02 small.
    # =========================================================================
    def attach_server_stack(self, server_name: str, stack: AsyncExitStack) -> None:
        """Track a per-MCP-server AsyncExitStack so aclose() can drain it."""
        self._owned_server_stacks[server_name] = stack


# =============================================================================
# Read further:
#   1. core/agent_runtime/tools/base.py — Tool ABC (cast_params, validate_params,
#      execute). We don't port the validation; in Go we let json.Unmarshal +
#      explicit checks do that.
#   2. core/agent_runtime/runner.py     — calls registry.execute() in the loop.
#      That dispatch site moves to s06.
#   3. tools/code_implementation_server.py — a real MCP stdio server. Exercise:
#      port a minimal version using os/exec + a JSON-RPC framing layer.
# =============================================================================
