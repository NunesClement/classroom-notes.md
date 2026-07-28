package adapter

import (
	"strings"
	"testing"
	"time"

	core "github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
)

func TestParseHintsUsesDefaultsAndParsesOverrides(t *testing.T) {
	config := core.DefaultConfig()
	config.TrustWorkloadEnvHints = true
	deadline := time.Date(2026, 7, 27, 14, 30, 0, 0, time.UTC)

	got, err := parseHints(map[string]string{
		HintPriority:         " 91 ",
		HintMaxLatency:       "45s",
		HintEstimatedRuntime: "7s",
		HintDeadlineAt:       deadline.Format(time.RFC3339),
		HintSuccessRate:      "0.875",
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	if got.priority != 91 {
		t.Fatalf("priority: got %d, want 91", got.priority)
	}
	if got.maxLatency.Value() != 45*time.Second {
		t.Fatalf("max latency: got %s, want 45s", got.maxLatency)
	}
	if got.estimatedRuntime.Value() != 7*time.Second {
		t.Fatalf("estimated runtime: got %s, want 7s", got.estimatedRuntime)
	}
	if got.deadlineAt == nil || !got.deadlineAt.Equal(deadline) {
		t.Fatalf("deadline: got %v, want %s", got.deadlineAt, deadline)
	}
	if got.successRate == nil || *got.successRate != 0.875 {
		t.Fatalf("success rate: got %v, want 0.875", got.successRate)
	}

	defaults, err := parseHints(nil, config)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.priority != config.DefaultPriority ||
		defaults.maxLatency != config.DefaultMaxQueueLatency ||
		defaults.estimatedRuntime != config.DefaultEstimatedRuntime {
		t.Fatalf("missing hints did not preserve defaults: %+v", defaults)
	}
}

func TestParseHintsRejectsInvalidExplicitValues(t *testing.T) {
	config := core.DefaultConfig()
	config.TrustWorkloadEnvHints = true
	tests := map[string]map[string]string{
		"empty":             {HintPriority: "  "},
		"priority text":     {HintPriority: "urgent"},
		"priority range":    {HintPriority: "101"},
		"latency malformed": {HintMaxLatency: "soon"},
		"latency zero":      {HintMaxLatency: "0s"},
		"runtime negative":  {HintEstimatedRuntime: "-1s"},
		"deadline":          {HintDeadlineAt: "tomorrow"},
		"success negative":  {HintSuccessRate: "-0.1"},
		"success range":     {HintSuccessRate: "1.1"},
		"success NaN":       {HintSuccessRate: "NaN"},
		"success infinity":  {HintSuccessRate: "+Inf"},
	}

	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseHints(values, config); err == nil {
				t.Fatalf("parseHints(%v) succeeded, want an error", values)
			}
		})
	}
}

func TestHintErrorsNameTheInvalidField(t *testing.T) {
	config := core.DefaultConfig()
	config.TrustWorkloadEnvHints = true
	_, err := parseHints(
		map[string]string{HintEstimatedRuntime: "invalid"},
		config,
	)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), HintEstimatedRuntime) {
		t.Fatalf("error %q does not name %s", err, HintEstimatedRuntime)
	}
}

func TestParseHintsIgnoresUntrustedWorkloadEnvironment(t *testing.T) {
	config := core.DefaultConfig()
	config.DefaultPriority = 17

	got, err := parseHints(map[string]string{
		HintPriority:         "100",
		HintMaxLatency:       "1ns",
		HintEstimatedRuntime: "invalid",
		HintSuccessRate:      "1",
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	if got.priority != 17 || got.maxLatency != config.DefaultMaxQueueLatency ||
		got.estimatedRuntime != config.DefaultEstimatedRuntime || got.successRate != nil {
		t.Fatalf("untrusted hints changed defaults: %+v", got)
	}
}
