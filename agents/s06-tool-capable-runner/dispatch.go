// Package main — s06-tool-capable-runner.
//
// File: dispatch.go — single-tool-call dispatch with truncation and error
// wrapping. Pulled out of runner.go because the rules are non-trivial:
//
//  1. Look up the tool by name. Unknown name → tool_result with IsError=true
//     containing the lookup error. The loop continues — upstream does the
//     same: a missing tool is the model's bug, not a fatal runner error.
//
//  2. Run the tool. If it returns an error → tool_result with IsError=true
//     and the error string as Output. Same rationale: we want the model to
//     see "your call failed because X" so it can recover, not abort.
//
//  3. Truncate large output. If maxBytes > 0 and len(output) > maxBytes,
//     replace the tail with "… [truncated]" so the total fits maxBytes.
//     Mirrors upstream's `truncate_text` in core/agent_runtime/helpers.py.
package main

import (
	"context"
	"fmt"
)

// truncationMarker is appended when output overflows MaxToolBytes. Same
// glyph (U+2026, horizontal ellipsis) and bracketed tag upstream uses.
const truncationMarker = "… [truncated]"

// dispatchToolCall runs one ToolCallRequest against the registry and shapes
// the result into a tool_result ContentBlock the runner can append back to
// the conversation.
//
// Errors are converted into IsError=true blocks (not returned) — the loop
// continues so the model can react. Returning an error is reserved for
// programmer mistakes (e.g. nil registry).
func dispatchToolCall(
	ctx context.Context,
	reg *Registry,
	call ToolCallRequest,
	maxBytes int,
) ContentBlock {
	if reg == nil {
		return ContentBlock{
			Type:      "tool_result",
			ToolUseID: call.ID,
			Output:    fmt.Sprintf("tool %q called but no registry is configured", call.Name),
			IsError:   true,
		}
	}

	tool, ok := reg.Get(call.Name)
	if !ok {
		return ContentBlock{
			Type:      "tool_result",
			ToolUseID: call.ID,
			Output:    fmt.Sprintf("tool %q not found", call.Name),
			IsError:   true,
		}
	}

	out, err := tool.Run(ctx, call.Args)
	if err != nil {
		return ContentBlock{
			Type:      "tool_result",
			ToolUseID: call.ID,
			Output:    fmt.Sprintf("tool %q failed: %v", call.Name, err),
			IsError:   true,
		}
	}

	return ContentBlock{
		Type:      "tool_result",
		ToolUseID: call.ID,
		Output:    truncate(out, maxBytes),
	}
}

// truncate returns s unchanged if maxBytes <= 0 or len(s) <= maxBytes.
// Otherwise it returns the first maxBytes-len(marker) bytes of s plus the
// marker, so the total length is exactly maxBytes (assuming maxBytes >=
// len(marker)).
func truncate(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	if maxBytes <= len(truncationMarker) {
		// Pathological budget — just return the marker, no payload.
		return truncationMarker
	}
	keep := maxBytes - len(truncationMarker)
	return s[:keep] + truncationMarker
}
