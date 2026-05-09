// File: main.go — small CLI to inspect resolved phase settings.
//
//	s03-config-loader [-phase planning] <config.json>
//
// Prints the AgentSettings as JSON to stdout. Useful for sanity-checking a
// real deepcode_config.json before s04+ wires it into a Provider.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
)

func main() {
	phase := flag.String("phase", "default", "phase name (e.g. planning, implementation)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: s03-config-loader [-phase <name>] <config.json>\n\n"+
				"  Loads the JSON config (with ${ENV_VAR} expansion) and prints\n"+
				"  the resolved AgentSettings for the requested phase.\n\n"+
				"  Examples:\n"+
				"    s03-config-loader -phase planning ./deepcode_config.json\n"+
				"    s03-config-loader testdata/deepcode_config.json\n")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	path := flag.Arg(0)

	cfg, err := Load(context.Background(), path)
	if err != nil {
		var miss *MissingEnvError
		if errors.As(err, &miss) {
			fmt.Fprintf(os.Stderr, "[s03] missing env var: %s\n", miss.Name)
			os.Exit(3)
		}
		fmt.Fprintf(os.Stderr, "[s03] load: %v\n", err)
		os.Exit(1)
	}

	settings := cfg.Resolve(*phase)
	fmt.Fprintf(os.Stderr, "[s03] phase=%q provider=%q model=%q\n",
		*phase, settings.Provider, settings.Model)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		fmt.Fprintf(os.Stderr, "[s03] encode: %v\n", err)
		os.Exit(1)
	}
}
