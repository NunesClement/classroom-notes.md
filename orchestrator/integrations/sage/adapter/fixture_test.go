package adapter

import (
	"os"
	"testing"

	core "github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
	"github.com/waggle-sensor/edge-scheduler/pkg/datatype"
	"gopkg.in/yaml.v2"
)

func TestSAGEJobExampleMatchesPinnedJobSchema(t *testing.T) {
	data, err := os.ReadFile("../../../examples/sage/job.example.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var job datatype.Job
	if err := yaml.UnmarshalStrict(data, &job); err != nil {
		t.Fatalf("example is not compatible with the pinned SAGE Job schema: %v", err)
	}
	if job.Name == "" || len(job.Plugins) == 0 || len(job.ScienceRules) == 0 {
		t.Fatalf("example decoded incompletely: %+v", job)
	}
	config := core.DefaultConfig()
	config.TrustWorkloadEnvHints = true
	for _, plugin := range job.Plugins {
		if plugin == nil || plugin.PluginSpec == nil {
			t.Fatalf("example contains a plugin without a plugin spec: %+v", plugin)
		}
		if _, err := parseHints(plugin.PluginSpec.Env, config); err != nil {
			t.Fatalf("plugin %q has invalid scheduler hints: %v", plugin.Name, err)
		}
		if _, err := requestedResources(&datatype.PluginRuntime{Plugin: *plugin}); err != nil {
			t.Fatalf("plugin %q has invalid resources: %v", plugin.Name, err)
		}
	}
}
