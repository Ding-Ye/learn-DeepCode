// File: load.go — read deepcode_config.json + resolve ${ENV_VAR} references.
//
// The loader is the SINGLE place that consults os.Environ. Once Load returns,
// every consumer reads from the parsed Config — no other file in the program
// should call os.Getenv. This is the "single source of truth" discipline
// upstream's core/config.py also enforces.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// envRefPattern matches the upstream regex `\${[A-Za-z_][A-Za-z0-9_]*}`.
// We tighten the spec slightly (uppercase + underscore) to discourage typos
// like ${path} masquerading as a config knob; this matches every example
// in deepcode_config.json.example.
var envRefPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// MissingEnvError is returned by Load when a referenced ${VAR} is unset.
// Callers can errors.As() to surface a precise message in CLIs.
type MissingEnvError struct {
	Name string // the variable name (without ${})
}

func (e *MissingEnvError) Error() string {
	return fmt.Sprintf("environment variable %q referenced in config is not set", e.Name)
}

// Load reads the JSON file at path, expands ${VAR} references against the
// process environment, and unmarshals the result into a Config.
//
// ctx is honoured so callers can cancel long-lived loaders; we don't do any
// I/O beyond a single ReadFile, but ctx-as-first-param is a project-wide
// convention (see CONVENTIONS in plan.md).
func Load(ctx context.Context, path string) (*Config, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	expanded, err := expandEnv(raw)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(expanded, &c); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return &c, nil
}

// expandEnv substitutes every "${VAR}" in b with os.Getenv(VAR). When the
// variable is unset (not just empty — explicitly missing from the
// environment), it returns *MissingEnvError so callers can react.
//
// We operate on the raw JSON bytes pre-Unmarshal: this keeps the substitution
// uniform across every string value in the tree without walking the parsed
// struct. Trade-off: a "${VAR}" that happens to fall inside a JSON key is
// also expanded — accepted, mirrors upstream's behaviour.
func expandEnv(b []byte) ([]byte, error) {
	var miss *MissingEnvError
	out := envRefPattern.ReplaceAllFunc(b, func(match []byte) []byte {
		name := string(match[2 : len(match)-1]) // strip "${" and "}"
		val, ok := os.LookupEnv(name)
		if !ok {
			if miss == nil {
				miss = &MissingEnvError{Name: name}
			}
			return match
		}
		// JSON-escape the value so e.g. a secret containing `"` doesn't
		// break the surrounding string literal.
		escaped, err := json.Marshal(val)
		if err != nil {
			return match
		}
		// json.Marshal wraps in quotes; we want only the inner content.
		return escaped[1 : len(escaped)-1]
	})
	if miss != nil {
		return nil, miss
	}
	return out, nil
}
