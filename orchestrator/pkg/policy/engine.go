package policy

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	OutcomeSelected = "selected"
	OutcomeDeferred = "deferred"
	OutcomeRejected = "rejected"

	ReasonSelected             = "selected"
	ReasonAlreadyRunning       = "already_running"
	ReasonConcurrencyLimit     = "concurrency_limit"
	ReasonGPUCapacity          = "gpu_capacity"
	ReasonResourceCapacity     = "resource_capacity"
	ReasonReliabilityThreshold = "reliability_threshold"
	ReasonInvalidTask          = "invalid_task"
)

type Engine struct {
	config Config
}

func NewEngine(config Config) (*Engine, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Engine{config: config}, nil
}

func (e *Engine) Config() Config {
	return e.config
}

type rankedCandidate struct {
	task                  Task
	deadline              time.Time
	hasDeadline           bool
	score                 Score
	slackSeconds          *float64
	predictedDeadlineMiss bool
	outcome               string
	reason                string
}

func (e *Engine) Decide(snapshot Snapshot) (Decision, error) {
	if snapshot.At.IsZero() {
		return Decision{}, errors.New("snapshot time is required")
	}
	if e.config.EnforceResourceFit && snapshot.Available == nil {
		return Decision{}, errors.New("available resources are required when resource fitting is enabled")
	}
	if snapshot.Available != nil && !snapshot.Available.Valid() {
		return Decision{}, errors.New("available resources cannot be negative")
	}

	decision := Decision{At: snapshot.At, Selected: []Task{}, Candidates: []CandidateDecision{}}
	ranked := make([]rankedCandidate, 0, len(snapshot.Ready))
	seen := make(map[string]struct{}, len(snapshot.Ready))
	runningIDs := make(map[string]struct{}, len(snapshot.Running))
	for _, task := range snapshot.Running {
		if task.ID != "" {
			runningIDs[task.ID] = struct{}{}
		}
	}
	for _, rawTask := range snapshot.Ready {
		candidate := e.rank(snapshot.At, rawTask)
		if rawTask.ID == "" {
			candidate.outcome = OutcomeRejected
			candidate.reason = ReasonInvalidTask
		} else if _, exists := seen[rawTask.ID]; exists {
			candidate.outcome = OutcomeRejected
			candidate.reason = ReasonInvalidTask
		} else {
			seen[rawTask.ID] = struct{}{}
			if _, running := runningIDs[rawTask.ID]; running &&
				candidate.outcome != OutcomeRejected {
				candidate.outcome = OutcomeDeferred
				candidate.reason = ReasonAlreadyRunning
			}
		}
		ranked = append(ranked, candidate)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := ranked[i], ranked[j]
		if left.outcome == OutcomeRejected || right.outcome == OutcomeRejected {
			if left.outcome != right.outcome {
				return right.outcome == OutcomeRejected
			}
		}
		if !almostEqual(left.score.Total, right.score.Total) {
			return left.score.Total > right.score.Total
		}
		if left.hasDeadline != right.hasDeadline {
			return left.hasDeadline
		}
		if left.hasDeadline && !left.deadline.Equal(right.deadline) {
			return left.deadline.Before(right.deadline)
		}
		if !left.task.EnqueuedAt.Equal(right.task.EnqueuedAt) {
			return left.task.EnqueuedAt.Before(right.task.EnqueuedAt)
		}
		return left.task.ID < right.task.ID
	})

	slots := e.config.MaxConcurrent - len(snapshot.Running)
	if slots < 0 {
		slots = 0
	}
	runningGPU := 0
	for _, task := range snapshot.Running {
		if requiresGPU(task) {
			runningGPU++
		}
	}
	available := Resources{}
	if snapshot.Available != nil {
		available = *snapshot.Available
	}

	for index := range ranked {
		candidate := &ranked[index]
		if candidate.outcome != "" {
			continue
		}
		successRate := e.config.DefaultSuccessRate
		if candidate.task.PredictedSuccessRate != nil {
			successRate = *candidate.task.PredictedSuccessRate
		}
		if successRate < e.config.MinimumSuccessRate {
			candidate.outcome = OutcomeDeferred
			candidate.reason = ReasonReliabilityThreshold
			continue
		}
		if slots == 0 {
			candidate.outcome = OutcomeDeferred
			candidate.reason = ReasonConcurrencyLimit
			continue
		}
		if requiresGPU(candidate.task) && runningGPU >= e.config.MaxGPUConcurrent {
			candidate.outcome = OutcomeDeferred
			candidate.reason = ReasonGPUCapacity
			continue
		}
		if e.config.EnforceResourceFit && !available.Fits(candidate.task.Resources) {
			candidate.outcome = OutcomeDeferred
			candidate.reason = ReasonResourceCapacity
			continue
		}

		candidate.outcome = OutcomeSelected
		candidate.reason = ReasonSelected
		decision.Selected = append(decision.Selected, candidate.task)
		slots--
		if requiresGPU(candidate.task) {
			runningGPU++
		}
		if e.config.EnforceResourceFit {
			available = available.Subtract(candidate.task.Resources)
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].task.ID < ranked[j].task.ID
	})
	for _, candidate := range ranked {
		decision.Candidates = append(decision.Candidates, CandidateDecision{
			TaskID:                candidate.task.ID,
			Outcome:               candidate.outcome,
			Reason:                candidate.reason,
			Score:                 candidate.score,
			SlackSeconds:          candidate.slackSeconds,
			PredictedDeadlineMiss: candidate.predictedDeadlineMiss,
		})
	}
	return decision, nil
}

