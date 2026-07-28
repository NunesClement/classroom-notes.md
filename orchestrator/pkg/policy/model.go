package policy

import "time"

type Resources struct {
	CPUMilli    int64 `json:"cpuMilli" yaml:"cpuMilli"`
	MemoryBytes int64 `json:"memoryBytes" yaml:"memoryBytes"`
	GPUs        int64 `json:"gpus" yaml:"gpus"`
}

func (r Resources) Valid() bool {
	return r.CPUMilli >= 0 && r.MemoryBytes >= 0 && r.GPUs >= 0
}

func (r Resources) Fits(request Resources) bool {
	return r.CPUMilli >= request.CPUMilli &&
		r.MemoryBytes >= request.MemoryBytes &&
		r.GPUs >= request.GPUs
}

func (r Resources) Subtract(request Resources) Resources {
	return Resources{
		CPUMilli:    r.CPUMilli - request.CPUMilli,
		MemoryBytes: r.MemoryBytes - request.MemoryBytes,
		GPUs:        r.GPUs - request.GPUs,
	}
}

type Task struct {
	ID                   string     `json:"id" yaml:"id"`
	Name                 string     `json:"name,omitempty" yaml:"name,omitempty"`
	GoalID               string     `json:"goalId,omitempty" yaml:"goalId,omitempty"`
	JobID                string     `json:"jobId,omitempty" yaml:"jobId,omitempty"`
	EnqueuedAt           time.Time  `json:"enqueuedAt,omitempty" yaml:"enqueuedAt,omitempty"`
	Priority             *int       `json:"priority,omitempty" yaml:"priority,omitempty"`
	EstimatedRuntime     Duration   `json:"estimatedRuntime,omitempty" yaml:"estimatedRuntime,omitempty"`
	MaxQueueLatency      Duration   `json:"maxQueueLatency,omitempty" yaml:"maxQueueLatency,omitempty"`
	DeadlineAt           *time.Time `json:"deadlineAt,omitempty" yaml:"deadlineAt,omitempty"`
	RequiresGPU          bool       `json:"requiresGpu,omitempty" yaml:"requiresGpu,omitempty"`
	PredictedSuccessRate *float64   `json:"predictedSuccessRate,omitempty" yaml:"predictedSuccessRate,omitempty"`
	Resources            Resources  `json:"resources,omitempty" yaml:"resources,omitempty"`
}

func NewPriority(value int) *int {
	return &value
}

type Snapshot struct {
	At        time.Time  `json:"at" yaml:"at"`
	Ready     []Task     `json:"ready" yaml:"ready"`
	Running   []Task     `json:"running,omitempty" yaml:"running,omitempty"`
	Available *Resources `json:"available,omitempty" yaml:"available,omitempty"`
}

type Score struct {
	Priority    float64 `json:"priority"`
	Slack       float64 `json:"slack"`
	Age         float64 `json:"age"`
	Reliability float64 `json:"reliability"`
	Total       float64 `json:"total"`
}

type CandidateDecision struct {
	TaskID                string   `json:"taskId"`
	Outcome               string   `json:"outcome"`
	Reason                string   `json:"reason"`
	Score                 Score    `json:"score"`
	SlackSeconds          *float64 `json:"slackSeconds,omitempty"`
	PredictedDeadlineMiss bool     `json:"predictedDeadlineMiss,omitempty"`
}

type Decision struct {
	At         time.Time           `json:"at"`
	Selected   []Task              `json:"selected"`
	Candidates []CandidateDecision `json:"candidates"`
}
