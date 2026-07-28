package adapter

import (
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	core "github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/policy"
	"github.com/waggle-sensor/edge-scheduler/pkg/datatype"
	sagelogger "github.com/waggle-sensor/edge-scheduler/pkg/logger"
	sagepolicy "github.com/waggle-sensor/edge-scheduler/pkg/nodescheduler/policy"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
)

var _ sagepolicy.SchedulingPolicy = (*Policy)(nil)

const (
	ReasonSAGEQueueNameCollision = "sage_queue_name_collision"
	ReasonFailOpenFallback       = "fail_open_fallback"
)

type Option func(*Policy)

func WithClock(clock func() time.Time) Option {
	return func(policy *Policy) {
		policy.clock = clock
	}
}

func WithLogger(logger *log.Logger) Option {
	return func(policy *Policy) {
		policy.logger = logger
	}
}

func WithStateRetention(retention time.Duration) Option {
	return func(policy *Policy) {
		policy.stateRetention = retention
	}
}

// Policy adapts the pure decision engine to Waggle's compiled-in
// SchedulingPolicy interface. It never pushes, pops, or reorders either queue.
type Policy struct {
	mu             sync.Mutex
	engine         *core.Engine
	config         core.Config
	clock          func() time.Time
	logger         *log.Logger
	firstSeen      map[string]time.Time
	lastObserved   map[string]time.Time
	stateRetention time.Duration
	lastDecision   core.Decision
}

func New(config core.Config, options ...Option) (*Policy, error) {
	engine, err := core.NewEngine(config)
	if err != nil {
		return nil, err
	}
	result := &Policy{
		engine:         engine,
		config:         config,
		clock:          time.Now,
		logger:         sagelogger.Info,
		firstSeen:      map[string]time.Time{},
		lastObserved:   map[string]time.Time{},
		stateRetention: 24 * time.Hour,
	}
	for _, option := range options {
		option(result)
	}
	if result.clock == nil {
		return nil, fmt.Errorf("clock cannot be nil")
	}
	if result.logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if result.stateRetention <= 0 {
		return nil, fmt.Errorf("state retention must be positive")
	}
	return result, nil
}

