package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunOfflineExperiment(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "policy.yaml")
	experimentPath := filepath.Join(directory, "experiment.json")
	writeFile(t, configPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
maxConcurrent: 1
`)
	writeFile(t, experimentPath, `{
  "baseline": {
    "at": "2026-07-27T12:00:00Z",
    "ready": [
      {
        "id": "a",
        "enqueuedAt": "2026-07-27T12:00:00Z",
        "priority": 50,
        "estimatedRuntime": "10s",
        "deadlineAt": "2026-07-27T12:02:00Z"
      },
      {
        "id": "b",
        "enqueuedAt": "2026-07-27T12:00:00Z",
        "priority": 50,
        "estimatedRuntime": "10s",
        "deadlineAt": "2026-07-27T12:02:00Z"
      }
    ]
  },
  "perturbations": [
    {
      "name": "earlier-b",
      "taskId": "b",
      "deadlineAtShift": "-1m"
    }
  ]
}`)

	var output bytes.Buffer
	err := run(
		[]string{"-config", configPath, "-experiment", experimentPath, "-pretty=false"},
		bytes.NewBuffer(nil),
		&output,
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"selectionChanged":true`) {
		t.Fatalf("expected changed selection report, got %s", output.String())
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
