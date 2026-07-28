package policy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

func TestHigherPriorityWins(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.MaxConcurrent = 1
	})
	snapshot := Snapshot{
		At: fixedNow,
		Ready: []Task{
			testTask("routine", fixedNow.Add(-time.Minute), 10),
			testTask("critical", fixedNow.Add(-time.Minute), 90),
		},
	}

	decision, err := engine.Decide(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "critical")
}

func TestExplicitZeroPriorityIsNotReplacedByDefault(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.DefaultPriority = 35
	})
	task := testTask("explicit-zero", fixedNow, 0)

	decision, err := engine.Decide(Snapshot{At: fixedNow, Ready: []Task{task}})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Selected) != 1 || decision.Selected[0].Priority == nil {
		t.Fatalf("unexpected selection %+v", decision.Selected)
	}
	if got := *decision.Selected[0].Priority; got != 0 {
		t.Fatalf("explicit priority 0 became %d", got)
	}
}

func TestMissingPriorityUsesDefault(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.DefaultPriority = 35
	})
	task := testTask("missing-priority", fixedNow, 0)
	task.Priority = nil

	decision, err := engine.Decide(Snapshot{At: fixedNow, Ready: []Task{task}})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Selected) != 1 || decision.Selected[0].Priority == nil {
		t.Fatalf("unexpected selection %+v", decision.Selected)
	}
	if got := *decision.Selected[0].Priority; got != 35 {
		t.Fatalf("missing priority used %d, want default 35", got)
	}
}

func TestLeastSlackWinsAtEqualPriority(t *testing.T) {
	engine := testEngine(t, nil)
	near := fixedNow.Add(45 * time.Second)
	far := fixedNow.Add(10 * time.Minute)
	snapshot := Snapshot{
		At: fixedNow,
		Ready: []Task{
			withDeadline(testTask("far", fixedNow, 50), far),
			withDeadline(testTask("near", fixedNow, 50), near),
		},
	}

	decision, err := engine.Decide(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "near")
}

func TestAgingPreventsPermanentFIFOStarvation(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.Weights = Weights{Age: 1}
	})
	snapshot := Snapshot{
		At: fixedNow,
		Ready: []Task{
			testTask("new", fixedNow.Add(-time.Second), 0),
			testTask("old", fixedNow.Add(-9*time.Minute), 0),
		},
	}

	decision, err := engine.Decide(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "old")
}

func TestGPUReservationAllowsCPUWork(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.MaxConcurrent = 2
		config.MaxGPUConcurrent = 1
	})
	gpu := testTask("gpu-ready", fixedNow, 80)
	gpu.RequiresGPU = true
	cpu := testTask("cpu-ready", fixedNow, 40)
	running := testTask("gpu-running", fixedNow.Add(-time.Minute), 90)
	running.RequiresGPU = true

	decision, err := engine.Decide(Snapshot{
		At:      fixedNow,
		Ready:   []Task{gpu, cpu},
		Running: []Task{running},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "cpu-ready")
	candidate, err := decision.Candidate("gpu-ready")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Reason != ReasonGPUCapacity {
		t.Fatalf("expected GPU capacity deferral, got %q", candidate.Reason)
	}
}

func TestGPUResourceInfersGPURequirement(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.MaxConcurrent = 2
		config.MaxGPUConcurrent = 1
	})
	first := testTask("first-gpu", fixedNow, 90)
	first.Resources.GPUs = 1
	second := testTask("second-gpu", fixedNow, 80)
	second.Resources.GPUs = 1

	decision, err := engine.Decide(Snapshot{At: fixedNow, Ready: []Task{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "first-gpu")
	if !decision.Selected[0].RequiresGPU {
		t.Fatal("selected task did not normalize resources.gpus into requiresGpu")
	}
	candidate, err := decision.Candidate("second-gpu")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Reason != ReasonGPUCapacity {
		t.Fatalf("second GPU task reason: got %q, want %q", candidate.Reason, ReasonGPUCapacity)
	}
}

func TestReadyTaskAlreadyRunningIsNotSelectedAgain(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.MaxConcurrent = 2
	})
	duplicate := testTask("already-running", fixedNow, 100)
	other := testTask("other", fixedNow, 10)

	decision, err := engine.Decide(Snapshot{
		At:      fixedNow,
		Ready:   []Task{duplicate, other},
		Running: []Task{duplicate},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "other")
	candidate, err := decision.Candidate(duplicate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Outcome != OutcomeDeferred || candidate.Reason != ReasonAlreadyRunning {
		t.Fatalf(
			"already-running task got outcome=%q reason=%q",
			candidate.Outcome,
			candidate.Reason,
		)
	}
}

func TestResourceFitSkipsOversizedTask(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.EnforceResourceFit = true
	})
	oversized := testTask("oversized", fixedNow, 100)
	oversized.Resources = Resources{CPUMilli: 2000, MemoryBytes: 1024}
	fitting := testTask("fitting", fixedNow, 10)
	fitting.Resources = Resources{CPUMilli: 500, MemoryBytes: 512}
	available := Resources{CPUMilli: 1000, MemoryBytes: 1024}

	decision, err := engine.Decide(Snapshot{
		At:        fixedNow,
		Ready:     []Task{oversized, fitting},
		Available: &available,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "fitting")
	candidate, _ := decision.Candidate("oversized")
	if candidate.Reason != ReasonResourceCapacity {
		t.Fatalf("expected resource deferral, got %q", candidate.Reason)
	}
}

