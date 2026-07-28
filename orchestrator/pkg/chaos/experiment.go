// Package chaos provides deterministic sensitivity experiments for scheduling
// snapshots. It does not inject faults into a live cluster; KWOK is used for
// that separate, opt-in layer.
package chaos

import (
	"fmt"
	"math"

	"github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
)

// Experiment compares one baseline decision with controlled perturbations of
// the same initial scheduling state.
type Experiment struct {
	Baseline      policy.Snapshot `json:"baseline"`
	Perturbations []Perturbation  `json:"perturbations"`
}

// Perturbation changes one task and/or the available capacity. Duration shifts
// may be negative. AvailableDelta is added component by component.
type Perturbation struct {
	Name                  string            `json:"name"`
	TaskID                string            `json:"taskId,omitempty"`
	PriorityDelta         int               `json:"priorityDelta,omitempty"`
	EnqueuedAtShift       policy.Duration   `json:"enqueuedAtShift,omitempty"`
	DeadlineAtShift       policy.Duration   `json:"deadlineAtShift,omitempty"`
	EstimatedRuntimeShift policy.Duration   `json:"estimatedRuntimeShift,omitempty"`
	MaxQueueLatencyShift  policy.Duration   `json:"maxQueueLatencyShift,omitempty"`
	SuccessRateDelta      float64           `json:"successRateDelta,omitempty"`
	AvailableDelta        *policy.Resources `json:"availableDelta,omitempty"`
}

type Report struct {
	Baseline policy.Decision `json:"baseline"`
	Runs     []Run           `json:"runs"`
}

type Run struct {
	Name              string          `json:"name"`
	SelectionChanged  bool            `json:"selectionChanged"`
	SelectionDistance float64         `json:"selectionDistance"`
	Decision          policy.Decision `json:"decision"`
}

func RunExperiment(engine *policy.Engine, experiment Experiment) (Report, error) {
	if engine == nil {
		return Report{}, fmt.Errorf("engine is required")
	}
	baseline, err := engine.Decide(cloneSnapshot(experiment.Baseline))
	if err != nil {
		return Report{}, fmt.Errorf("baseline decision: %w", err)
	}
	report := Report{
		Baseline: baseline,
		Runs:     make([]Run, 0, len(experiment.Perturbations)),
	}
	seenNames := make(map[string]struct{}, len(experiment.Perturbations))
	for index, perturbation := range experiment.Perturbations {
		if perturbation.Name == "" {
			return Report{}, fmt.Errorf("perturbation %d: name is required", index)
		}
		if _, exists := seenNames[perturbation.Name]; exists {
			return Report{}, fmt.Errorf("perturbation %q: duplicate name", perturbation.Name)
		}
		seenNames[perturbation.Name] = struct{}{}

		snapshot, err := apply(
			cloneSnapshot(experiment.Baseline),
			perturbation,
			engine.Config(),
		)
		if err != nil {
			return Report{}, fmt.Errorf("perturbation %q: %w", perturbation.Name, err)
		}
		decision, err := engine.Decide(snapshot)
		if err != nil {
			return Report{}, fmt.Errorf("perturbation %q decision: %w", perturbation.Name, err)
		}
		distance := jaccardDistance(baseline.SelectedIDs(), decision.SelectedIDs())
		report.Runs = append(report.Runs, Run{
			Name:              perturbation.Name,
			SelectionChanged:  distance > 0,
			SelectionDistance: distance,
			Decision:          decision,
		})
	}
	return report, nil
}

