// File: main.go — small CLI for the planning runtime.
//
// Usage:
//
//	s07-planning-runtime <plan-file>
//
// Reads the candidate plan text, runs ValidatePlanText, prints OK or the
// list of missing sections, and records one Attempt to a per-process
// scratch directory under $TMPDIR/learn-deepcode-s07/. The scratch dir is
// only there so a learner running `go run .` sees the JSONL file land
// somewhere — production callers pass a real WorkflowContext.TaskDir().
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: s07-planning-runtime <plan-file>\n\n"+
				"  Validate the candidate plan text. Prints OK or the missing\n"+
				"  required sections, and appends one Attempt row to\n"+
				"  $TMPDIR/learn-deepcode-s07/planning_attempts.jsonl.\n")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	planPath := flag.Arg(0)

	data, err := os.ReadFile(planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[s07] read plan: %v\n", err)
		os.Exit(1)
	}

	missing := ValidatePlanText(string(data))

	scratch := filepath.Join(os.TempDir(), "learn-deepcode-s07")
	rt := &PlanningRuntime{}
	ctx := context.Background()

	att := Attempt{
		Provider:  "cli",
		Model:     "(none)",
		OK:        len(missing) == 0,
		PlanBytes: len(data),
	}
	if len(missing) > 0 {
		att.Error = fmt.Sprintf("missing sections: %v", missing)
	}
	if err := rt.RecordAttempt(ctx, scratch, att); err != nil {
		fmt.Fprintf(os.Stderr, "[s07] record attempt: %v\n", err)
		os.Exit(1)
	}

	if len(missing) == 0 {
		fmt.Printf("OK — all %d required sections present (%d bytes)\n",
			len(RequiredPlanSections), len(data))
		fmt.Fprintf(os.Stderr, "[s07] attempt logged at %s\n",
			PlanningPaths(scratch).Attempts)
		return
	}

	fmt.Printf("MISSING %d/%d sections:\n", len(missing), len(RequiredPlanSections))
	for _, name := range missing {
		fmt.Printf("  - %s\n", name)
	}
	fmt.Fprintf(os.Stderr, "[s07] attempt logged at %s\n",
		PlanningPaths(scratch).Attempts)
	os.Exit(3)
}
