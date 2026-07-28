package adapter

import (
	"io"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	core "github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
	"github.com/waggle-sensor/edge-scheduler/pkg/datatype"
)

var adapterTestNow = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

func TestLegacyPluginWithoutHintsUsesDefaults(t *testing.T) {
	config := adapterTestConfig()
	runtime := testRuntime("legacy", "instance-a", nil)
	ready := queueOf(runtime)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, runtime)

	decision := policy.LastDecision()
	if len(decision.Selected) != 1 {
		t.Fatalf("selected %d tasks, want 1", len(decision.Selected))
	}
	task := decision.Selected[0]
	if task.Priority == nil || *task.Priority != config.DefaultPriority {
		t.Fatalf("priority: got %v, want default %d", task.Priority, config.DefaultPriority)
	}
	if task.EstimatedRuntime != config.DefaultEstimatedRuntime {
		t.Fatalf("runtime: got %s, want default %s", task.EstimatedRuntime, config.DefaultEstimatedRuntime)
	}
	if task.MaxQueueLatency != config.DefaultMaxQueueLatency {
		t.Fatalf("latency: got %s, want default %s", task.MaxQueueLatency, config.DefaultMaxQueueLatency)
	}
}

func TestPriorityHintControlsSelection(t *testing.T) {
	config := adapterTestConfig()
	config.Weights = core.Weights{Priority: 1}
	low := testRuntime("routine", "instance-a", map[string]string{HintPriority: "10"})
	high := testRuntime("urgent", "instance-b", map[string]string{HintPriority: "95"})
	ready := queueOf(low, high)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, high)
}

func TestMaxLatencyDeadlineUsesStableFirstSeenTime(t *testing.T) {
	config := adapterTestConfig()
	config.Weights = core.Weights{Slack: 1}
	current := adapterTestNow
	runtime := testRuntime("urgent", "instance-a", map[string]string{
		HintMaxLatency:       "1m",
		HintEstimatedRuntime: "10s",
	})
	ready := queueOf(runtime)
	policy := newTestPolicy(t, config, func() time.Time { return current })

	if _, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{}); err != nil {
		t.Fatal(err)
	}
	firstSlack := candidateSlack(t, policy.LastDecision(), runtimeIdentity(runtime))
	if firstSlack != 50 {
		t.Fatalf("initial slack: got %.0fs, want 50s", firstSlack)
	}

	current = current.Add(20 * time.Second)
	if _, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{}); err != nil {
		t.Fatal(err)
	}
	secondSlack := candidateSlack(t, policy.LastDecision(), runtimeIdentity(runtime))
	if secondSlack != 30 {
		t.Fatalf("second slack: got %.0fs, want 30s", secondSlack)
	}
}

func TestBackwardClockCorrectionDoesNotRejectQueuedTask(t *testing.T) {
	config := adapterTestConfig()
	current := adapterTestNow
	runtime := testRuntime("clock-safe", "instance-a", nil)
	ready := queueOf(runtime)
	policy := newTestPolicy(t, config, func() time.Time { return current })

	if _, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{}); err != nil {
		t.Fatal(err)
	}
	current = current.Add(-time.Minute)
	selected, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, runtime)
	if !policy.LastDecision().Selected[0].EnqueuedAt.Equal(current) {
		t.Fatalf(
			"enqueue time was not reset after clock correction: got %s, want %s",
			policy.LastDecision().Selected[0].EnqueuedAt,
			current,
		)
	}
}

func TestAbsoluteDeadlineHintOverridesRelativeDeadline(t *testing.T) {
	config := adapterTestConfig()
	deadline := adapterTestNow.Add(25 * time.Second)
	runtime := testRuntime("urgent", "instance-a", map[string]string{
		HintMaxLatency:       "10m",
		HintEstimatedRuntime: "10s",
		HintDeadlineAt:       deadline.Format(time.RFC3339),
	})
	ready := queueOf(runtime)
	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })

	if _, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{}); err != nil {
		t.Fatal(err)
	}
	slack := candidateSlack(t, policy.LastDecision(), runtimeIdentity(runtime))
	if slack != 15 {
		t.Fatalf("slack: got %.0fs, want 15s from the absolute deadline", slack)
	}
}

