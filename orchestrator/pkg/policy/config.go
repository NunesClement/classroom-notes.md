package policy

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ConfigAPIVersion = "scheduling.sagecontinuum.org/v1alpha1"
	ConfigKind       = "ResilientUrgentPolicy"
	MaxConcurrentCap = 1024
)

// Weights controls the explainable score used to rank ready tasks.
type Weights struct {
	Priority    float64 `json:"priority" yaml:"priority"`
	Slack       float64 `json:"slack" yaml:"slack"`
	Age         float64 `json:"age" yaml:"age"`
	Reliability float64 `json:"reliability" yaml:"reliability"`
}

// Config contains only policy behavior. It has no SAGE, Kubernetes, or network
// settings, which keeps the decision engine deterministic and portable.
type Config struct {
	APIVersion              string   `json:"apiVersion" yaml:"apiVersion"`
	Kind                    string   `json:"kind" yaml:"kind"`
	MaxConcurrent           int      `json:"maxConcurrent" yaml:"maxConcurrent"`
	MaxGPUConcurrent        int      `json:"maxGPUConcurrent" yaml:"maxGPUConcurrent"`
	DefaultPriority         int      `json:"defaultPriority" yaml:"defaultPriority"`
	DefaultEstimatedRuntime Duration `json:"defaultEstimatedRuntime" yaml:"defaultEstimatedRuntime"`
	DefaultMaxQueueLatency  Duration `json:"defaultMaxQueueLatency" yaml:"defaultMaxQueueLatency"`
	AgingHorizon            Duration `json:"agingHorizon" yaml:"agingHorizon"`
	SlackHorizon            Duration `json:"slackHorizon" yaml:"slackHorizon"`
	DefaultSuccessRate      float64  `json:"defaultSuccessRate" yaml:"defaultSuccessRate"`
	MinimumSuccessRate      float64  `json:"minimumSuccessRate" yaml:"minimumSuccessRate"`
	TrustWorkloadEnvHints   bool     `json:"trustWorkloadEnvHints" yaml:"trustWorkloadEnvHints"`
	EnforceResourceFit      bool     `json:"enforceResourceFit" yaml:"enforceResourceFit"`
	FailOpen                bool     `json:"failOpen" yaml:"failOpen"`
	Weights                 Weights  `json:"weights" yaml:"weights"`
}

func DefaultConfig() Config {
	return Config{
		APIVersion:              ConfigAPIVersion,
		Kind:                    ConfigKind,
		MaxConcurrent:           1,
		MaxGPUConcurrent:        1,
		DefaultPriority:         0,
		DefaultEstimatedRuntime: NewDuration(30 * time.Second),
		DefaultMaxQueueLatency:  NewDuration(5 * time.Minute),
		AgingHorizon:            NewDuration(10 * time.Minute),
		SlackHorizon:            NewDuration(5 * time.Minute),
		DefaultSuccessRate:      1,
		MinimumSuccessRate:      0,
		TrustWorkloadEnvHints:   false,
		FailOpen:                true,
		Weights: Weights{
			Priority:    0.45,
			Slack:       0.30,
			Age:         0.15,
			Reliability: 0.10,
		},
	}
}

func (c Config) Validate() error {
	var errs []error
	if c.APIVersion != ConfigAPIVersion {
		errs = append(errs, fmt.Errorf("apiVersion must be %q", ConfigAPIVersion))
	}
	if c.Kind != ConfigKind {
		errs = append(errs, fmt.Errorf("kind must be %q", ConfigKind))
	}
	if c.MaxConcurrent < 1 || c.MaxConcurrent > MaxConcurrentCap {
		errs = append(errs, fmt.Errorf("maxConcurrent must be between 1 and %d", MaxConcurrentCap))
	}
	if c.MaxGPUConcurrent < 0 || c.MaxGPUConcurrent > c.MaxConcurrent {
		errs = append(errs, errors.New("maxGPUConcurrent must be between 0 and maxConcurrent"))
	}
	if c.DefaultPriority < 0 || c.DefaultPriority > 100 {
		errs = append(errs, errors.New("defaultPriority must be between 0 and 100"))
	}
	if c.DefaultEstimatedRuntime.Value() <= 0 {
		errs = append(errs, errors.New("defaultEstimatedRuntime must be positive"))
	}
	if c.DefaultMaxQueueLatency.Value() <= 0 {
		errs = append(errs, errors.New("defaultMaxQueueLatency must be positive"))
	}
	if c.AgingHorizon.Value() <= 0 {
		errs = append(errs, errors.New("agingHorizon must be positive"))
	}
	if c.SlackHorizon.Value() <= 0 {
		errs = append(errs, errors.New("slackHorizon must be positive"))
	}
	if !validProbability(c.DefaultSuccessRate) {
		errs = append(errs, errors.New("defaultSuccessRate must be between 0 and 1"))
	}
	if !validProbability(c.MinimumSuccessRate) {
		errs = append(errs, errors.New("minimumSuccessRate must be between 0 and 1"))
	}
	if c.MinimumSuccessRate > c.DefaultSuccessRate {
		errs = append(errs, errors.New("minimumSuccessRate cannot exceed defaultSuccessRate"))
	}
	weights := []float64{c.Weights.Priority, c.Weights.Slack, c.Weights.Age, c.Weights.Reliability}
	total := 0.0
	for _, weight := range weights {
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 {
			errs = append(errs, errors.New("all weights must be finite and non-negative"))
			break
		}
		total += weight
	}
	if total <= 0 {
		errs = append(errs, errors.New("at least one weight must be positive"))
	} else if math.IsNaN(total) || math.IsInf(total, 0) {
		errs = append(errs, errors.New("sum of weights must be finite"))
	}
	return errors.Join(errs...)
}

func LoadConfig(reader io.Reader) (Config, error) {
	config := DefaultConfig()
	// Require an explicit schema header in files. The remaining fields keep
	// their defaults, but accepting an empty document as a valid versioned
	// policy would make configuration mistakes too easy to miss.
	config.APIVersion = ""
	config.Kind = ""
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode policy config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return Config{}, errors.New("decode policy config: exactly one YAML document is required")
	} else if !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode policy config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate policy config: %w", err)
	}
	return config, nil
}

func LoadConfigFile(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open policy config: %w", err)
	}
	defer file.Close()
	return LoadConfig(file)
}

func validProbability(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
