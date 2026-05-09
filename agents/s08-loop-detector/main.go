// Package main — s08-loop-detector CLI demo.
//
// Run: go run .
//
// Simulates five consecutive calls to the same tool name and prints the
// Status returned by CheckTool each time. Demonstrates that the loop
// detector fires on call #5 (the run-of-five threshold) — an LLM that
// keeps invoking the same tool without progress is the canonical case
// the detector catches.
package main

import (
	"fmt"
	"os"
)

func main() {
	d := NewLoopDetector()
	const repeated = "execute_python"

	fmt.Printf("LoopDetector demo: calling %q five times in a row\n", repeated)
	fmt.Println("---")
	for i := 1; i <= 5; i++ {
		st := d.CheckTool(repeated)
		fmt.Printf("call #%d  code=%-14s should_stop=%-5v  message=%s\n",
			i, st.Code, st.ShouldStop, st.Message)
		if st.ShouldStop {
			fmt.Println("---")
			fmt.Printf("aborting on call #%d due to %s\n", i, st.Code)
			os.Exit(0)
		}
	}
	fmt.Println("---")
	fmt.Println("no abort triggered (this should not happen with default MaxRepeats=5)")
}
