# SAGE/Waggle Integration

[Version française](sage-integration-fr.md)

## Target Revision

The adapter was prepared from:

- repository: [waggle-sensor/edge-scheduler](https://github.com/waggle-sensor/edge-scheduler);
- inspected commit: [`5391a00b34fa069f14b4ce50153725571007b5ef`](https://github.com/waggle-sensor/edge-scheduler/commit/5391a00b34fa069f14b4ce50153725571007b5ef);
- interface: [`pkg/nodescheduler/policy`](https://github.com/waggle-sensor/edge-scheduler/tree/5391a00b34fa069f14b4ce50153725571007b5ef/pkg/nodescheduler/policy).

The integration module pins this revision. It must be rebuilt and revalidated if the version installed on SAGE differs.

## Why a Compiled Integration?

The `NodeScheduler` selects a policy through its `policy` argument, but the upstream registry knows only the policies compiled into its binary. It cannot dynamically load a Python script.

The MVP therefore provides a second Go entry point that:

1. loads the usual Waggle configuration;
2. loads the `ResilientUrgentPolicy` configuration;
3. builds the upstream components with `NewNodeSchedulerBuilder`;
4. assigns the adapter to the `SchedulingPolicy` field;
5. starts the same `NodeScheduler` when validation mode has not been requested.

The upstream source tree is neither vendored nor modified in this repository; the integration references it as a pinned Go module.

## Offline Validation

From the repository root:

```bash
go run ./integrations/sage/cmd/waggle-nodescheduler \
  -policy-config config/policy.example.yaml \
  -validate-config
```

This command:

- validates the policy configuration;
- verifies that the integration binary builds against the pinned API;
- exits before `scheduler.Configure()`.

It does not contact:

- the k3s API;
- RabbitMQ;
- the science-rule checker;
- the scoreboard;
- the cloud scheduler.

Do not run the binary without `-validate-config` on a machine connected to SAGE until the deployment has been validated with the team.

## Scheduling Metadata

The upstream `PluginRuntime` type contains neither priority nor a deadline. Until the SAGE schema evolves, the adapter reads hints from `pluginSpec.env`.

`PluginSpec.Env` is controlled by the job author and passed to the container;
it is not an attested source. `trustWorkloadEnvHints` is therefore `false` by
default, including in the example configuration and ConfigMap. An operator can
enable it for a closed pilot. Before multi-tenant use, cloud admission must
authorize, bound, and audit these properties according to the requester's
identity.

| Variable | Format | Default |
|---|---|---|
| `SAGE_SCHEDULER_PRIORITY` | integer from 0 to 100 | `defaultPriority` |
| `SAGE_SCHEDULER_MAX_LATENCY` | positive Go duration, for example `2m` | `defaultMaxQueueLatency` |
| `SAGE_SCHEDULER_ESTIMATED_RUNTIME` | positive Go duration | `defaultEstimatedRuntime` |
| `SAGE_SCHEDULER_DEADLINE_AT` | RFC3339 date | deadline calculated from maximum latency |
| `SAGE_SCHEDULER_SUCCESS_RATE` | number between 0 and 1 | `defaultSuccessRate` |

Example plugin:

```yaml
plugins:
  - name: smoke-detector
    pluginSpec:
      image: registry.sagecontinuum.org/example/smoke-detector:1.0.0
      env:
        SAGE_SCHEDULER_PRIORITY: "95"
        SAGE_SCHEDULER_MAX_LATENCY: "30s"
        SAGE_SCHEDULER_ESTIMATED_RUNTIME: "12s"
        SAGE_SCHEDULER_SUCCESS_RATE: "0.92"
      selector:
        resource.gpu: "true"
      resource:
        request.cpu: "500m"
        request.memory: "512Mi"
        limit.gpu: "1"
```

For a periodic rule, prefer `SAGE_SCHEDULER_MAX_LATENCY`. A fixed RFC3339 deadline quickly becomes stale if the same plugin is triggered multiple times.

SAGE also passes these variables to the application container. A proper upstream integration should instead add these properties to the `schedule(...)` parameters, already parsed by `ScienceRule.ActionParameters`, and then retain them in `PluginRuntime`.

Do not store hints in:

- `pluginSpec.selector`, because every key becomes a Kubernetes `nodeSelector`;
- `pluginSpec.resource`, because an unknown key becomes an extended Kubernetes resource.

## GPU and Resources

A task is considered a GPU task if any of the following is positive:

- `selector.resource.gpu: "true"`;
- `resource.limit.gpu`;
- `resource.nvidia.com/gpu`.

`maxGPUConcurrent` is a logical limit based on the SAGE queues. It proves
neither the physical presence of a GPU nor how much memory is free. It counts
GPU workloads, not GPU units. Multi-GPU requests therefore remain outside this
adapter's capacity-fit mode until Waggle provides real GPU capacity.

All declared quantities are validated before selection. The known SAGE keys
are `request.cpu`, `limit.cpu`, `request.memory`, `limit.memory`, and
`limit.gpu`. Any other key must be a qualified extended resource, such as
`example.com/fpga`, with an integer quantity. A CPU or memory request cannot
exceed its limit, and simultaneous GPU aliases must have the same value.

The inspected upstream commit currently passes a fictitiously high capacity to every policy. For this reason:

- `enforceResourceFit` must remain disabled for the first deployment;
- Kubernetes continues to enforce the real `requests` and `limits`;
- an upstream change will be required to provide reliable instantaneous capacity to the engine.

## Queue Semantics

The adapter:

- snapshots `readyQueue` and `scheduledQueue`;
- never modifies them;
- returns pointers originating from `readyQueue`;
- limits how many it returns according to the configuration.

The upstream `NodeScheduler` remains responsible for removing an entry from the queue, moving it to `scheduledQueue`, creating the Pod, and handling lifecycle events.

The internal identity concatenates `GoalID`, `JobID`, the plugin name, and `PodInstance`. Age is recorded on first observation and is not persistent.

The adapter deliberately does not read `PluginSpec.Job` when making a
decision: after creating the Pod, the pinned Waggle controller rewrites this
field in a goroutine without a lock shared with the policy. The properties
used by the decision are limited to stable metadata and declarations in the
spec.

Blocking upstream limitation: `Queue.Pop` compares only `Plugin.Name`,
including on some goal-cleanup paths. The adapter serializes identical names
between `readyQueue` and `scheduledQueue` to protect its own selection. An
invalid first entry can therefore block later entries with the same name, and
upstream cleanup can still remove the wrong runtime. Waggle must be fixed to
remove by pointer or full identity and then add quarantine/requeue behavior
before a canary.

Second blocking upstream limitation: the pinned `Queue` does not expose an
atomic snapshot. `Length` and `More` read its state without locking while the
REST API can call `Push` from another goroutine. The adapter's internal mutex
therefore does not protect against this race. Before a canary, Waggle must add
an atomic copy under the queue lock or serialize REST submissions through the
main loop's channel.

The `POST /api/v1/schedule` endpoint in the same revision also creates a
runtime without a `Queued` transition, a `PodInstance`, or registration in the
`GoalManager`. This local path is therefore explicitly unsupported by the MVP:
it must remain unexposed and must not be called. The normal goals/science-rules
path is the only intended path until an upstream fix is available.

## Failure Modes

### Invalid Hint

A task containing an invalid priority, duration, date, or probability is rejected by the decision. The error is logged. The MVP does not yet publish this reason in SAGE events.

### Engine Error

With `failOpen: true`, the adapter falls back to queue order while respecting global and GPU limits and name collisions. The decision is recorded with `fail_open_fallback`. With `failOpen: false`, it returns the error to the `NodeScheduler`.

### Pod Creation Failure

The policy validates the hints, names, and all resource quantities it sees.
Other errors remain possible in the upstream controller. In the pinned commit,
a failure after moving to `scheduledPlugins` is not always requeued; with a
concurrency of 1, this can block admission. An upstream watchdog/requeue
mechanism is required for true resilience.

### Unsafe Waggle Modes

The upstream `NoRabbitMQ` field does not prevent the builder from creating the
transport, and `Simulate` mode can proceed as far as a missing Kubernetes
client. The binary accepts these values for `-validate-config` but rejects them
before any actual execution.

The RabbitMQ compatibility credentials `service/service` are inherited from
the Waggle binary but are rejected by default during actual execution. The
deployment must preserve `RABBITMQ_USERNAME` and `RABBITMQ_PASSWORD` variables
provided by a Secret. The `--allow-insecure-rabbitmq-defaults` flag requires
explicit acknowledgment if a legacy site still depends on these values.

### Impossible Deadline

The task receives `predictedDeadlineMiss: true` but can remain selected. The MVP knows of neither a degraded version nor an alternative application to launch.

### Restart

Local age is lost. In addition, the inspected upstream revision deletes application Pods when its `ResourceManager` starts. Recovery after a restart must therefore be designed in SAGE before the system can be described as resilient.

## What the Policy Interface Cannot Do

The `SelectBestPlugins` method can select or defer, but it cannot:

- delete or preempt an active Pod;
- trigger a checkpoint;
- create a replica;
- migrate a task;
- manage a retry;
- reserve a Kubernetes resource;
- modify a `PriorityClass`.

These capabilities will require an extension to the SAGE controller or a Kubernetes plugin versioned for the k3s release that is actually deployed.

## Future Deployment

Before any deployment:

1. confirm the exact `edge-scheduler` image and commit on the nodes;
2. record `k3s version` and the target architectures, particularly `linux/arm64`;
3. choose between a replacement `NodeScheduler` image and an upstream addition of `resilient-urgent` to the existing `--policy` registry;
4. confirm the policy mount path and WES service account;
5. generate a ConfigMap hash in the Pod template because the policy is loaded
   only at startup;
6. fix `Queue.Pop`/cleanup operations to use full identity and requeue after a
   creation failure;
7. make queue snapshots atomic with respect to REST API submissions;
8. disable or fix `POST /api/v1/schedule`, and verify network exposure of port
   8080;
9. confirm the `edge-scheduler` license in accordance with [NOTICE](../NOTICE);
10. validate offline and then in shadow mode before a canary.

The prepared manifests and controlled restart procedure are described under
[`integrations/sage/deploy`](../integrations/sage/deploy/README.md).

### Question for the SAGE Team

**Should the first integration be delivered as a replacement image for the WES `NodeScheduler`, or as an upstream contribution that adds `resilient-urgent` to the existing `--policy` argument registry?**

Please also provide the exact `waggle/scheduler` image version and the output of `k3s version` from the target node. This choice determines packaging, the manifest, and Kubernetes compatibility; the MVP deliberately makes no assumption about it.

The module retains `go 1.20`, and CI checks this source compatibility, but the
static image is built with Go 1.26.5 to receive recent standard-library fixes.

## References

- [SAGE Architecture](https://sagecontinuum.org/docs/about/architecture)
- [Waggle edge-scheduler](https://github.com/waggle-sensor/edge-scheduler)
- [Waggle policies](https://github.com/waggle-sensor/edge-scheduler/tree/main/pkg/nodescheduler/policy)
- [Kubernetes Scheduling Framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)
- [scheduler-plugins and compatibility matrix](https://github.com/kubernetes-sigs/scheduler-plugins#compatibility-matrix)
- [KWOK](https://kwok.sigs.k8s.io/)
