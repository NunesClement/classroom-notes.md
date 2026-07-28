package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "policyctl:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("policyctl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config/policy.example.yaml", "path to the policy YAML")
	snapshotPath := flags.String("snapshot", "-", "path to a snapshot JSON, or - for stdin")
	validateOnly := flags.Bool("validate-config", false, "validate configuration and exit")
	pretty := flags.Bool("pretty", true, "pretty-print the decision JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	config, err := policy.LoadConfigFile(*configPath)
	if err != nil {
		return err
	}
	if *validateOnly {
		fmt.Fprintln(stdout, "configuration is valid")
		return nil
	}

	var input io.Reader = os.Stdin
	if *snapshotPath != "-" {
		file, openErr := os.Open(*snapshotPath)
		if openErr != nil {
			return fmt.Errorf("open snapshot: %w", openErr)
		}
		defer file.Close()
		input = file
	}
	var snapshot policy.Snapshot
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("snapshot must contain exactly one JSON document")
	}

	engine, err := policy.NewEngine(config)
	if err != nil {
		return err
	}
	decision, err := engine.Decide(snapshot)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	if *pretty {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(decision); err != nil {
		return fmt.Errorf("encode decision: %w", err)
	}
	return nil
}
