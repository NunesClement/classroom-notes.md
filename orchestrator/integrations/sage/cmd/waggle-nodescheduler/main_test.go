package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateConfigStopsBeforeSAGEWiring(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	writeConfig(t, policyPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
maxConcurrent: 1
`)

	var output bytes.Buffer
	err := run([]string{
		"-policy-config", policyPath,
		"-validate-config",
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != "configuration is valid" {
		t.Fatalf("unexpected output %q", output.String())
	}
}

func TestRejectsUnsupportedPolicyBeforeConnecting(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	writeConfig(t, policyPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
`)

	err := run([]string{
		"-policy-config", policyPath,
		"-policy", "default",
		"-validate-config",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported policy error")
	}
}

func TestWaggleConfigCanReferencePolicyConfig(t *testing.T) {
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "policy.yaml")
	runtimePath := filepath.Join(directory, "nodescheduler.yaml")
	writeConfig(t, policyPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
`)
	writeConfig(t, runtimePath, `
nodeName: W123
policy: resilient-urgent
policyConfig: `+policyPath+`
`)

	var output bytes.Buffer
	if err := run([]string{"-config", runtimePath, "-validate-config"}, &output); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != "configuration is valid" {
		t.Fatalf("unexpected output %q", output.String())
	}
}

func TestExplicitPolicyConfigFlagOverridesWaggleConfig(t *testing.T) {
	directory := t.TempDir()
	validPolicyPath := filepath.Join(directory, "valid-policy.yaml")
	runtimePath := filepath.Join(directory, "nodescheduler.yaml")
	writeConfig(t, validPolicyPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
`)
	writeConfig(t, runtimePath, `
policy: resilient-urgent
policyConfig: /does/not/exist.yaml
`)

	var output bytes.Buffer
	err := run([]string{
		"-config", runtimePath,
		"-policy-config", validPolicyPath,
		"-validate-config",
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != "configuration is valid" {
		t.Fatalf("unexpected output %q", output.String())
	}
}

func TestUnsafeLegacyModesAreRejectedBeforeConnecting(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	writeConfig(t, policyPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
`)

	for _, flagName := range []string{"-no-rabbitmq", "-simulate"} {
		t.Run(flagName, func(t *testing.T) {
			err := run(
				[]string{"-policy-config", policyPath, flagName},
				&bytes.Buffer{},
			)
			if err == nil {
				t.Fatalf("expected %s live mode to be rejected", flagName)
			}
		})
	}
}

func TestLiveModeRejectsInheritedRabbitMQCredentialsBeforeConnecting(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	writeConfig(t, policyPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
`)

	err := run([]string{
		"-policy-config", policyPath,
		"-rabbitmq-username", "service",
		"-rabbitmq-password", "service",
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "service/service") {
		t.Fatalf("expected inherited credential rejection, got %v", err)
	}
}

func TestLiveModeRejectsEmptyRabbitMQCredentialsBeforeConnecting(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	writeConfig(t, policyPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
`)

	err := run([]string{
		"-policy-config", policyPath,
		"-rabbitmq-username", "",
		"-rabbitmq-password", "",
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "must be provided explicitly") {
		t.Fatalf("expected empty credential rejection, got %v", err)
	}
}

func TestUnsafeRabbitMQDefaultsRequireExplicitAcknowledgement(t *testing.T) {
	config := defaultRuntimeConfig()
	config.RabbitmqUsername = "service"
	config.RabbitmqPassword = "service"
	if err := validateLiveConfig(config, false); err == nil {
		t.Fatal("expected inherited credentials to be rejected")
	}
	if err := validateLiveConfig(config, true); err != nil {
		t.Fatalf("explicit acknowledgement was rejected: %v", err)
	}
}

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
