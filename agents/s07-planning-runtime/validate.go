// File: validate.go — cheap plan-shape validation.
//
// Mirrors upstream's "string substring" check: if the lowercased plan text
// contains every required section name as a substring, the plan is shaped
// correctly. This is intentionally loose — perfect YAML parsing is not the
// goal here. The orchestrator will pay attention to ValidatePlanText's
// missing-list and either retry or coerce. The five required sections are
// taken verbatim from upstream:
//
//	REQUIRED_PLAN_SECTIONS = (
//	    "file_structure",
//	    "implementation_components",
//	    "validation_approach",
//	    "environment_setup",
//	    "implementation_strategy",
//	)
//
// Upstream counterpart: workflows/planning_runtime.py:18-24 + 130-145.
package main

import "strings"

// RequiredPlanSections is the canonical (and ordered) list of section names
// the planning runtime expects to see in any valid plan text. Order matters
// only for human-readable reporting — the validator is order-insensitive.
var RequiredPlanSections = []string{
	"file_structure",
	"implementation_components",
	"validation_approach",
	"environment_setup",
	"implementation_strategy",
}

// ValidatePlanText returns the names of required sections that are MISSING
// from the supplied text. An empty (or nil) return value means the plan is
// shaped correctly. The check is case-insensitive substring match — same as
// upstream's `f"{section}:" not in lower_text` behavior, generalized so
// either "file_structure:" (YAML key) or "## file_structure" (markdown
// heading) counts as present.
//
// The function is pure: no I/O, no allocation beyond the result slice.
func ValidatePlanText(text string) []string {
	lower := strings.ToLower(text)
	var missing []string
	for _, section := range RequiredPlanSections {
		if !strings.Contains(lower, section) {
			missing = append(missing, section)
		}
	}
	return missing
}