func TestRunningGPUDefersReadyGPUButAllowsCPU(t *testing.T) {
	config := adapterTestConfig()
	config.MaxConcurrent = 2
	config.MaxGPUConcurrent = 1
	config.Weights = core.Weights{Priority: 1}

	runningGPU := testRuntime("gpu-running", "instance-running", nil)
	runningGPU.Plugin.PluginSpec.Selector = map[string]string{"resource.gpu": "true"}
	readyGPU := testRuntime("gpu-urgent", "instance-gpu", map[string]string{HintPriority: "100"})
	readyGPU.Plugin.PluginSpec.Selector = map[string]string{"resource.gpu": "true"}
	readyCPU := testRuntime("cpu", "instance-cpu", map[string]string{HintPriority: "10"})
	ready := queueOf(readyGPU, readyCPU)
	running := queueOf(runningGPU)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, running, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, readyCPU)

	candidate, err := policy.LastDecision().Candidate(runtimeIdentity(readyGPU))
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Reason != core.ReasonGPUCapacity {
		t.Fatalf("GPU candidate reason: got %q, want %q", candidate.Reason, core.ReasonGPUCapacity)
	}
}

func TestInvalidRunningGPUStillConsumesGPUBudget(t *testing.T) {
	config := adapterTestConfig()
	config.MaxConcurrent = 2
	config.MaxGPUConcurrent = 1
	config.Weights = core.Weights{Priority: 1}

	runningGPU := testRuntime("gpu-running", "instance-running", map[string]string{
		HintPriority: "invalid",
	})
	runningGPU.Plugin.PluginSpec.Selector = map[string]string{"resource.gpu": "true"}
	readyGPU := testRuntime("gpu-urgent", "instance-gpu", map[string]string{HintPriority: "100"})
	readyGPU.Plugin.PluginSpec.Selector = map[string]string{"resource.gpu": "true"}
	readyCPU := testRuntime("cpu", "instance-cpu", map[string]string{HintPriority: "10"})

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(
		queueOf(readyGPU, readyCPU),
		queueOf(runningGPU),
		datatype.Resource{},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, readyCPU)

	candidate, err := policy.LastDecision().Candidate(runtimeIdentity(readyGPU))
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Reason != core.ReasonGPUCapacity {
		t.Fatalf("GPU candidate reason: got %q, want %q", candidate.Reason, core.ReasonGPUCapacity)
	}
}

func TestReturnsOriginalPointersWithoutChangingQueues(t *testing.T) {
	config := adapterTestConfig()
	config.MaxConcurrent = 2
	first := testRuntime("first", "instance-a", nil)
	second := testRuntime("second", "instance-b", nil)
	runningRuntime := testRuntime("running", "instance-c", nil)
	ready := queueOf(first, second)
	running := queueOf(runningRuntime)
	readyBefore := snapshotQueue(ready)
	runningBefore := snapshotQueue(running)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, running, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, first)

	if got := snapshotQueue(ready); !reflect.DeepEqual(got, readyBefore) {
		t.Fatalf("ready queue changed:\ngot  %p %p\nwant %p %p", got[0], got[1], readyBefore[0], readyBefore[1])
	}
	if got := snapshotQueue(running); !reflect.DeepEqual(got, runningBefore) {
		t.Fatal("scheduled queue changed")
	}
	if ready.Length() != len(readyBefore) || running.Length() != len(runningBefore) {
		t.Fatalf("queue lengths changed: ready=%d running=%d", ready.Length(), running.Length())
	}
}

func TestLastDecisionReturnsIndependentCopy(t *testing.T) {
	config := adapterTestConfig()
	runtime := testRuntime("copy", "instance-a", map[string]string{
		HintPriority:   "80",
		HintDeadlineAt: adapterTestNow.Add(time.Minute).Format(time.RFC3339),
	})
	ready := queueOf(runtime)
	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	if _, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{}); err != nil {
		t.Fatal(err)
	}

	first := policy.LastDecision()
	*first.Selected[0].Priority = 1
	*first.Selected[0].DeadlineAt = time.Time{}
	*first.Candidates[0].SlackSeconds = 12345
	second := policy.LastDecision()
	if second.Selected[0].Priority == nil || *second.Selected[0].Priority != 80 {
		t.Fatalf("caller mutated stored priority: %v", second.Selected[0].Priority)
	}
	if second.Selected[0].DeadlineAt == nil || second.Selected[0].DeadlineAt.IsZero() {
		t.Fatal("caller mutated stored deadline")
	}
	if second.Candidates[0].SlackSeconds == nil || *second.Candidates[0].SlackSeconds == 12345 {
		t.Fatal("caller mutated stored slack")
	}
}

