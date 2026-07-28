package chaos

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
)

func TestRunExperimentDetectsDecisionBoundary(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(2 * time.Minute)
	experiment := Experiment{
		Baseline: policy.Snapshot{
			At: now,
			Ready: []policy.Task{
				chaosTask("a", now, deadline),
				chaosTask("b", now, deadline),
			},
		},
		Perturbations: []Perturbation{{
			Name:            "b-deadline-minus-one-minute",
			TaskID:          "b",
			DeadlineAtShift: policy.NewDuration(-time.Minute),
		}},
	}
	engine := chaosEngine(t, nil)

	report, err := RunExperiment(engine, experiment)
	if err != nil {
		t.Fatal(err)
	}
	if selected := report.Baseline.SelectedIDs(); !reflect.DeepEqual(selected, []string{"a"}) {
		t.Fatalf("unexpected baseline selection %v", selected)
	}
	if len(report.Runs) != 1 {
		t.Fatalf("expected one run, got %d", len(report.Runs))
	}
	run := report.Runs[0]
	if selected := run.Decision.SelectedIDs(); !reflect.DeepEqual(selected, []string{"b"}) {
		t.Fatalf("unexpected perturbed selection %v", selected)
	}
	if !run.SelectionChanged || run.SelectionDistance != 1 {
		t.Fatalf("expected complete selection divergence, got changed=%v distance=%v", run.SelectionChanged, run.SelectionDistance)
	}
}

func TestRunExperimentDoesNotMutateBaseline(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Minute)
	baseline := policy.Snapshot{
		At:    now,
		Ready: []policy.Task{chaosTask("task", now, deadline)},
	}
	originalDeadline := *baseline.Ready[0].DeadlineAt
	experiment := Experiment{
		Baseline: baseline,
		Perturbations: []Perturbation{{
			Name:            "deadline-shift",
			TaskID:          "task",
			DeadlineAtShift: policy.NewDuration(-30 * time.Second),
		}},
	}

	if _, err := RunExperiment(chaosEngine(t, nil), experiment); err != nil {
		t.Fatal(err)
	}
	if !baseline.Ready[0].DeadlineAt.Equal(originalDeadline) {
		t.Fatal("experiment mutated the caller's baseline")
	}
}

func TestEstimatedRuntimePerturbationCanChangeSelection(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(2 * time.Minute)
	experiment := Experiment{
		Baseline: policy.Snapshot{
			At: now,
			Ready: []policy.Task{
				chaosTask("a", now, deadline),
				chaosTask("b", now, deadline),
			},
		},
		Perturbations: []Perturbation{{
			Name:                  "b-runtime-plus-twenty-seconds",
			TaskID:                "b",
			EstimatedRuntimeShift: policy.NewDuration(20 * time.Second),
		}},
	}

	report, err := RunExperiment(chaosEngine(t, nil), experiment)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Baseline.SelectedIDs(); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("baseline selected %v, want a", got)
	}
	if got := report.Runs[0].Decision.SelectedIDs(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("perturbed decision selected %v, want b", got)
	}
}

func TestDurationAndArrivalDeltasStartFromPolicyDefaults(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	task := policy.Task{
		ID: "task",
	}
	experiment := Experiment{
		Baseline: policy.Snapshot{At: now, Ready: []policy.Task{task}},
		Perturbations: []Perturbation{{
			Name:                  "default-relative-deltas",
			TaskID:                "task",
			PriorityDelta:         5,
			EnqueuedAtShift:       policy.NewDuration(-5 * time.Second),
			EstimatedRuntimeShift: policy.NewDuration(10 * time.Second),
			MaxQueueLatencyShift:  policy.NewDuration(-30 * time.Second),
		}},
	}
	engine := chaosEngine(t, func(config *policy.Config) {
		config.DefaultPriority = 20
		config.DefaultEstimatedRuntime = policy.NewDuration(30 * time.Second)
		config.DefaultMaxQueueLatency = policy.NewDuration(2 * time.Minute)
	})

	report, err := RunExperiment(engine, experiment)
	if err != nil {
		t.Fatal(err)
	}
	decision := report.Runs[0].Decision
	if len(decision.Selected) != 1 {
		t.Fatalf("perturbed task was not admissible: %+v", decision)
	}
	selected := decision.Selected[0]
	if selected.EstimatedRuntime.Value() != 40*time.Second {
		t.Fatalf("runtime: got %s, want default 30s + 10s", selected.EstimatedRuntime)
	}
	if selected.Priority == nil || *selected.Priority != 25 {
		t.Fatalf("priority: got %v, want default 20 + 5", selected.Priority)
	}
	if selected.MaxQueueLatency.Value() != 90*time.Second {
		t.Fatalf("latency: got %s, want default 2m - 30s", selected.MaxQueueLatency)
	}
	if !selected.EnqueuedAt.Equal(now.Add(-5 * time.Second)) {
		t.Fatalf("enqueuedAt: got %s, want snapshot time - 5s", selected.EnqueuedAt)
	}
}