func (e *Engine) rank(now time.Time, task Task) rankedCandidate {
	candidate := rankedCandidate{task: cloneTask(task)}
	if task.ID == "" || !task.Resources.Valid() {
		candidate.outcome = OutcomeRejected
		candidate.reason = ReasonInvalidTask
		return candidate
	}
	if task.Resources.GPUs > 0 {
		candidate.task.RequiresGPU = true
	}
	priority := e.config.DefaultPriority
	if task.Priority != nil {
		if *task.Priority < 0 || *task.Priority > 100 {
			candidate.outcome = OutcomeRejected
			candidate.reason = ReasonInvalidTask
			return candidate
		}
		priority = *task.Priority
	}
	candidate.task.Priority = NewPriority(priority)
	if task.EnqueuedAt.IsZero() {
		task.EnqueuedAt = now
		candidate.task.EnqueuedAt = now
	}
	if task.EnqueuedAt.After(now) {
		candidate.outcome = OutcomeRejected
		candidate.reason = ReasonInvalidTask
		return candidate
	}
	estimated := task.EstimatedRuntime.Value()
	if estimated == 0 {
		estimated = e.config.DefaultEstimatedRuntime.Value()
		candidate.task.EstimatedRuntime = e.config.DefaultEstimatedRuntime
	}
	if estimated <= 0 {
		candidate.outcome = OutcomeRejected
		candidate.reason = ReasonInvalidTask
		return candidate
	}
	successRate := e.config.DefaultSuccessRate
	if task.PredictedSuccessRate != nil {
		if !validProbability(*task.PredictedSuccessRate) {
			candidate.outcome = OutcomeRejected
			candidate.reason = ReasonInvalidTask
			return candidate
		}
		successRate = *task.PredictedSuccessRate
	}

	deadline := task.DeadlineAt
	if deadline == nil {
		maxLatency := task.MaxQueueLatency.Value()
		if maxLatency == 0 {
			maxLatency = e.config.DefaultMaxQueueLatency.Value()
			candidate.task.MaxQueueLatency = e.config.DefaultMaxQueueLatency
		}
		if maxLatency <= 0 {
			candidate.outcome = OutcomeRejected
			candidate.reason = ReasonInvalidTask
			return candidate
		}
		calculated := task.EnqueuedAt.Add(maxLatency)
		deadline = &calculated
	}
	candidate.deadline = *deadline
	candidate.hasDeadline = true

	age := now.Sub(task.EnqueuedAt)
	slack := deadline.Sub(now) - estimated
	slackSeconds := slack.Seconds()
	candidate.slackSeconds = &slackSeconds
	candidate.predictedDeadlineMiss = slack <= 0

	priorityScore := float64(priority)
	ageScore := clamp100(100 * age.Seconds() / e.config.AgingHorizon.Value().Seconds())
	slackScore := 100.0
	if slack > 0 {
		slackScore = clamp100(100 * (1 - slack.Seconds()/e.config.SlackHorizon.Value().Seconds()))
	}
	reliabilityScore := 100 * successRate
	totalWeight := e.config.Weights.Priority + e.config.Weights.Slack + e.config.Weights.Age + e.config.Weights.Reliability
	total := priorityScore*(e.config.Weights.Priority/totalWeight) +
		slackScore*(e.config.Weights.Slack/totalWeight) +
		ageScore*(e.config.Weights.Age/totalWeight) +
		reliabilityScore*(e.config.Weights.Reliability/totalWeight)
	candidate.score = Score{
		Priority:    priorityScore,
		Slack:       slackScore,
		Age:         ageScore,
		Reliability: reliabilityScore,
		Total:       total,
	}
	return candidate
}

func cloneTask(task Task) Task {
	result := task
	if task.Priority != nil {
		priority := *task.Priority
		result.Priority = &priority
	}
	if task.DeadlineAt != nil {
		deadline := *task.DeadlineAt
		result.DeadlineAt = &deadline
	}
	if task.PredictedSuccessRate != nil {
		successRate := *task.PredictedSuccessRate
		result.PredictedSuccessRate = &successRate
	}
	return result
}

func clamp100(value float64) float64 {
	return math.Max(0, math.Min(100, value))
}

func almostEqual(left, right float64) bool {
	return math.Abs(left-right) < 1e-9
}

func requiresGPU(task Task) bool {
	return task.RequiresGPU || task.Resources.GPUs > 0
}

func (d Decision) SelectedIDs() []string {
	ids := make([]string, 0, len(d.Selected))
	for _, task := range d.Selected {
		ids = append(ids, task.ID)
	}
	return ids
}

func (d Decision) Candidate(taskID string) (CandidateDecision, error) {
	for _, candidate := range d.Candidates {
		if candidate.TaskID == taskID {
			return candidate, nil
		}
	}
	return CandidateDecision{}, fmt.Errorf("candidate %q not found", taskID)
}
