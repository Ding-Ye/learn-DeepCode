// Package main — s05-workflow-context CLI demo.
//
// Run: go run . <input>
//
// Examples:
//
//	go run . paper.pdf
//	go run . https://arxiv.org/abs/2401.01234
//	go run . spec.md
//
// Output: each WorkflowContext field on its own "key: value" line, then
// every derived path. Useful as a sanity check before plumbing the context
// into s07/s10.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	workspace := flag.String("workspace", "", "override workspace root (default $HOME/.deepcode-learn)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: s05 [-workspace DIR] <input>\n\n"+
				"  Print the WorkflowContext built from <input>.\n"+
				"  <input> can be a local path (.pdf/.md/.docx/.txt/.html)\n"+
				"  or an http(s):// URL.\n")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	ctx, err := Prepare(flag.Arg(0), Options{WorkspaceRoot: *workspace})
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("task_id:        %s\n", ctx.TaskID())
	fmt.Printf("input_source:   %s\n", ctx.InputSource())
	fmt.Printf("input_kind:     %s\n", ctx.InputKind())
	fmt.Printf("workspace_root: %s\n", ctx.WorkspaceRoot())
	fmt.Printf("task_dir:       %s\n", ctx.TaskDir())
	fmt.Println("---")
	fmt.Printf("reference_path:              %s\n", ctx.ReferencePath())
	fmt.Printf("initial_plan_path:           %s\n", ctx.InitialPlanPath())
	fmt.Printf("implementation_report_path:  %s\n", ctx.ImplementationReportPath())
	fmt.Printf("logs_dir:                    %s\n", ctx.LogsDir())
	fmt.Printf("generate_code_dir:           %s\n", ctx.GenerateCodeDir())
}
