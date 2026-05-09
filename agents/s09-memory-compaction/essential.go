// Package main — s09-memory-compaction.
//
// File: essential.go — the whitelist of MCP tools whose tool_use / tool_result
// pairs survive compaction.
//
// Source: upstream `workflows/agents/memory_agent_concise.py:1586-1595`
// (the local `essential_tools = [...]` list inside `record_tool_result`).
// We copy the names verbatim — adding or removing one here is a behavioural
// change worth noting in the chapter docs.
//
// Why a map[string]bool, not a slice: Compact() does a hot lookup per block;
// the map turns it into O(1). Tests assert membership directly (presence of
// key + value true).
package main

// EssentialTools is the upstream whitelist. Names match the MCP tool names
// surfaced by `tools/code_implementation_server.py`. Anything not in this map
// gets dropped during Compact() — that's the whole point of the whitelist.
var EssentialTools = map[string]bool{
	"read_file":             true, // upstream L1588 — read file contents
	"write_file":            true, // upstream L1589 — also the boundary marker
	"execute_python":        true, // upstream L1590 — testing/validation
	"execute_bash":          true, // upstream L1591 — build/exec
	"search_code":           true, // upstream L1592 — code search
	"search_reference_code": true, // upstream L1593 — reference repo search
	"get_file_structure":    true, // upstream L1594 — project layout
	"read_code_mem":         true, // upstream L1587 — implement_code_summary.md
}
