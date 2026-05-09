package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	verbose := flag.Bool("v", false, "print schema list to stderr")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: s02 [-v] <tool-name> [<json-args>]\n\n"+
				"  Built-in tools registered: echo, now, mcp_demo (sim).\n\n"+
				"  Examples:\n"+
				"    s02 -v echo '{\"text\":\"hi\"}'\n"+
				"    s02 now\n"+
				"    s02 -v mcp_demo '{}'\n")
	}
	flag.Parse()

	r := NewRegistry()
	r.Register(NewEchoTool())
	r.Register(NewNowTool())
	r.Register(NewMCPSubprocessTool("demo"))
	defer func() {
		if err := r.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "[s02] close err: %v\n", err)
		}
	}()

	if *verbose {
		schemas := r.List()
		fmt.Fprintf(os.Stderr, "[s02] %d tool(s) registered:\n", len(schemas))
		for _, s := range schemas {
			fmt.Fprintf(os.Stderr, "  - %s\n", s.Name)
		}
	}

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	name := flag.Arg(0)
	tool, ok := r.Get(name)
	if !ok {
		log.Fatalf("tool %q not found. Available: %s", name, strings.Join(r.Names(), ", "))
	}

	argsJSON := "{}"
	if flag.NArg() > 1 {
		argsJSON = strings.Join(flag.Args()[1:], " ")
	}
	if !json.Valid([]byte(argsJSON)) {
		log.Fatalf("invalid JSON args: %s", argsJSON)
	}

	out, err := tool.Run(context.Background(), json.RawMessage(argsJSON))
	if err != nil {
		log.Fatalf("run %s: %v", name, err)
	}
	fmt.Println(out)
}