func TestRunExperimentRejectsUnknownTask(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	experiment := Experiment{
		Baseline: policy.Snapshot{
			At:    now,
			Ready: []policy.Task{chaosTask("known", now, now.Add(time.Minute))},
		},
		Perturbations: []Perturbation{{
			Name:          "unknown-task",
			TaskID:        "missing",
			PriorityDelta: 1,
		}},
	}

	if _, err := RunExperiment(chaosEngine(t, nil), experiment); err == nil {
		t.Fatal("expected unknown task error")
	}
}

func TestCapacityPerturbationChangesAdmission(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	available := policy.Resources{CPUMilli: 1000, MemoryBytes: 1024}
	task := chaosTask("task", now, now.Add(time.Minute))
	task.Resources = policy.Resources{CPUMilli: 1000, MemoryBytes: 1024}
	experiment := Experiment{
		Baseline: policy.Snapshot{
			At:        now,
			Ready:     []policy.Task{task},
			Available: &available,
		},
		Perturbations: []Perturbation{{
			Name:           "lose-half-cpu",
			AvailableDelta: &policy.Resources{CPUMilli: -500},
		}},
	}
	engine := chaosEngine(t, func(config *policy.Config) {
		config.EnforceResourceFit = true
	})

	report, err := RunExperiment(engine, experiment)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Baseline.Selected) != 1 || len(report.Runs[0].Decision.Selected) != 0 {
		t.Fatalf("expected capacity loss to defer selection")
	}
	if report.Runs[0].SelectionDistance != 1 {
		t.Fatalf("expected complete divergence, got %v", report.Runs[0].SelectionDistance)
	}
}

func TestSuccessRateDeltaStartsFromPolicyDefault(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	task := chaosTask("task", now, now.Add(time.Minute))
	task.PredictedSuccessRate = nil
	experiment := Experiment{
		Baseline: policy.Snapshot{At: now, Ready: []policy.Task{task}},
		Perturbations: []Perturbation{{
			Name:             "reliability-drop",
			TaskID:           "task",
			SuccessRateDelta: -0.2,
		}},
	}
	engine := chaosEngine(t, func(config *policy.Config) {
		config.DefaultSuccessRate = 0.8
		config.MinimumSuccessRate = 0.7
	})

	report, err := RunExperiment(engine, experiment)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Baseline.Selected) != 1 || len(report.Runs[0].Decision.Selected) != 0 {
		t.Fatalf("expected default 0.8 reduced to 0.6 to cross the 0.7 threshold")
	}
	candidate, err := report.Runs[0].Decision.Candidate("task")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(candidate.Score.Reliability-60) > 1e-9 {
		t.Fatalf("reliability score: got %v, want 60", candidate.Score.Reliability)
	}
}

func chaosTask(id string, enqueuedAt, deadline time.Time) policy.Task {
	return policy.Task{
		ID:                   id,
		EnqueuedAt:           enqueuedAt,
		Priority:             policy.NewPriority(50),
		EstimatedRuntime:     policy.NewDuration(10 * time.Second),
		MaxQueueLatency:      policy.NewDuration(time.Minute),
		DeadlineAt:           &deadline,
		PredictedSuccessRate: nil,
	}
}

func chaosEngine(t *testing.T, mutate func(*policy.Config)) *policy.Engine {
	t.Helper()
	config := policy.DefaultConfig()
	if mutate != nil {
		mutate(&config)
	}
	engine, err := policy.NewEngine(config)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