func TestDuplicateIdentityReturnsFirstRuntimePointer(t *testing.T) {
	config := adapterTestConfig()
	first := testRuntime("duplicate", "same-instance", nil)
	second := testRuntime("duplicate", "same-instance", nil)
	ready := queueOf(first, second)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, first)
	if selected[0] == second {
		t.Fatal("adapter returned the later duplicate pointer")
	}
}

func TestSameNameAcrossJobsCannotPopWrongRuntime(t *testing.T) {
	config := adapterTestConfig()
	config.Weights = core.Weights{Priority: 1}
	first := testRuntime("shared-plugin", "instance-a", map[string]string{HintPriority: "10"})
	first.Plugin.GoalID = "goal-first"
	first.Plugin.JobID = "job-first"
	second := testRuntime("shared-plugin", "instance-b", map[string]string{HintPriority: "100"})
	second.Plugin.GoalID = "goal-second"
	second.Plugin.JobID = "job-second"
	ready := queueOf(first, second)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, first)
	if popped := ready.Pop(selected[0]); popped != first {
		t.Fatalf("pinned SAGE Queue.Pop removed %p, want selected runtime %p", popped, first)
	}
	candidate, err := policy.LastDecision().Candidate(runtimeIdentity(second))
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Reason != ReasonSAGEQueueNameCollision {
		t.Fatalf("later homonym reason: got %q, want %q", candidate.Reason, ReasonSAGEQueueNameCollision)
	}
	if candidate.Outcome != core.OutcomeDeferred {
		t.Fatalf("later homonym outcome: got %q, want %q", candidate.Outcome, core.OutcomeDeferred)
	}
}

func TestReadyRuntimeCannotShareNameWithScheduledRuntime(t *testing.T) {
	config := adapterTestConfig()
	config.MaxConcurrent = 2
	runningRuntime := testRuntime("shared-plugin", "instance-running", nil)
	runningRuntime.Plugin.GoalID = "goal-running"
	readyRuntime := testRuntime("shared-plugin", "instance-ready", nil)
	readyRuntime.Plugin.GoalID = "goal-ready"
	ready := queueOf(readyRuntime)
	running := queueOf(runningRuntime)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, running, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected)
	candidate, err := policy.LastDecision().Candidate(runtimeIdentity(readyRuntime))
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Outcome != core.OutcomeDeferred ||
		candidate.Reason != ReasonSAGEQueueNameCollision {
		t.Fatalf("scheduled-name collision got outcome=%q reason=%q", candidate.Outcome, candidate.Reason)
	}
}

func TestInvalidHintRejectsOnlyThatRuntime(t *testing.T) {
	config := adapterTestConfig()
	invalid := testRuntime("invalid", "instance-a", map[string]string{HintPriority: "critical"})
	valid := testRuntime("valid", "instance-b", nil)
	ready := queueOf(invalid, valid)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, valid)
	assertRejectedInvalid(t, policy.LastDecision(), runtimeIdentity(invalid))
}

func TestInvalidRequestedResourceRejectsOnlyThatRuntime(t *testing.T) {
	config := adapterTestConfig()
	invalid := testRuntime("invalid-resource", "instance-a", nil)
	invalid.Plugin.PluginSpec.Resource = map[string]string{"request.cpu": "lots"}
	valid := testRuntime("valid", "instance-b", nil)
	ready := queueOf(invalid, valid)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, valid)
	assertRejectedInvalid(t, policy.LastDecision(), runtimeIdentity(invalid))
}

