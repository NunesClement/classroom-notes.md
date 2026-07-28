package adapter

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	core "github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
)

const (
	HintPriority         = "SAGE_SCHEDULER_PRIORITY"
	HintMaxLatency       = "SAGE_SCHEDULER_MAX_LATENCY"
	HintEstimatedRuntime = "SAGE_SCHEDULER_ESTIMATED_RUNTIME"
	HintDeadlineAt       = "SAGE_SCHEDULER_DEADLINE_AT"
	HintSuccessRate      = "SAGE_SCHEDULER_SUCCESS_RATE"
)

type hints struct {
	priority         int
	maxLatency       core.Duration
	estimatedRuntime core.Duration
	deadlineAt       *time.Time
	successRate      *float64
}

func parseHints(values map[string]string, defaults core.Config) (hints, error) {
	result := hints{
		priority:         defaults.DefaultPriority,
		maxLatency:       defaults.DefaultMaxQueueLatency,
		estimatedRuntime: defaults.DefaultEstimatedRuntime,
	}
	// PluginSpec.Env is authored by the workload submitter and is also
	// forwarded into the container. It is not an attested control-plane
	// source, so operators must explicitly opt in before it can steer policy.
	if !defaults.TrustWorkloadEnvHints || values == nil {
		return result, nil
	}

	if raw, ok, err := hintValue(values, HintPriority); err != nil {
		return hints{}, err
	} else if ok {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 100 {
			return hints{}, fmt.Errorf("%s must be an integer between 0 and 100", HintPriority)
		}
		result.priority = value
	}
	if raw, ok, err := hintValue(values, HintMaxLatency); err != nil {
		return hints{}, err
	} else if ok {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return hints{}, fmt.Errorf("%s must be a positive duration", HintMaxLatency)
		}
		result.maxLatency = core.NewDuration(value)
	}
	if raw, ok, err := hintValue(values, HintEstimatedRuntime); err != nil {
		return hints{}, err
	} else if ok {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return hints{}, fmt.Errorf("%s must be a positive duration", HintEstimatedRuntime)
		}
		result.estimatedRuntime = core.NewDuration(value)
	}
	if raw, ok, err := hintValue(values, HintDeadlineAt); err != nil {
		return hints{}, err
	} else if ok {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return hints{}, fmt.Errorf("%s must be an RFC3339 timestamp", HintDeadlineAt)
		}
		result.deadlineAt = &value
	}
	if raw, ok, err := hintValue(values, HintSuccessRate); err != nil {
		return hints{}, err
	} else if ok {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return hints{}, fmt.Errorf("%s must be a number between 0 and 1", HintSuccessRate)
		}
		result.successRate = &value
	}
	return result, nil
}

func hintValue(values map[string]string, key string) (string, bool, error) {
	value, ok := values[key]
	if !ok {
		return "", false, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, fmt.Errorf("%s cannot be empty", key)
	}
	return value, true, nil
}
