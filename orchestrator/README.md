# SAGE Resilient Urgent Scheduler

[Version française](README-fr.md)

Canonical repository:
[github.com/NunesClement/sage-resilient-urgent-scheduler](https://github.com/NunesClement/sage-resilient-urgent-scheduler)

This repository contains an initial explainable scheduling MVP for SAGE/Waggle nodes. It ranks urgent applications, applies simple capacity limits, and provides an adapter that compiles against the SAGE `NodeScheduler`.

The policy core is application-agnostic: adapters provide generic task and
capacity data, while application-specific capture, inference, and transport
stay outside the scheduler. The summer-camp project plugs this core into
Mortimus through the SAGE adapter; other applications and controllers can use
the same engine through their own adapters.

The MVP has not been deployed or connected to a SAGE node. The engine can be used offline with `policyctl`, while the `integrations/sage` module prepares the actual integration without embedding a copy of the SAGE repository.

### What the Orchestrator Will Do

1. Read a validated policy and a snapshot of ready and active tasks.

2. Score each task based on priority, deadline slack, age, and predicted reliability.

3. Favor urgent tasks while aging older tasks to limit starvation.

4. Defer tasks that exceed concurrency, GPU, reliability, or—when the information is trustworthy—resource limits.

5. Return a deterministic, explained decision without directly modifying the SAGE queues.

6. Use a bounded queue-order fallback if the engine fails and `failOpen` mode is enabled.

7. Let the existing SAGE `NodeScheduler` create and track Pods after selection.

8. Optionally coordinate a primary camera and an additional HaLow (or other)
   image source, then deliver a validated two-image pair to the selected AI
   application.

---

### Development Steps

1. Implement and test the decision engine independently of SAGE and Kubernetes.

2. Provide strict YAML configuration and an offline tool for replaying scenarios.

3. Adapt the `edge-scheduler` queues and types to the engine interface.

4. Provide a `waggle-nodescheduler` binary that injects the new policy into the existing SAGE constructor.

5. Validate the configuration offline without contacting Kubernetes, RabbitMQ, or a SAGE node.

6. Confirm the WES/k3s version and expected deployment method with the SAGE team.

7. Test on KWOK, then k3s, then in shadow and canary modes on SAGE hardware.

---

### What the MVP Actually Does

- Ranks tasks by priority, slack, age, and probability of success.
- Flags deadlines that are likely impossible to meet.
- Limits both the total number of selected tasks and the number of GPU tasks.
- Can filter by CPU, memory, and GPU when the supplied capacity is real.
- Rejects invalid metadata and explains every selection or deferral.
- Provides a bounded fallback for internal errors.
- Ignores workload-provided hints by default; using them requires explicit
  activation in a trusted pilot.
- Compiles against the `SchedulingPolicy` interface from the inspected SAGE commit.
- Provides an opt-in paired-camera core with a versioned HaLow request/reply
  seam, capture-time GPS/surveyed position and PTZ metadata, and verified chunk
  transfer; existing scheduling behavior is unchanged.
- Provides an optional `intentctl` gateway that asks the existing Hermes/GLM
  service to translate natural language into a small, reviewable science-goal
  draft using existing SAGE terms.

### What the MVP Does Not Do Yet

- It does not preempt an already active Pod.
- It does not retry tasks or manage a retry budget.
- It does not create checkpoints, replicas, or migrations.
- It does not automatically launch an alternative application: the current "fallback" is a decision fallback, not a fallback application.
- It does not persist task age across restarts.
- It does not yet use sensors, energy, temperature, or a learned prediction.
- It does not replace the real-time guarantees that Kubernetes lacks.
- It does not yet provide multi-tenant authorization for urgency levels.
- An intent draft is not executable: the translator does not choose plugins,
  assign priority, submit SAGE jobs, or bypass human approval.
- It does not provide a concrete MQTT/Meshtastic transport, physical camera,
  GPS, or PTZ driver, or AI adapter. Those are deployment-specific
  implementations of the core interfaces.
- The pinned Waggle commit removes queue entries by plugin name only.
  The adapter serializes same-named entries along the selection path, but an
  upstream fix using full identity is still required before a canary.
- The pinned Waggle queue does not provide an atomic snapshot: its REST API can
  add a task while the policy is iterating. An upstream locked snapshot method
  or channel-serialized submission is also required before a canary.
- The local `POST /api/v1/schedule` endpoint in this Waggle revision does not
  fully prepare the SAGE runtime. It must remain inaccessible and unused until
  it is fixed upstream; the normal goals/science-rules path remains the one
  targeted by the MVP.

## Answers to the Three Questions

### 1. How Can SAGE Be Driven with Resilience and Urgency?

SAGE remains the orchestrator; only its selection policy is replaced. Each application can provide a priority, estimated duration, deadline or maximum latency, and optionally a probability of success. These declarations are accepted only in a trusted pilot; a multi-user environment will need to authorize and bound them in the cloud. The engine then ranks the applications and returns only as many as the configured limits allow.

This first step provides explainable admission and ordering. Full resilience will later require additional actions in the SAGE controller: retries, checkpointing, replication, bounded preemption, and state persistence.

### 2. How Can Chaos Be Used to Study Policies?

`policyctl` can replay the exact same snapshot with controlled variations in load, GPU capacity, deadline, estimated duration, or reliability. `chaoslab` measures the sensitivity of selections, including with their Jaccard distance, while KWOK can inject Kubernetes failures and transitions at scale.

This repository does not claim to inject those failures yet. It provides the deterministic engine and detailed decisions needed for a future reproducible experimentation environment.

### 3. How Can an Autonomous Edge Agent Be Implemented and Validated?

The scheduling agent starts here as a deterministic local loop: observe the
queues, decide, explain, and then let SAGE apply the selection. It calls no AI
service and receives no additional Kubernetes permissions. Separately, the
opt-in paired-camera core calls only an injected `PairAnalyzer`; this repository
contains no AI service implementation.

Validation should progress through unit tests and fuzzing, offline replay, KWOK, k3s with controlled failures, shadow mode on SAGE, and then a canary on a few nodes. Destructive or autonomous actions will remain behind deterministic safeguards.

## Offline Usage

Validate only the configuration:

```bash
go run ./cmd/policyctl \
  -config config/policy.example.yaml \
  -validate-config
```

Calculate a decision from a JSON snapshot:

```bash
go run ./cmd/policyctl \
  -config config/policy.example.yaml \
  -snapshot examples/snapshots/urgent-vs-routine.json
```

Measure sensitivity to small perturbations:

```bash
go run ./cmd/chaoslab \
  -config config/policy.example.yaml \
  -experiment examples/chaos/sensitivity.json
```

Validate the SAGE binary without establishing a connection:

```bash
go run ./integrations/sage/cmd/waggle-nodescheduler \
  -policy-config config/policy.example.yaml \
  -validate-config
```

The last command initializes the default Waggle runtime configuration, loads
the policy, and then exits before `scheduler.Configure()`: it contacts neither
k3s, RabbitMQ, nor the WES services.

The example configuration leaves `trustWorkloadEnvHints` set to `false`. An
operator can enable it for a closed pilot after verifying who can submit hints;
in a shared environment, SAGE admission must authorize them according to the
requester's identity.

## Optional Intent Translation

`intentctl` uses the existing internal Hermes service through an
OpenAI-compatible chat-completions endpoint. GLM 5.2 is the default model; a
separate model deployment is not required for this first version.

```bash
export HERMES_CHAT_COMPLETIONS_URL=http://hermes.internal/v1/chat/completions
export HERMES_API_KEY=replace-if-required

go run ./cmd/intentctl -input examples/intents/cloud-cover.txt
```

The result is a small JSON draft containing `goal`, `applications`, `nodes`,
`nodeTags`, `scienceRules`, `successCriteria`, and any open `questions`. It has
`humanApprovalRequired: true` and cannot be passed directly to the scheduler.
If Hermes does not implement OpenAI JSON response mode, add `-json-mode=false`.
See [docs/intent-translation.md](docs/intent-translation.md) for the mapping.

## Structure

- `pkg/policy`: deterministic engine and independent types.
- `pkg/orchestration`: optional primary-plus-additional camera coordination and
  the generic AI boundary.
- `pkg/intent`: the small science-goal draft and Hermes translation boundary.
- `cmd/policyctl`: offline validation and replay.
- `cmd/chaoslab`: offline analysis of decision sensitivity.
- `cmd/intentctl`: optional natural-language-to-science-goal-draft command.
- `integrations/sage/adapter`: conversion from Waggle queues to the engine.
- `integrations/sage/cmd/waggle-nodescheduler`: SAGE binary with the policy injected.
- `integrations/sage/deploy`: prepared, unapplied manifests and restart procedure.
- `docs/architecture.md`: decision calculation, limitations, and validation path.
- `docs/halow-orchestration.md`: additive two-image flow and HaLow protocol seam.
- `docs/intent-translation.md`: intent semantics, model boundary, and usage.
- `docs/sage-integration.md`: exact SAGE contract and deployment question.

The code remains tested with Go 1.20 for compatibility with the SAGE
dependency. The image is built with Go 1.26.5 so it includes a maintained
standard library.

## Sources and Research Leads

1. [Waggle edge-scheduler](https://github.com/waggle-sensor/edge-scheduler),
   its [NodeScheduler policies](https://github.com/waggle-sensor/edge-scheduler/tree/main/pkg/nodescheduler/policy),
   and the [`policy` argument](https://github.com/waggle-sensor/edge-scheduler/blob/main/cmd/nodescheduler/main.go#L46).
   Waggle policies are written in Go and compiled into the scheduler binary.
   Logic written in Python or another language would need a compiled bridge.

2. [Kubernetes Scheduler](https://kubernetes.io/docs/concepts/scheduling-eviction/kube-scheduler/)
   and [Kubernetes scheduler-plugins](https://github.com/kubernetes-sigs/scheduler-plugins).
   The Waggle `NodeScheduler` creates Pods whose final placement is handled by
   the cluster scheduler. A custom scheduler or Scheduling Framework plugin is
   another possible implementation layer, subject to exact k3s/Kubernetes
   version compatibility.

3. [KWOK](https://kwok.sigs.k8s.io/) can simulate Kubernetes nodes and Pods for
   scheduler testing at control-plane scale. It complements, but does not
   replace, tests with real containers, GPUs, sensors, and SAGE services.

4. [Michael Wooldridge's Google Scholar profile](https://scholar.google.com/citations?user=JD8v9fkAAAAJ&hl=en&oi=sra)
   is a research lead for predictive task scheduling and related multi-agent
   work. No particular real-time predictive scheduling paper from this profile
   has been identified for the project yet.

## License

Code specific to this repository is provided under the Apache-2.0 license. The `waggle-sensor/edge-scheduler` dependency requires a separate review before any binary distribution; see [NOTICE](NOTICE).