func (p *Policy) SelectBestPlugins(
	readyQueue *datatype.Queue,
	scheduledQueue *datatype.Queue,
	available datatype.Resource,
) ([]*datatype.PluginRuntime, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.clock().UTC()
	ready := snapshotQueue(readyQueue)
	running := snapshotQueue(scheduledQueue)
	byID := make(map[string]*datatype.PluginRuntime, len(ready))
	snapshot := core.Snapshot{
		At:      now,
		Ready:   make([]core.Task, 0, len(ready)),
		Running: make([]core.Task, 0, len(running)),
	}
	observed := make(map[string]struct{}, len(ready)+len(running))
	seenReadyNames := make(map[string]struct{}, len(ready))
	seenReadyIDs := make(map[string]struct{}, len(ready))
	nameCollisionIDs := make(map[string]struct{})
	for _, runtime := range running {
		if runtime != nil {
			seenReadyNames[runtime.Plugin.Name] = struct{}{}
		}
	}

	for _, runtime := range ready {
		task, err := p.toTask(now, runtime)
		if err != nil {
			p.logger.Printf("resilient-urgent: invalid hints for %s: %v", runtimeIdentity(runtime), err)
			task = p.invalidTask(now, runtime)
		}
		name := runtime.Plugin.Name
		_, nameSeen := seenReadyNames[name]
		_, idSeen := seenReadyIDs[task.ID]
		if nameSeen && !idSeen {
			// The pinned Waggle Queue.Pop implementation compares only
			// Plugin.Name. Selecting a later homonym would make NodeScheduler
			// remove and execute the wrong runtime, so serialize homonyms.
			task = p.invalidTask(now, runtime)
			nameCollisionIDs[task.ID] = struct{}{}
			p.logger.Printf(
				"resilient-urgent: deferred %s because an earlier ready or scheduled runtime uses plugin name %q",
				task.ID,
				name,
			)
		}
		seenReadyNames[name] = struct{}{}
		seenReadyIDs[task.ID] = struct{}{}
		snapshot.Ready = append(snapshot.Ready, task)
		// Keep the first pointer for an identity. The core rejects later
		// duplicates, so overwriting this entry could return a different
		// PluginRuntime from the one the core actually selected.
		if _, exists := byID[task.ID]; !exists {
			byID[task.ID] = runtime
		}
		observed[task.ID] = struct{}{}
	}
	for _, runtime := range running {
		task, err := p.toTask(now, runtime)
		if err != nil {
			task = p.invalidTask(now, runtime)
			// A malformed active runtime still consumes the logical GPU
			// budget. Treat explicit or malformed GPU declarations
			// conservatively so bad metadata cannot admit a second GPU task.
			task.RequiresGPU = requiresGPU(runtime)
		}
		snapshot.Running = append(snapshot.Running, task)
		observed[task.ID] = struct{}{}
	}
	if p.config.EnforceResourceFit {
		resources, err := availableResources(available, int64(p.config.MaxGPUConcurrent))
		if err != nil {
			if !p.config.FailOpen {
				p.lastDecision = emptyDecision(now)
				p.cleanupState(now, observed)
				return nil, err
			}
			p.logger.Printf("resilient-urgent: resource snapshot unavailable, using safe fallback: %v", err)
			selected, decision := p.fallback(now, ready, running)
			p.lastDecision = decision
			p.cleanupState(now, observed)
			return selected, nil
		}
		snapshot.Available = &resources
	}

	decision, err := p.engine.Decide(snapshot)
	if err != nil {
		if !p.config.FailOpen {
			p.lastDecision = emptyDecision(now)
			p.cleanupState(now, observed)
			return nil, err
		}
		p.logger.Printf("resilient-urgent: decision failed, using safe fallback: %v", err)
		selected, fallbackDecision := p.fallback(now, ready, running)
		p.lastDecision = fallbackDecision
		p.cleanupState(now, observed)
		return selected, nil
	}
	for index := range decision.Candidates {
		if _, collision := nameCollisionIDs[decision.Candidates[index].TaskID]; collision {
			decision.Candidates[index].Outcome = core.OutcomeDeferred
			decision.Candidates[index].Reason = ReasonSAGEQueueNameCollision
		}
	}
	p.lastDecision = decision
	p.cleanupState(now, observed)

	selected := make([]*datatype.PluginRuntime, 0, len(decision.Selected))
	for _, task := range decision.Selected {
		runtime, exists := byID[task.ID]
		if !exists {
			return nil, fmt.Errorf("selected task %q is not present in the Waggle ready queue", task.ID)
		}
		selected = append(selected, runtime)
	}
	p.logger.Printf(
		"resilient-urgent: ready=%d running=%d selected=%v",
		len(ready),
		len(running),
		decision.SelectedIDs(),
	)
	return selected, nil
}

func emptyDecision(now time.Time) core.Decision {
	return core.Decision{
		At:         now,
		Selected:   []core.Task{},
		Candidates: []core.CandidateDecision{},
	}
}

func (p *Policy) LastDecision() core.Decision {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := p.lastDecision
	result.Selected = make([]core.Task, len(p.lastDecision.Selected))
	for index, task := range p.lastDecision.Selected {
		result.Selected[index] = cloneCoreTask(task)
	}
	result.Candidates = make([]core.CandidateDecision, len(p.lastDecision.Candidates))
	for index, candidate := range p.lastDecision.Candidates {
		result.Candidates[index] = candidate
		if candidate.SlackSeconds != nil {
			slack := *candidate.SlackSeconds
			result.Candidates[index].SlackSeconds = &slack
		}
	}
	return result
}