func apply(
	snapshot policy.Snapshot,
	perturbation Perturbation,
	config policy.Config,
) (policy.Snapshot, error) {
	if math.IsNaN(perturbation.SuccessRateDelta) || math.IsInf(perturbation.SuccessRateDelta, 0) {
		return policy.Snapshot{}, fmt.Errorf("successRateDelta must be finite")
	}
	taskChange := perturbation.PriorityDelta != 0 ||
		perturbation.EnqueuedAtShift.Value() != 0 ||
		perturbation.DeadlineAtShift.Value() != 0 ||
		perturbation.EstimatedRuntimeShift.Value() != 0 ||
		perturbation.MaxQueueLatencyShift.Value() != 0 ||
		perturbation.SuccessRateDelta != 0
	if taskChange && perturbation.TaskID == "" {
		return policy.Snapshot{}, fmt.Errorf("taskId is required for task perturbations")
	}

	matches := 0
	for index := range snapshot.Ready {
		if snapshot.Ready[index].ID != perturbation.TaskID {
			continue
		}
		matches++
		task := &snapshot.Ready[index]
		if perturbation.PriorityDelta != 0 {
			priority := config.DefaultPriority
			if task.Priority != nil {
				priority = *task.Priority
			}
			priority += perturbation.PriorityDelta
			task.Priority = policy.NewPriority(priority)
		}
		if perturbation.EnqueuedAtShift.Value() != 0 {
			enqueuedAt := task.EnqueuedAt
			if enqueuedAt.IsZero() {
				enqueuedAt = snapshot.At
			}
			task.EnqueuedAt = enqueuedAt.Add(perturbation.EnqueuedAtShift.Value())
		}
		if perturbation.EstimatedRuntimeShift.Value() != 0 {
			estimatedRuntime := task.EstimatedRuntime.Value()
			if estimatedRuntime == 0 {
				estimatedRuntime = config.DefaultEstimatedRuntime.Value()
			}
			task.EstimatedRuntime = policy.NewDuration(
				estimatedRuntime + perturbation.EstimatedRuntimeShift.Value(),
			)
		}
		if task.DeadlineAt != nil {
			shifted := task.DeadlineAt.Add(perturbation.DeadlineAtShift.Value())
			task.DeadlineAt = &shifted
		} else if perturbation.DeadlineAtShift.Value() != 0 {
			return policy.Snapshot{}, fmt.Errorf("task %q has no absolute deadline", perturbation.TaskID)
		}
		if perturbation.MaxQueueLatencyShift.Value() != 0 {
			maxQueueLatency := task.MaxQueueLatency.Value()
			if maxQueueLatency == 0 {
				maxQueueLatency = config.DefaultMaxQueueLatency.Value()
			}
			task.MaxQueueLatency = policy.NewDuration(
				maxQueueLatency + perturbation.MaxQueueLatencyShift.Value(),
			)
		}
		if perturbation.SuccessRateDelta != 0 {
			successRate := config.DefaultSuccessRate
			if task.PredictedSuccessRate != nil {
				successRate = *task.PredictedSuccessRate
			}
			successRate += perturbation.SuccessRateDelta
			task.PredictedSuccessRate = &successRate
		}
	}
	if taskChange && matches != 1 {
		return policy.Snapshot{}, fmt.Errorf("taskId %q must match exactly one ready task, matched %d", perturbation.TaskID, matches)
	}

	if perturbation.AvailableDelta != nil {
		if snapshot.Available == nil {
			return policy.Snapshot{}, fmt.Errorf("baseline has no available capacity")
		}
		snapshot.Available.CPUMilli += perturbation.AvailableDelta.CPUMilli
		snapshot.Available.MemoryBytes += perturbation.AvailableDelta.MemoryBytes
		snapshot.Available.GPUs += perturbation.AvailableDelta.GPUs
	}
	return snapshot, nil
}

func cloneSnapshot(source policy.Snapshot) policy.Snapshot {
	result := source
	result.Ready = append([]policy.Task(nil), source.Ready...)
	result.Running = append([]policy.Task(nil), source.Running...)
	for index := range result.Ready {
		result.Ready[index] = cloneTask(result.Ready[index])
	}
	for index := range result.Running {
		result.Running[index] = cloneTask(result.Running[index])
	}
	if source.Available != nil {
		available := *source.Available
		result.Available = &available
	}
	return result
}

func cloneTask(source policy.Task) policy.Task {
	result := source
	if source.Priority != nil {
		priority := *source.Priority
		result.Priority = &priority
	}
	if source.DeadlineAt != nil {
		deadline := *source.DeadlineAt
		result.DeadlineAt = &deadline
	}
	if source.PredictedSuccessRate != nil {
		successRate := *source.PredictedSuccessRate
		result.PredictedSuccessRate = &successRate
	}
	return result
}

func jaccardDistance(left, right []string) float64 {
	leftSet := make(map[string]struct{}, len(left))
	rightSet := make(map[string]struct{}, len(right))
	for _, id := range left {
		leftSet[id] = struct{}{}
	}
	for _, id := range right {
		rightSet[id] = struct{}{}
	}
	if len(leftSet) == 0 && len(rightSet) == 0 {
		return 0
	}
	union := make(map[string]struct{}, len(leftSet)+len(rightSet))
	for id := range leftSet {
		union[id] = struct{}{}
	}
	for id := range rightSet {
		union[id] = struct{}{}
	}
	intersection := 0
	for id := range leftSet {
		if _, exists := rightSet[id]; exists {
			intersection++
		}
	}
	return 1 - float64(intersection)/float64(len(union))
}
