package adapter

import (
	"strings"
	"testing"

	core "github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
	"github.com/waggle-sensor/edge-scheduler/pkg/datatype"
)

func TestRequestedResourcesParsesSAGEQuantities(t *testing.T) {
	runtime := testRuntime("detector", "instance-a", nil)
	runtime.Plugin.PluginSpec.Resource = map[string]string{
		"request.cpu":    "750m",
		"request.memory": "1Gi",
		"limit.gpu":      "1",
	}

	got, err := requestedResources(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUMilli != 750 {
		t.Fatalf("CPU: got %d, want 750m", got.CPUMilli)
	}
	if got.MemoryBytes != 1024*1024*1024 {
		t.Fatalf("memory: got %d, want 1Gi", got.MemoryBytes)
	}
	if got.GPUs != 1 {
		t.Fatalf("GPUs: got %d, want 1", got.GPUs)
	}
}

func TestRequestedResourcesFallsBackFromRequestToLimit(t *testing.T) {
	runtime := testRuntime("detector", "instance-a", nil)
	runtime.Plugin.PluginSpec.Resource = map[string]string{
		"limit.cpu":    "2",
		"limit.memory": "512Mi",
	}

	got, err := requestedResources(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUMilli != 2000 {
		t.Fatalf("CPU: got %d, want 2000m", got.CPUMilli)
	}
	if got.MemoryBytes != 512*1024*1024 {
		t.Fatalf("memory: got %d, want 512Mi", got.MemoryBytes)
	}
}

func TestRequestedResourcesRejectsInvalidQuantities(t *testing.T) {
	tests := map[string]map[string]string{
		"malformed CPU": {
			"request.cpu": "many",
		},
		"empty memory": {
			"request.memory": " ",
		},
		"negative CPU": {
			"limit.cpu": "-1",
		},
		"fractional GPU": {
			"limit.gpu": "500m",
		},
		"invalid secondary alias": {
			"request.cpu": "500m",
			"limit.cpu":   "invalid",
		},
		"invalid extended resource": {
			"example.com/fpga": "many",
		},
		"invalid resource name": {
			"not a resource": "1",
		},
		"likely built-in typo": {
			"requests.cpu": "1",
		},
		"conflicting GPU aliases": {
			"limit.gpu":      "1",
			"nvidia.com/gpu": "2",
		},
		"CPU request exceeds limit": {
			"request.cpu": "2",
			"limit.cpu":   "1",
		},
		"memory request exceeds limit": {
			"request.memory": "2Gi",
			"limit.memory":   "1Gi",
		},
		"fractional extended resource": {
			"example.com/fpga": "500m",
		},
		"CPU millicore overflow": {
			"request.cpu": "1000000000000000000000000000000",
		},
		"memory byte overflow": {
			"request.memory": "1000000000000000000000000000000",
		},
	}

	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			runtime := testRuntime("detector", "instance-a", nil)
			runtime.Plugin.PluginSpec.Resource = values
			if _, err := requestedResources(runtime); err == nil {
				t.Fatalf("requestedResources(%v) succeeded, want an error", values)
			}
		})
	}
}

func TestRequestedResourcesValidatesButDoesNotCountExtendedResources(t *testing.T) {
	runtime := testRuntime("detector", "instance-a", nil)
	runtime.Plugin.PluginSpec.Resource = map[string]string{
		"example.com/fpga": "1",
	}
	got, err := requestedResources(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if got != (core.Resources{}) {
		t.Fatalf("extended resource unexpectedly changed core capacity accounting: %+v", got)
	}
}

func TestRequestedResourcesAllowsMatchingGPUAliases(t *testing.T) {
	runtime := testRuntime("detector", "instance-a", nil)
	runtime.Plugin.PluginSpec.Resource = map[string]string{
		"limit.gpu":      "1",
		"nvidia.com/gpu": "1",
	}
	got, err := requestedResources(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if got.GPUs != 1 {
		t.Fatalf("GPUs: got %d, want 1", got.GPUs)
	}
}

func TestAvailableResourcesRejectsInvalidQuantities(t *testing.T) {
	tests := map[string]datatype.Resource{
		"malformed CPU": {CPU: "invalid", Memory: "1Gi"},
		"empty CPU":     {CPU: "", Memory: "1Gi"},
		"negative CPU":  {CPU: "-1", Memory: "1Gi"},
		"bad memory":    {CPU: "1", Memory: "a lot"},
		"CPU overflow":  {CPU: "1000000000000000000000000000000", Memory: "1Gi"},
		"memory overflow": {
			CPU:    "1",
			Memory: "1000000000000000000000000000000",
		},
	}

	for name, available := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := availableResources(available, 1); err == nil {
				t.Fatalf("availableResources(%+v) succeeded, want an error", available)
			}
		})
	}
}

func TestMalformedGPUHintIsConservativelyGPURequired(t *testing.T) {
	runtime := testRuntime("detector", "instance-a", nil)
	runtime.Plugin.PluginSpec.Resource = map[string]string{
		"limit.gpu": "invalid",
	}
	if !requiresGPU(runtime) {
		t.Fatal("malformed explicit GPU resource was treated as CPU-only")
	}
}

func TestResourceErrorNamesTheField(t *testing.T) {
	runtime := testRuntime("detector", "instance-a", nil)
	runtime.Plugin.PluginSpec.Resource = map[string]string{
		"request.memory": "invalid",
	}
	_, err := requestedResources(runtime)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "request.memory") {
		t.Fatalf("error %q does not name request.memory", err)
	}
}