func TestMissingPluginSpecIsRejected(t *testing.T) {
	config := adapterTestConfig()
	invalid := &datatype.PluginRuntime{
		Plugin: datatype.Plugin{
			Name:   "missing-spec",
			GoalID: "goal",
			JobID:  "job",
		},
		PodInstance: "instance-a",
	}
	valid := testRuntime("valid", "instance-b", nil)
	ready := queueOf(invalid, valid)

	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, valid)
	assertRejectedInvalid(t, policy.LastDecision(), runtimeIdentity(invalid))
}

func TestInvalidKubernetesMetadataRejectsOnlyThatRuntime(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*datatype.PluginRuntime)
	}{
		{
			name: "plugin name",
			mutate: func(runtime *datatype.PluginRuntime) {
				runtime.Plugin.Name = "-invalid"
			},
		},
		{
			name: "goal label",
			mutate: func(runtime *datatype.PluginRuntime) {
				runtime.Plugin.GoalID = strings.Repeat("g", 64)
			},
		},
		{
			name: "derived pod name",
			mutate: func(runtime *datatype.PluginRuntime) {
				runtime.Plugin.JobID = "UPPERCASE"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := adapterTestConfig()
			invalid := testRuntime("invalid-metadata", "instance-a", nil)
			test.mutate(invalid)
			valid := testRuntime("valid", "instance-b", nil)
			ready := queueOf(invalid, valid)

			policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })
			selected, err := policy.SelectBestPlugins(ready, &datatype.Queue{}, datatype.Resource{})
			if err != nil {
				t.Fatal(err)
			}
			assertRuntimePointers(t, selected, valid)
			assertRejectedInvalid(t, policy.LastDecision(), runtimeIdentity(invalid))
		})
	}
}

func TestInvalidAvailableResourceFailsClosed(t *testing.T) {
	config := adapterTestConfig()
	config.EnforceResourceFit = true
	config.FailOpen = false
	ready := queueOf(testRuntime("valid", "instance-a", nil))
	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })

	_, err := policy.SelectBestPlugins(
		ready,
		&datatype.Queue{},
		datatype.Resource{CPU: "invalid", Memory: "1Gi"},
	)
	if err == nil {
		t.Fatal("expected an invalid available resource error")
	}
}

func TestFailClosedErrorClearsPreviousDecision(t *testing.T) {
	config := adapterTestConfig()
	config.EnforceResourceFit = true
	config.FailOpen = false
	current := adapterTestNow
	runtime := testRuntime("valid", "instance-a", nil)
	policy := newTestPolicy(t, config, func() time.Time { return current })

	if _, err := policy.SelectBestPlugins(
		queueOf(runtime),
		&datatype.Queue{},
		datatype.Resource{CPU: "1", Memory: "1Gi"},
	); err != nil {
		t.Fatal(err)
	}
	if len(policy.LastDecision().Selected) != 1 {
		t.Fatal("expected the first decision to select the valid runtime")
	}

	current = current.Add(time.Second)
	if _, err := policy.SelectBestPlugins(
		queueOf(runtime),
		&datatype.Queue{},
		datatype.Resource{CPU: "invalid", Memory: "1Gi"},
	); err == nil {
		t.Fatal("expected an invalid available resource error")
	}
	decision := policy.LastDecision()
	if !decision.At.Equal(current) {
		t.Fatalf("failed decision timestamp: got %s, want %s", decision.At, current)
	}
	if len(decision.Selected) != 0 || len(decision.Candidates) != 0 {
		t.Fatalf("failed decision leaked previous state: %+v", decision)
	}
}

func TestFailOpenFallbackSkipsInvalidRuntimes(t *testing.T) {
	config := adapterTestConfig()
	config.MaxConcurrent = 5
	config.MaxGPUConcurrent = 1
	config.EnforceResourceFit = true
	config.FailOpen = true

	missingSpec := &datatype.PluginRuntime{
		Plugin: datatype.Plugin{
			Name:   "missing-spec",
			GoalID: "goal-missing",
			JobID:  "job-missing",
		},
		PodInstance: "instance-missing",
	}
	invalidHint := testRuntime("invalid-hint", "instance-hint", map[string]string{
		HintPriority: "",
	})
	invalidResource := testRuntime("invalid-resource", "instance-resource", nil)
	invalidResource.Plugin.PluginSpec.Resource = map[string]string{
		"request.memory": "invalid",
	}
	valid := testRuntime("valid", "instance-valid", nil)
	ready := queueOf(nil, missingSpec, invalidHint, invalidResource, valid)
	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })

	selected, err := policy.SelectBestPlugins(
		ready,
		&datatype.Queue{},
		datatype.Resource{CPU: "invalid", Memory: "1Gi"},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected, valid)
	decision := policy.LastDecision()
	if got := decision.SelectedIDs(); !reflect.DeepEqual(got, []string{runtimeIdentity(valid)}) {
		t.Fatalf("fallback decision selected %v, want valid runtime", got)
	}
	candidate, err := decision.Candidate(runtimeIdentity(valid))
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Outcome != core.OutcomeSelected || candidate.Reason != ReasonFailOpenFallback {
		t.Fatalf("fallback candidate got outcome=%q reason=%q", candidate.Outcome, candidate.Reason)
	}
}