func TestReliabilityThresholdDefersRiskyTask(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.MinimumSuccessRate = 0.8
	})
	riskyRate := 0.5
	risky := testTask("risky", fixedNow, 100)
	risky.PredictedSuccessRate = &riskyRate
	safe := testTask("safe", fixedNow, 10)

	decision, err := engine.Decide(Snapshot{At: fixedNow, Ready: []Task{risky, safe}})
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "safe")
	candidate, _ := decision.Candidate("risky")
	if candidate.Reason != ReasonReliabilityThreshold {
		t.Fatalf("expected reliability deferral, got %q", candidate.Reason)
	}
}

func TestDecisionIsIndependentFromInputPermutation(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.MaxConcurrent = 2
	})
	tasks := []Task{
		testTask("c", fixedNow.Add(-time.Minute), 30),
		testTask("a", fixedNow.Add(-time.Minute), 30),
		testTask("b", fixedNow.Add(-time.Minute), 30),
	}
	first, err := engine.Decide(Snapshot{At: fixedNow, Ready: tasks})
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Decide(Snapshot{At: fixedNow, Ready: []Task{tasks[2], tasks[0], tasks[1]}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("decisions differ by input order:\n%+v\n%+v", first, second)
	}
}

func TestDecisionDoesNotAliasInputPointers(t *testing.T) {
	engine := testEngine(t, nil)
	priority := 70
	deadline := fixedNow.Add(time.Minute)
	successRate := 0.8
	task := testTask("independent", fixedNow, priority)
	task.DeadlineAt = &deadline
	task.PredictedSuccessRate = &successRate
	wantDeadline := deadline
	wantSuccessRate := successRate

	decision, err := engine.Decide(Snapshot{At: fixedNow, Ready: []Task{task}})
	if err != nil {
		t.Fatal(err)
	}
	selected := decision.Selected[0]
	if selected.Priority == task.Priority ||
		selected.DeadlineAt == task.DeadlineAt ||
		selected.PredictedSuccessRate == task.PredictedSuccessRate {
		t.Fatal("decision retained a pointer owned by the input snapshot")
	}

	*task.Priority = 1
	*task.DeadlineAt = time.Time{}
	*task.PredictedSuccessRate = 0
	if *selected.Priority != priority {
		t.Fatalf("input mutation changed selected priority to %d", *selected.Priority)
	}
	if !selected.DeadlineAt.Equal(wantDeadline) {
		t.Fatalf("input mutation changed selected deadline to %s", selected.DeadlineAt)
	}
	if *selected.PredictedSuccessRate != wantSuccessRate {
		t.Fatalf("input mutation changed selected success rate to %f", *selected.PredictedSuccessRate)
	}
}

