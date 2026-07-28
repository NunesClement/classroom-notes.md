package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/clementnunes/sage-resilient-urgent-scheduler/integrations/sage/adapter"
	core "github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
	"github.com/waggle-sensor/edge-scheduler/pkg/logger"
	"github.com/waggle-sensor/edge-scheduler/pkg/nodescheduler"
	"gopkg.in/yaml.v2"
)

var Version = "dev"

type runtimeConfig struct {
	nodescheduler.NodeSchedulerConfig `yaml:",inline"`
	PolicyConfigPath                  string `yaml:"policyConfig"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		logger.Error.Print(err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	config := defaultRuntimeConfig()
	flags := flag.NewFlagSet("waggle-nodescheduler", flag.ContinueOnError)
	flags.SetOutput(output)
	var configPath string
	var validateOnly bool
	var showVersion bool
	var allowInsecureRabbitMQDefaults bool

	flags.BoolVar(&config.Debug, "debug", config.Debug, "enable debug logs")
	flags.StringVar(&configPath, "config", "", "path to the Waggle node scheduler YAML")
	flags.StringVar(&config.PolicyConfigPath, "policy-config", "config/policy.example.yaml", "path to resilient-urgent policy YAML")
	flags.BoolVar(&config.Simulate, "simulate", config.Simulate, "validate legacy Waggle simulation config (live execution is rejected)")
	flags.StringVar(&config.Name, "nodename", config.Name, "Waggle node name (VSN)")
	flags.StringVar(&config.Kubeconfig, "kubeconfig", config.Kubeconfig, "absolute path to kubeconfig")
	flags.BoolVar(&config.InCluster, "in-cluster", config.InCluster, "use the in-cluster k3s service account")
	flags.BoolVar(&config.NoRabbitMQ, "no-rabbitmq", config.NoRabbitMQ, "validate legacy no-RabbitMQ config (live execution is rejected)")
	flags.BoolVar(
		&allowInsecureRabbitMQDefaults,
		"allow-insecure-rabbitmq-defaults",
		false,
		"allow the inherited service/service RabbitMQ credentials (unsafe)",
	)
	flags.StringVar(&config.RabbitmqURI, "rabbitmq-uri", config.RabbitmqURI, "RabbitMQ URI")
	flags.StringVar(&config.RabbitmqUsername, "rabbitmq-username", config.RabbitmqUsername, "RabbitMQ username")
	flags.StringVar(&config.RabbitmqPassword, "rabbitmq-password", config.RabbitmqPassword, "RabbitMQ password")
	flags.StringVar(&config.GoalStreamURL, "goalstream-url", config.GoalStreamURL, "goal stream URL")
	flags.StringVar(&config.RuleCheckerURI, "rulechecker-uri", config.RuleCheckerURI, "science rule checker URI")
	flags.StringVar(&config.ScoreboardURI, "scoreboard-uri", config.ScoreboardURI, "scoreboard URI")
	flags.StringVar(&config.SchedulingPolicy, "policy", "resilient-urgent", "must be resilient-urgent")
	flags.BoolVar(&validateOnly, "validate-config", false, "validate policy configuration without connecting to SAGE")
	flags.BoolVar(&showVersion, "version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	explicitFlags := make(map[string]string)
	flags.Visit(func(flagValue *flag.Flag) {
		explicitFlags[flagValue.Name] = flagValue.Value.String()
	})
	if showVersion {
		fmt.Fprintln(output, Version)
		return nil
	}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("read Waggle config: %w", err)
		}
		if err := yaml.UnmarshalStrict(data, &config); err != nil {
			return fmt.Errorf("decode Waggle config: %w", err)
		}
		// Match conventional CLI precedence: explicitly supplied flags win
		// over values loaded from the configuration file.
		for name, value := range explicitFlags {
			if err := flags.Set(name, value); err != nil {
				return fmt.Errorf("reapply command-line flag %q: %w", name, err)
			}
		}
	}
	if config.SchedulingPolicy != "resilient-urgent" {
		return errors.New("this binary only supports -policy resilient-urgent")
	}

	policyConfig, err := core.LoadConfigFile(config.PolicyConfigPath)
	if err != nil {
		return err
	}
	if validateOnly {
		fmt.Fprintln(output, "configuration is valid")
		return nil
	}
	if err := validateLiveConfig(config, allowInsecureRabbitMQDefaults); err != nil {
		return err
	}
	if !config.Debug {
		logger.Debug.SetOutput(io.Discard)
	}
	config.Version = Version
	wagglePolicy, err := adapter.New(policyConfig)
	if err != nil {
		return fmt.Errorf("create resilient-urgent policy: %w", err)
	}

	appID := environment("WAGGLE_APP_ID", "")
	// The pinned builder resolves its compiled-in policy immediately and logs
	// an error for unknown names. Build with a known placeholder, then restore
	// the operator-visible name and inject the adapter below.
	upstreamConfig := config.NodeSchedulerConfig
	upstreamConfig.SchedulingPolicy = "default"
	scheduler := nodescheduler.NewNodeSchedulerBuilder(&upstreamConfig).
		AddGoalManager(appID).
		AddKnowledgebase().
		AddResourceManager().
		AddAPIServer().
		AddLoggerToBeehive(appID).
		AddConnToScoreboard().
		Build()
	upstreamConfig.SchedulingPolicy = config.SchedulingPolicy
	scheduler.SchedulingPolicy = wagglePolicy

	logger.Info.Printf("Waggle node scheduler %q starts with resilient-urgent policy", config.Name)
	if err := scheduler.Configure(); err != nil {
		return fmt.Errorf("configure Waggle node scheduler: %w", err)
	}
	scheduler.Run()
	return nil
}

func validateLiveConfig(config runtimeConfig, allowInsecureRabbitMQDefaults bool) error {
	if config.NoRabbitMQ {
		return errors.New("live no-RabbitMQ mode is unsupported by the pinned Waggle NodeScheduler; use -validate-config only")
	}
	if config.Simulate {
		return errors.New("live simulation mode is unsafe in the pinned Waggle NodeScheduler; use -validate-config only")
	}
	if strings.TrimSpace(config.RabbitmqUsername) == "" ||
		strings.TrimSpace(config.RabbitmqPassword) == "" {
		return errors.New("live RabbitMQ username and password must be provided explicitly")
	}
	if config.RabbitmqUsername == "service" &&
		config.RabbitmqPassword == "service" &&
		!allowInsecureRabbitMQDefaults {
		return errors.New(
			"refusing inherited RabbitMQ credentials service/service; provide Secret-backed " +
				"RABBITMQ_USERNAME and RABBITMQ_PASSWORD or explicitly acknowledge the unsafe defaults",
		)
	}
	return nil
}

func defaultRuntimeConfig() runtimeConfig {
	kubeconfig := ""
	if userHome, err := os.UserHomeDir(); err == nil {
		kubeconfig = filepath.Join(userHome, ".kube", "config")
	}
	return runtimeConfig{
		NodeSchedulerConfig: nodescheduler.NodeSchedulerConfig{
			Name:             environment("WAGGLE_NODE_VSN", "W000"),
			Kubeconfig:       kubeconfig,
			RabbitmqURI:      environment("RABBITMQ_URI", "wes-rabbitmq:5672"),
			RabbitmqUsername: environment("RABBITMQ_USERNAME", "service"),
			RabbitmqPassword: environment("RABBITMQ_PASSWORD", "service"),
			RuleCheckerURI:   "http://wes-sciencerule-checker:5000",
			ScoreboardURI:    "wes-scoreboard:6379",
			SchedulingPolicy: "resilient-urgent",
		},
	}
}

func environment(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