func TestFailOpenFallbackDoesNotBypassInvalidEarlierHomonym(t *testing.T) {
	config := adapterTestConfig()
	config.MaxConcurrent = 2
	config.MaxGPUConcurrent = 1
	config.EnforceResourceFit = true
	config.FailOpen = true

	invalidFirst := testRuntime("same", "same-instance", map[string]string{
		HintEstimatedRuntime: "not-a-duration",
	})
	validSecond := testRuntime("same", "same-instance", nil)
	validDuplicate := testRuntime("same", "same-instance", nil)
	ready := queueOf(invalidFirst, validSecond, validDuplicate)
	policy := newTestPolicy(t, config, func() time.Time { return adapterTestNow })

	selected, err := policy.SelectBestPlugins(
		ready,
		&datatype.Queue{},
		datatype.Resource{CPU: "invalid", Memory: "1Gi"},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimePointers(t, selected)
}

func TestNilQueuesAreAccepted(t *testing.T) {
	policy := newTestPolicy(t, adapterTestConfig(), func() time.Time { return adapterTestNow })
	selected, err := policy.SelectBestPlugins(nil, nil, datatype.Resource{})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 {
		t.Fatalf("selected %d runtimes from nil queues", len(selected))
	}
}

func adapterTestConfig() core.Config {
	config := core.DefaultConfig()
	config.DefaultPriority = 25
	config.MaxConcurrent = 1
	config.MaxGPUConcurrent = 1
	config.FailOpen = false
	config.TrustWorkloadEnvHints = true
	return config
}

func newTestPolicy(t *testing.T, config core.Config, clock func() time.Time) *Policy {
	t.Helper()
	policy, err := New(
		config,
		WithClock(clock),
		WithLogger(log.New(io.Discard, "", 0)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func testRuntime(name, podInstance string, env map[string]string) *datatype.PluginRuntime {
	return &datatype.PluginRuntime{
		Plugin: datatype.Plugin{
			Name:   name,
			GoalID: "goal-" + name,
			JobID:  "job-" + name,
			PluginSpec: &datatype.PluginSpec{
				Image: "registry.sagecontinuum.org/example/" + name + ":1.0.0",
				Env:   env,
			},
		},
		PodInstance: podInstance,
	}
}

func queueOf(runtimes ...*datatype.PluginRuntime) *datatype.Queue {
	queue := &datatype.Queue{}
	for _, runtime := range runtimes {
		queue.Push(runtime)
	}
	return queue
}

func assertRuntimePointers(t *testing.T, got []*datatype.PluginRuntime, want ...*datatype.PluginRuntime) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("selected %d runtimes, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("selected[%d]=%p, want original pointer %p", index, got[index], want[index])
		}
	}
}

func candidateSlack(t *testing.T, decision core.Decision, id string) float64 {
	t.Helper()
	candidate, err := decision.Candidate(id)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.SlackSeconds == nil {
		t.Fatalf("candidate %q has no slack", id)
	}
	return *candidate.SlackSeconds
}

func assertRejectedInvalid(t *testing.T, decision core.Decision, id string) {
	t.Helper()
	candidate, err := decision.Candidate(id)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Outcome != core.OutcomeRejected || candidate.Reason != core.ReasonInvalidTask {
		t.Fatalf("candidate %q: got outcome=%q reason=%q, want rejected/invalid_task", id, candidate.Outcome, candidate.Reason)
	}
}
