// Package main — s06-tool-capable-runner.
//
// File: spec.go — RunSpec (input) + RunResult (output) + StopReason
// constants. Mirrors upstream's `AgentRunSpec` / `AgentRunResult` dataclasses
// in core/agent_runtime/runner.py, trimmed to the teaching cut.
package main

// StopReason values returned by Runner.Run. The runner sets exactly one.
const (
	// StopDone — the model returned final text on this iteration. The
	// happy path: FinalMessage holds that assistant turn.
	StopDone = "done"

	// StopMaxIterations — the loop hit MaxIterations without the model
	// emitting a final text answer. FinalMessage is the synthetic apology
	// from upstream's _DEFAULT_MAX_ITERATIONS_MESSAGE template.
	StopMaxIterations = "max_iterations"

	// StopError — the provider call failed in a way the runner couldn't
	// recover from. Err is non-nil; FinalMessage is empty.
	StopError = "error"
)

// RunSpec is everything Runner.Run needs to do one full agent execution.
//
// The runner copies InitialMessages — the caller's slice is never mutated.
// Tools is a *Registry (s06's minimal flavour); pass any registry whose
// Get(name) returns a tool with the same Run signature.
type RunSpec struct {
	// InitialMessages seeds the conversation. Typically one system message
	// plus one user message; the runner appends to a copy as the loop
	// iterates.
	InitialMessages []Message

	// Tools is the dispatch table. May be nil if the prompt is tool-free,
	// but then the model should not emit tool_use blocks. The schema list
	// from Tools.List() is sent to the model on every iteration.
	Tools *Registry

	// Model is the model id passed through to Provider.Chat. The runner
	// itself doesn't interpret it.
	Model string

	// MaxIterations bounds the loop. Each iteration is one call to
	// Provider.Chat plus optional tool dispatch. Hit this and the runner
	// returns StopMaxIterations with the synthetic apology.
	MaxIterations int

	// MaxToolBytes truncates oversized tool output. Anything longer is
	// snipped to MaxToolBytes-len(marker) bytes plus the marker
	// "… [truncated]". Zero or negative means "no limit".
	MaxToolBytes int

	// MaxTokens is forwarded to Provider.Chat unchanged.
	MaxTokens int

	// Temperature is forwarded to Provider.Chat unchanged.
	Temperature float64
}

// RunResult is what Runner.Run returns. StopReason is exactly one of
// StopDone | StopMaxIterations | StopError.
type RunResult struct {
	// FinalMessage is the assistant turn the loop terminated on. On
	// StopDone it holds the model's final text answer; on StopMaxIterations
	// it holds the synthetic apology; on StopError it is empty.
	FinalMessage Message

	// AllMessages is the full transcript: InitialMessages + every assistant
	// turn + every tool_result the loop appended. Useful for tests and
	// debugging.
	AllMessages []Message

	// StopReason is one of the Stop* constants.
	StopReason string

	// Iterations is how many provider calls the loop made before stopping.
	Iterations int
}
