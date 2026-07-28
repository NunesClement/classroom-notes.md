package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
)

func TestRunProducesOfflineDecision(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "policy.yaml")
	snapshotPath := filepath.Join(directory, "snapshot.json")
	writeTestFile(t, configPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
maxConcurrent: 1
`)
	writeTestFile(t, snapshotPath, `{
  "at": "2026-07-27T12:00:00Z",
  "ready": [
    {
      "id": "routine",
      "enqueuedAt": "2026-07-27T11:59:00Z",
      "priority": 10,
      "estimatedRuntime": "10s",
      "maxQueueLatency": "5m"
    },
    {
      "id": "urgent",
      "enqueuedAt": "2026-07-27T11:59:00Z",
      "priority": 90,
      "estimatedRuntime": "10s",
      "maxQueueLatency": "5m"
    }
  ]
}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{
		"-config", configPath,
		"-snapshot", snapshotPath,
		"-pretty=false",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run failed: %v (stderr: %s)", err, stderr.String())
	}
	var decision policy.Decision
	if err := json.Unmarshal(stdout.Bytes(), &decision); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if selected := decision.SelectedIDs(); len(selected) != 1 || selected[0] != "urgent" {
		t.Fatalf("expected urgent selection, got %v", selected)
	}
}

func TestRunValidatesWithoutSnapshot(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "policy.yaml")
	writeTestFile(t, configPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
`)

	var stdout bytes.Buffer
	err := run(
		[]string{"-config", configPath, "-validate-config"},
		&stdout,
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "configuration is valid" {
		t.Fatalf("unexpected output %q", stdout.String())
	}
}

func TestRunRejectsUnknownSnapshotFields(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "policy.yaml")
	snapshotPath := filepath.Join(directory, "snapshot.json")
	writeTestFile(t, configPath, `
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
`)
	writeTestFile(t, snapshotPath, `{
  "at": "2026-07-27T12:00:00Z",
  "ready": [],
  "typo": true
}`)

	err := run(
		[]string{"-config", configPath, "-snapshot", snapshotPath},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("expected strict snapshot decoding error")
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
