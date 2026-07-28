package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/chaos"
	"github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "chaoslab:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("chaoslab", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config/policy.example.yaml", "path to the policy YAML")
	experimentPath := flags.String("experiment", "-", "path to an experiment JSON, or - for stdin")
	pretty := flags.Bool("pretty", true, "pretty-print the report JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	config, err := policy.LoadConfigFile(*configPath)
	if err != nil {
		return err
	}
	engine, err := policy.NewEngine(config)
	if err != nil {
		return err
	}

	input := stdin
	if *experimentPath != "-" {
		file, openErr := os.Open(*experimentPath)
		if openErr != nil {
			return fmt.Errorf("open experiment: %w", openErr)
		}
		defer file.Close()
		input = file
	}
	var experiment chaos.Experiment
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&experiment); err != nil {
		return fmt.Errorf("decode experiment: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("experiment must contain exactly one JSON document")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode experiment: %w", err)
	}

	report, err := chaos.RunExperiment(engine, experiment)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	if *pretty {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	return nil
}