func cloneCoreTask(task core.Task) core.Task {
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

func (p *Policy) toTask(now time.Time, runtime *datatype.PluginRuntime) (core.Task, error) {
	id := runtimeIdentity(runtime)
	p.lastObserved[id] = now
	enqueuedAt, exists := p.firstSeen[id]
	if !exists {
		enqueuedAt = now
		p.firstSeen[id] = now
	} else if enqueuedAt.After(now) {
		// Wall clocks can move backwards after NTP or RTC correction on an
		// edge node. Reset age instead of presenting a future task to the
		// deterministic engine, which would reject it.
		enqueuedAt = now
		p.firstSeen[id] = now
	}

	if runtime == nil {
		return core.Task{}, fmt.Errorf("plugin runtime cannot be nil")
	}
	if runtime.Plugin.PluginSpec == nil {
		return core.Task{}, fmt.Errorf("plugin %q has no plugin spec", runtime.Plugin.Name)
	}
	if err := validateRuntimeMetadata(runtime); err != nil {
		return core.Task{}, err
	}
	hints, err := parseHints(runtime.Plugin.PluginSpec.Env, p.config)
	if err != nil {
		return core.Task{}, err
	}
	resources, err := requestedResources(runtime)
	if err != nil {
		return core.Task{}, err
	}
	return core.Task{
		ID:                   id,
		Name:                 runtime.Plugin.Name,
		GoalID:               runtime.Plugin.GoalID,
		JobID:                runtime.Plugin.JobID,
		EnqueuedAt:           enqueuedAt,
		Priority:             core.NewPriority(hints.priority),
		EstimatedRuntime:     hints.estimatedRuntime,
		MaxQueueLatency:      hints.maxLatency,
		DeadlineAt:           hints.deadlineAt,
		RequiresGPU:          requiresGPU(runtime),
		PredictedSuccessRate: hints.successRate,
		Resources:            resources,
	}, nil
}

func validateRuntimeMetadata(runtime *datatype.PluginRuntime) error {
	if problems := validation.IsDNS1123Label(runtime.Plugin.Name); len(problems) > 0 {
		return fmt.Errorf(
			"plugin name %q is not a Kubernetes DNS label: %s",
			runtime.Plugin.Name,
			strings.Join(problems, "; "),
		)
	}
	labelValues := []struct {
		name  string
		value string
	}{
		{name: "goal ID", value: runtime.Plugin.GoalID},
		{name: "job ID", value: runtime.Plugin.JobID},
		{name: "pod instance", value: runtime.PodInstance},
	}
	for _, label := range labelValues {
		if problems := validation.IsValidLabelValue(label.value); len(problems) > 0 {
			return fmt.Errorf(
				"%s %q is not a Kubernetes label value: %s",
				label.name,
				label.value,
				strings.Join(problems, "; "),
			)
		}
	}
	if runtime.Plugin.JobID != "" {
		podName := runtime.Plugin.Name + "-" + runtime.Plugin.JobID
		if problems := validation.IsDNS1123Subdomain(podName); len(problems) > 0 {
			return fmt.Errorf(
				"derived pod name %q is invalid: %s",
				podName,
				strings.Join(problems, "; "),
			)
		}
	}
	return nil
}

func (p *Policy) invalidTask(now time.Time, runtime *datatype.PluginRuntime) core.Task {
	id := runtimeIdentity(runtime)
	enqueuedAt, exists := p.firstSeen[id]
	if !exists {
		enqueuedAt = now
		p.firstSeen[id] = now
	}
	return core.Task{ID: id, EnqueuedAt: enqueuedAt, Priority: core.NewPriority(-1)}
}

func (p *Policy) fallback(
	now time.Time,
	ready []*datatype.PluginRuntime,
	running []*datatype.PluginRuntime,
) ([]*datatype.PluginRuntime, core.Decision) {
	slots := p.config.MaxConcurrent - len(running)
	if slots < 0 {
		slots = 0
	}
	capacity := slots
	if capacity > len(ready) {
		capacity = len(ready)
	}
	decision := core.Decision{
		At:         now,
		Selected:   []core.Task{},
		Candidates: make([]core.CandidateDecision, 0, len(ready)),
	}
	gpuRunning := 0
	for _, runtime := range running {
		if requiresGPU(runtime) {
			gpuRunning++
		}
	}
	seenIDs := make(map[string]struct{}, len(ready)+len(running))
	blockedNames := make(map[string]struct{}, len(ready)+len(running))
	for _, runtime := range running {
		if runtime != nil {
			seenIDs[runtimeIdentity(runtime)] = struct{}{}
			blockedNames[runtime.Plugin.Name] = struct{}{}
		}
	}
	selected := make([]*datatype.PluginRuntime, 0, capacity)
	for _, runtime := range ready {
		candidate := core.CandidateDecision{}
		if runtime == nil {
			candidate.TaskID = runtimeIdentity(runtime)
			candidate.Outcome = core.OutcomeRejected
			candidate.Reason = core.ReasonInvalidTask
			decision.Candidates = append(decision.Candidates, candidate)
			continue
		}
		id := runtimeIdentity(runtime)
		candidate.TaskID = id
		name := runtime.Plugin.Name
		_, nameBlocked := blockedNames[name]
		_, idSeen := seenIDs[id]
		blockedNames[name] = struct{}{}
		seenIDs[id] = struct{}{}
		if nameBlocked && !idSeen {
			candidate.Outcome = core.OutcomeDeferred
			candidate.Reason = ReasonSAGEQueueNameCollision
			decision.Candidates = append(decision.Candidates, candidate)
			continue
		}
		if idSeen || runtime.Plugin.PluginSpec == nil {
			candidate.Outcome = core.OutcomeRejected
			candidate.Reason = core.ReasonInvalidTask
			decision.Candidates = append(decision.Candidates, candidate)
			continue
		}
		task, err := p.toTask(now, runtime)
		if err != nil {
			p.logger.Printf("resilient-urgent: fallback skipped invalid runtime %s: %v", id, err)
			candidate.Outcome = core.OutcomeRejected
			candidate.Reason = core.ReasonInvalidTask
			decision.Candidates = append(decision.Candidates, candidate)
			continue
		}
		if len(selected) >= slots {
			candidate.Outcome = core.OutcomeDeferred
			candidate.Reason = core.ReasonConcurrencyLimit
			decision.Candidates = append(decision.Candidates, candidate)
			continue
		}
		if requiresGPU(runtime) && gpuRunning >= p.config.MaxGPUConcurrent {
			candidate.Outcome = core.OutcomeDeferred
			candidate.Reason = core.ReasonGPUCapacity
			decision.Candidates = append(decision.Candidates, candidate)
			continue
		}
		selected = append(selected, runtime)
		decision.Selected = append(decision.Selected, task)
		candidate.Outcome = core.OutcomeSelected
		candidate.Reason = ReasonFailOpenFallback
		decision.Candidates = append(decision.Candidates, candidate)
		if requiresGPU(runtime) {
			gpuRunning++
		}
	}
	sort.SliceStable(decision.Candidates, func(left, right int) bool {
		return decision.Candidates[left].TaskID < decision.Candidates[right].TaskID
	})
	return selected, decision
}

func (p *Policy) cleanupState(now time.Time, observed map[string]struct{}) {
	for id, lastSeen := range p.lastObserved {
		if _, ok := observed[id]; ok {
			continue
		}
		if now.Sub(lastSeen) >= p.stateRetention {
			delete(p.lastObserved, id)
			delete(p.firstSeen, id)
		}
	}
}

func snapshotQueue(queue *datatype.Queue) []*datatype.PluginRuntime {
	if queue == nil {
		return nil
	}
	queue.ResetIter()
	length := queue.Length()
	result := make([]*datatype.PluginRuntime, 0, length)
	for len(result) < length && queue.More() {
		runtime := queue.Next()
		if runtime != nil {
			result = append(result, runtime)
		}
	}
	return result
}

func runtimeIdentity(runtime *datatype.PluginRuntime) string {
	if runtime == nil {
		return "invalid/nil"
	}
	parts := []string{
		runtime.Plugin.GoalID,
		runtime.Plugin.JobID,
		runtime.Plugin.Name,
		runtime.PodInstance,
	}
	return strings.Join(parts, "/")
}

func requiresGPU(runtime *datatype.PluginRuntime) bool {
	if runtime == nil || runtime.Plugin.PluginSpec == nil {
		return false
	}
	if runtime.Plugin.PluginSpec.IsGPURequired() {
		return true
	}
	for key, raw := range runtime.Plugin.PluginSpec.Resource {
		if key != "limit.gpu" && key != "nvidia.com/gpu" {
			continue
		}
		quantity, err := resource.ParseQuantity(raw)
		if err != nil {
			// A malformed explicit GPU requirement must not make the
			// fail-open fallback treat a GPU workload as CPU-only.
			return true
		}
		if quantity.Sign() > 0 {
			return true
		}
	}
	return false
}

func requestedResources(runtime *datatype.PluginRuntime) (core.Resources, error) {
	result := core.Resources{}
	if runtime == nil || runtime.Plugin.PluginSpec == nil {
		return result, nil
	}
	values := runtime.Plugin.PluginSpec.Resource
	parsed, err := parseResourceQuantities(values)
	if err != nil {
		return core.Resources{}, err
	}
	if quantity, ok := firstQuantity(parsed, "request.cpu", "limit.cpu"); ok {
		result.CPUMilli, err = quantityMilliValue("requested CPU", quantity)
		if err != nil {
			return core.Resources{}, err
		}
	}
	if quantity, ok := firstQuantity(parsed, "request.memory", "limit.memory"); ok {
		result.MemoryBytes, err = quantityValue("requested memory", quantity)
		if err != nil {
			return core.Resources{}, err
		}
	}
	if quantity, ok := firstQuantity(parsed, "limit.gpu", "nvidia.com/gpu"); ok {
		gpus, exact := quantity.AsInt64()
		if !exact {
			return core.Resources{}, fmt.Errorf("GPU resource quantity %q must be a whole number", quantity.String())
		}
		result.GPUs = gpus
	}
	return result, nil
}

func availableResources(available datatype.Resource, gpus int64) (core.Resources, error) {
	cpu, err := parseNonNegativeQuantity("available CPU", available.CPU)
	if err != nil {
		return core.Resources{}, err
	}
	memory, err := parseNonNegativeQuantity("available memory", available.Memory)
	if err != nil {
		return core.Resources{}, err
	}
	cpuMilli, err := quantityMilliValue("available CPU", cpu)
	if err != nil {
		return core.Resources{}, err
	}
	memoryBytes, err := quantityValue("available memory", memory)
	if err != nil {
		return core.Resources{}, err
	}
	return core.Resources{CPUMilli: cpuMilli, MemoryBytes: memoryBytes, GPUs: gpus}, nil
}

func quantityMilliValue(name string, quantity resource.Quantity) (int64, error) {
	maximum := resource.NewMilliQuantity(math.MaxInt64, resource.DecimalSI)
	if quantity.Cmp(*maximum) > 0 {
		return 0, fmt.Errorf("%s quantity %q exceeds the int64 millicore limit", name, quantity.String())
	}
	return quantity.MilliValue(), nil
}

func quantityValue(name string, quantity resource.Quantity) (int64, error) {
	maximum := resource.NewQuantity(math.MaxInt64, resource.DecimalSI)
	if quantity.Cmp(*maximum) > 0 {
		return 0, fmt.Errorf("%s quantity %q exceeds the int64 limit", name, quantity.String())
	}
	return quantity.Value(), nil
}

func parseResourceQuantities(values map[string]string) (map[string]resource.Quantity, error) {
	knownSAGEKeys := map[string]struct{}{
		"request.cpu":    {},
		"limit.cpu":      {},
		"request.memory": {},
		"limit.memory":   {},
		"limit.gpu":      {},
		"nvidia.com/gpu": {},
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parsed := make(map[string]resource.Quantity, len(values))
	for _, key := range keys {
		if problems := validation.IsQualifiedName(key); len(problems) > 0 {
			return nil, fmt.Errorf("invalid resource name %q: %s", key, strings.Join(problems, "; "))
		}
		if _, known := knownSAGEKeys[key]; !known && !strings.Contains(key, "/") {
			return nil, fmt.Errorf(
				"unknown SAGE resource %q must be a domain-qualified extended resource",
				key,
			)
		}
		raw := values[key]
		quantity, err := parseNonNegativeQuantity(key, raw)
		if err != nil {
			return nil, err
		}
		if (key == "limit.gpu" || key == "nvidia.com/gpu") && !quantity.IsZero() {
			if _, exact := quantity.AsInt64(); !exact {
				return nil, fmt.Errorf("%s must be a whole number, got %q", key, raw)
			}
		}
		if _, known := knownSAGEKeys[key]; !known && !quantity.IsZero() {
			if _, exact := quantity.AsInt64(); !exact {
				return nil, fmt.Errorf(
					"extended resource %s must be a whole number, got %q",
					key,
					raw,
				)
			}
		}
		parsed[key] = quantity
	}
	if request, hasRequest := parsed["request.cpu"]; hasRequest {
		if limit, hasLimit := parsed["limit.cpu"]; hasLimit && request.Cmp(limit) > 0 {
			return nil, fmt.Errorf("request.cpu cannot exceed limit.cpu")
		}
	}
	if request, hasRequest := parsed["request.memory"]; hasRequest {
		if limit, hasLimit := parsed["limit.memory"]; hasLimit && request.Cmp(limit) > 0 {
			return nil, fmt.Errorf("request.memory cannot exceed limit.memory")
		}
	}
	legacyGPU, hasLegacyGPU := parsed["limit.gpu"]
	extendedGPU, hasExtendedGPU := parsed["nvidia.com/gpu"]
	if hasLegacyGPU && hasExtendedGPU && legacyGPU.Cmp(extendedGPU) != 0 {
		return nil, fmt.Errorf(
			"limit.gpu and nvidia.com/gpu must match when both are declared",
		)
	}
	return parsed, nil
}

func parseNonNegativeQuantity(name, raw string) (resource.Quantity, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return resource.Quantity{}, fmt.Errorf("%s quantity cannot be empty", name)
	}
	quantity, err := resource.ParseQuantity(raw)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("parse %s quantity %q: %w", name, raw, err)
	}
	if quantity.Sign() < 0 {
		return resource.Quantity{}, fmt.Errorf("%s quantity cannot be negative", name)
	}
	return quantity, nil
}

func firstQuantity(values map[string]resource.Quantity, keys ...string) (resource.Quantity, bool) {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value, true
		}
	}
	return resource.Quantity{}, false
}