func TestImpossibleDeadlineIsVisibleAndSelectedUrgently(t *testing.T) {
	engine := testEngine(t, nil)
	deadline := fixedNow.Add(5 * time.Second)
	task := withDeadline(testTask("late", fixedNow, 50), deadline)
	task.EstimatedRuntime = NewDuration(20 * time.Second)

	decision, err := engine.Decide(Snapshot{At: fixedNow, Ready: []Task{task}})
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "late")
	candidate, _ := decision.Candidate("late")
	if !candidate.PredictedDeadlineMiss {
		t.Fatal("expected predicted deadline miss")
	}
	if candidate.SlackSeconds == nil || *candidate.SlackSeconds >= 0 {
		t.Fatalf("expected negative slack, got %v", candidate.SlackSeconds)
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	_, err := LoadConfig(strings.NewReader(`
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
unknownField: true
`))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestLoadConfigRequiresSchemaHeader(t *testing.T) {
	_, err := LoadConfig(strings.NewReader(`{}`))
	if err == nil {
		t.Fatal("expected missing apiVersion and kind error")
	}
}

func TestConfigRejectsUnboundedConcurrencyAndWeightOverflow(t *testing.T) {
	config := DefaultConfig()
	config.MaxConcurrent = MaxConcurrentCap + 1
	if err := config.Validate(); err == nil {
		t.Fatal("expected maxConcurrent cap error")
	}

	config = DefaultConfig()
	config.Weights = Weights{
		Priority:    1e308,
		Slack:       1e308,
		Age:         1e308,
		Reliability: 1e308,
	}
	if err := config.Validate(); err == nil {
		t.Fatal("expected non-finite weight sum error")
	}
}

func TestLargeFiniteWeightsStillProduceJSONScore(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.Weights = Weights{
			Priority:    1e307,
			Slack:       1e307,
			Age:         1e307,
			Reliability: 1e307,
		}
	})
	decision, err := engine.Decide(Snapshot{
		At:    fixedNow,
		Ready: []Task{testTask("candidate", fixedNow, 50)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(decision); err != nil {
		t.Fatalf("decision contains a non-JSON score: %v", err)
	}
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	config, err := LoadConfig(strings.NewReader(`
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
maxConcurrent: 3
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.MaxConcurrent != 3 {
		t.Fatalf("expected maxConcurrent=3, got %d", config.MaxConcurrent)
	}
	if config.DefaultEstimatedRuntime.Value() != 30*time.Second {
		t.Fatalf("unexpected default runtime %s", config.DefaultEstimatedRuntime)
	}
}

func TestLoadConfigRejectsMultipleDocuments(t *testing.T) {
	_, err := LoadConfig(strings.NewReader(`
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
---
apiVersion: scheduling.sagecontinuum.org/v1alpha1
kind: ResilientUrgentPolicy
`))
	if err == nil {
		t.Fatal("expected multiple-document error")
	}
}

func TestEngineRejectsMissingCapacityWhenResourceFitIsEnabled(t *testing.T) {
	engine := testEngine(t, func(config *Config) {
		config.EnforceResourceFit = true
	})
	_, err := engine.Decide(Snapshot{
		At:    fixedNow,
		Ready: []Task{testTask("candidate", fixedNow, 10)},
	})
	if err == nil {
		t.Fatal("expected missing resource capacity error")
	}
}

func TestInvalidTaskDoesNotBlockValidTask(t *testing.T) {
	engine := testEngine(t, nil)
	invalid := testTask("invalid", fixedNow, 101)
	valid := testTask("valid", fixedNow, 10)

	decision, err := engine.Decide(Snapshot{At: fixedNow, Ready: []Task{invalid, valid}})
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, decision, "valid")
	candidate, err := decision.Candidate("invalid")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Outcome != OutcomeRejected || candidate.Reason != ReasonInvalidTask {
		t.Fatalf("expected invalid task rejection, got outcome=%q reason=%q", candidate.Outcome, candidate.Reason)
	}
}

func testEngine(t *testing.T, mutate func(*Config)) *Engine {
	t.Helper()
	config := DefaultConfig()
	if mutate != nil {
		mutate(&config)
	}
	engine, err := NewEngine(config)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func testTask(id string, enqueuedAt time.Time, priority int) Task {
	return Task{
		ID:               id,
		EnqueuedAt:       enqueuedAt,
		Priority:         NewPriority(priority),
		EstimatedRuntime: NewDuration(10 * time.Second),
		MaxQueueLatency:  NewDuration(5 * time.Minute),
	}
}

func withDeadline(task Task, deadline time.Time) Task {
	task.DeadlineAt = &deadline
	return task
}

func assertSelected(t *testing.T, decision Decision, expected ...string) {
	t.Helper()
	if actual := decision.SelectedIDs(); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("expected selected %v, got %v", expected, actual)
	}
}
