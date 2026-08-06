# Tutorial — understanding the resilient urgent scheduler, one piece at a time

This is a hands-on tutorial for someone who has never seen this project before.
You do not need a SAGE node, a Kubernetes cluster, a GPU, or a camera. Everything
below runs offline on a laptop.

The method is the same in every step:

1. **Test one thing** — run a single command, or change a single value.
2. **Look at what came back.**
3. **Write down the conclusion** before moving on.

Do not skip the conclusions. They are the part that stays with you.

---

## Before you start

### What this project actually is

SAGE runs scientific applications on small computers in the field. Those
computers have limited CPU, memory and GPU. Sometimes a routine job and an
urgent job become ready at the same moment, and only one can run.

This project is the piece that **chooses which one starts, and explains why**.
It does not detect smoke, run AI models, create containers, or replace SAGE.

One sentence to keep in mind:

> In urgent computing, the right result delivered too late is the wrong result.

### What you need

- Go 1.20 or newer (`go version`)
- Git
- A terminal

The first build of the SAGE integration downloads Kubernetes client libraries,
so the very first command needs internet access. Everything after that is offline.

### Getting there

```bash
cd sage-summer-camp-2026/orchestrator
```

**Every command in this tutorial is run from that `orchestrator/` directory.**

### The map

Read this once. You do not need to understand it yet — you will meet each piece
in its own step.

```text
orchestrator/
├── pkg/policy/            THE BRAIN. Decides. Knows nothing about SAGE.   -> Steps 2-8
├── pkg/chaos/             Measures how sensitive the brain is.            -> Step 9
├── pkg/orchestration/     OPTIONAL. Two-camera capture + HaLow transfer.  -> Step 12
├── pkg/intent/            OPTIONAL. Plain English -> science-goal draft.  -> Step 13
├── cmd/policyctl/         Run the brain offline on a JSON file.          -> Steps 4-8
├── cmd/chaoslab/          Run sensitivity experiments.                    -> Step 9
├── cmd/intentctl/         Run the intent translator.                      -> Step 13
├── integrations/sage/     The ONLY code that touches SAGE/Waggle.         -> Steps 10-11
├── config/                Example policy configuration.                   -> Step 3
└── examples/              Ready-made inputs to experiment with.
```

The single most important idea in the whole repository:

> `pkg/policy` does not import SAGE and does not import Kubernetes.
> That is why you can test all of it on a laptop.

---

# Part 1 — The decision engine

## Step 1 — Prove everything works before you change anything

**What you are testing:** that the project builds and its tests pass on your
machine, before you touch a thing.

```bash
make test
```

**What you should see:** a list of `ok` lines, one per package, and no `FAIL`.

**Conclusion to write down:** roughly a hundred tests, including a fuzz test,
run in a couple of seconds with no cluster, no network and no hardware. This is
the practical pay-off of keeping the engine free of SAGE and Kubernetes imports.
If a test ever fails later in this tutorial, you now know it was your change.

---

## Step 2 — Meet the vocabulary

**What you are testing:** nothing yet. Five words appear everywhere in this
project. Learn them now and everything afterwards is easier.

| Word | Meaning |
|---|---|
| **Task** | One application waiting to run. Has a priority, a deadline, a size. |
| **Selected** | Valid work that may start **now**. |
| **Deferred** | Valid work that **stays in the queue** and may start later. |
| **Rejected** | Invalid or ineligible work. Not the same as deferred. |
| **Slack** | How much spare time is left before the deadline. |

The distinction that beginners get wrong most often:

> **Deferred is not a failure.** A deferred task was not rejected, dropped, or
> cancelled. It simply did not get the slot this round. It is still queued.

**Conclusion:** three outcomes, not two. Selected, deferred, rejected.

---

## Step 3 — Read the configuration, then break it on purpose

**What you are testing:** that the policy refuses to start with a nonsensical
configuration.

First look at the file:

```bash
cat config/policy.example.yaml
```

The fields that matter most:

| Field | What it controls |
|---|---|
| `maxConcurrent: 1` | How many tasks may run at once. **Set to 1** in the example. |
| `maxGPUConcurrent: 1` | How many of those may be GPU tasks. |
| `agingHorizon: 10m` | After 10 minutes of waiting, a task's "age" score is at maximum. |
| `slackHorizon: 5m` | 5 minutes of spare time or more counts as "not urgent". |
| `minimumSuccessRate: 0.50` | Below this predicted reliability, defer the task. |
| `weights` | How much each factor counts. They total 1.0. |
| `trustWorkloadEnvHints: false` | Ignore scheduling hints written by job authors. |
| `enforceResourceFit: false` | Do not filter on CPU/memory until SAGE reports real capacity. |

Now check that it is valid:

```bash
go run ./cmd/policyctl -config config/policy.example.yaml -validate-config
```

```text
configuration is valid
```

Now break it deliberately. Open `config/policy.example.yaml` and set
`maxGPUConcurrent: 5` while leaving `maxConcurrent: 1`. Run the same command
again:

```text
policyctl: validate policy config: maxGPUConcurrent must be between 0 and maxConcurrent
```

**Put the file back to `maxGPUConcurrent: 1` before continuing.**

Try one more break: add a line `maxConcurent: 2` (note the typo, one `r`). You
will get a decoding error naming the unknown field, not a silent default.

**Conclusion:** the configuration is validated strictly and fails immediately
with a readable message. A typo cannot silently become a default. This matters
because on a real node nobody is watching the logs at 3 a.m.

---

## Step 4 — Your first decision

**What you are testing:** the engine's core behaviour — one urgent task and one
routine task competing for a single slot.

Look at the input first:

```bash
cat examples/snapshots/urgent-vs-routine.json
```

It describes a moment in time (`at`), two ready tasks, nothing running:

| Task | priority | estimated runtime | latency budget | GPU |
|---|---|---|---|---|
| `image-sampler` | 20 | 30 s | 10 min | no |
| `smoke-detector` | 90 | 20 s | 45 s | yes |

Now run the decision:

```bash
go run ./cmd/policyctl \
  -config config/policy.example.yaml \
  -snapshot examples/snapshots/urgent-vs-routine.json
```

**What you should see** (abridged — the real output is full JSON):

```text
selected: goal-urgent/job-urgent/smoke-detector/instance-002

candidates:
  goal-routine/.../image-sampler/instance-001
      outcome  deferred
      reason   concurrency_limit
      total    19.65
      slack    +540 s
  goal-urgent/.../smoke-detector/instance-002
      outcome  selected
      reason   selected
      total    80.75
      slack    -5 s      predictedDeadlineMiss: true
```

Three things to notice:

- The urgent task won, and the score says why: 80.75 against 19.65.
- The routine task was **deferred**, not rejected. Its reason is
  `concurrency_limit` — it lost on capacity, not on quality.
- `smoke-detector` has **negative slack**: even though it was selected, the
  engine already predicts it will miss its deadline, and says so out loud.

**Conclusion:** every candidate leaves with an outcome *and* a machine-readable
reason. The scheduler never says "no" without saying why.

---

## Step 5 — Check the score by hand

**What you are testing:** that the score is arithmetic you can verify yourself,
not a black box.

The engine normalises four things onto a 0–100 scale, then takes a weighted
average using the weights from your config (0.45 / 0.30 / 0.15 / 0.10).

For `smoke-detector`, at decision time `12:00:30`:

| Factor | Value | How it was obtained |
|---|---|---|
| Priority | 90 | Declared in the snapshot. |
| Slack | 100 | Slack is negative, so urgency is maxed out. |
| Age | 5 | Waited 30 s of the 10 min `agingHorizon` → 30/600 = 5 %. |
| Reliability | 95 | `predictedSuccessRate` 0.95 → 95. |

```text
90 x 0.45  +  100 x 0.30  +  5 x 0.15  +  95 x 0.10
= 40.5     +  30          +  0.75      +  9.5
= 80.75
```

Now `image-sampler`. Its deadline is 10 minutes out, so it has 540 s of slack —
more than the 5 min `slackHorizon`, so its urgency score clamps to **0**:

```text
20 x 0.45  +  0 x 0.30  +  5 x 0.15  +  99 x 0.10
= 9        +  0         +  0.75      +  9.9
= 19.65
```

Both match the output from Step 4 exactly.

**Conclusion:** the ranking is four numbers and a weighted average. There is no
machine learning here and nothing hidden. If you disagree with a decision, you
can recompute it on paper and find the factor responsible.

---

## Step 6 — Change one number and watch the outcome change

**What you are testing:** whether `deferred` really was about capacity.

Edit `config/policy.example.yaml` and set `maxConcurrent: 2`. Re-run the exact
command from Step 4.

**What you should see:**

```text
image-sampler    selected   selected
smoke-detector   selected   selected
```

Both tasks are now selected. Nothing about the tasks changed — only the number
of available slots.

**Put `maxConcurrent` back to `1` before continuing.**

**Conclusion:** `concurrency_limit` was a statement about the *node*, not about
`image-sampler`. This is why the distinction between deferred and rejected
matters: deferred work is fine, it is just waiting.

---

## Step 7 — Make a task fail the reliability gate

**What you are testing:** that admission checks happen in a fixed order, and
that the reason string tells you which gate stopped the task.

Copy the example so you do not damage it:

```bash
cp examples/snapshots/urgent-vs-routine.json /tmp/reliability-test.json
```

In `/tmp/reliability-test.json`, change `image-sampler`'s
`"predictedSuccessRate": 0.99` to `0.40`. That is below the
`minimumSuccessRate: 0.50` in your config. Then run:

```bash
go run ./cmd/policyctl \
  -config config/policy.example.yaml \
  -snapshot /tmp/reliability-test.json
```

**What you should see:**

```text
image-sampler    deferred   reliability_threshold    total 13.75
smoke-detector   selected   selected                 total 80.75
```

The reason changed from `concurrency_limit` to `reliability_threshold`.

This is the order the engine applies, and it stops at the first failure:

```text
1. valid metadata, unique ID        -> rejected: invalid_task
2. reliability threshold            -> deferred: reliability_threshold
3. concurrency limit                -> deferred: concurrency_limit
4. GPU concurrency limit            -> deferred: gpu_capacity
5. resource fit (off by default)    -> deferred: resource_capacity
```

`image-sampler` would have failed gate 3 anyway, but it never got there — gate 2
stopped it first.

**Conclusion:** the reason string names the **first** gate that blocked the
task, not every reason it might have failed. When debugging a real node, fix the
reason you are given, then look again.

---

## Step 8 — Watch a deadline become impossible

**What you are testing:** what the engine does when a task cannot possibly
finish on time.

Look again at `smoke-detector` in Step 4. Its numbers:

```text
deadline  = enqueued 12:00:00 + latency budget 45 s = 12:00:45
now       = 12:00:30
runtime   = 20 s

slack = deadline - now - runtime
      = 12:00:45 - 12:00:30 - 20 s
      = 15 s - 20 s
      = -5 s
```

Negative slack means: even if it started this instant, it would finish 5 seconds
late. The engine sets `predictedDeadlineMiss: true` — **and selects it anyway**.

That is deliberate, and it is documented as a limitation. The engine knows the
deadline is at risk, but it does not know of any cheaper model, smaller image or
alternative application to run instead. Refusing to run it would guarantee no
result at all.

**Conclusion:** the MVP makes an impossible deadline *visible* rather than
silently accepting it or silently dropping the task. Acting on that signal —
switching to a faster model, shedding load — is future work, not something this
version does.

---

## Step 9 — Sensitivity: how fragile is a decision?

**What you are testing:** whether small changes to the inputs flip the outcome.

```bash
go run ./cmd/chaoslab \
  -config config/policy.example.yaml \
  -experiment examples/chaos/sensitivity.json
```

The baseline in this experiment is deliberately cruel: `smoke-detector` and
`air-quality` are given **identical** priority, deadline, arrival time and
runtime. Their scores come out exactly equal, at 71.75 each. With
`maxConcurrent: 1`, one of them must lose.

**What you should see:**

```text
baseline selected: air-quality

smoke-detector-deadline-10s-earlier    -> smoke-detector   changed  distance 1.0
smoke-detector-arrives-5s-earlier      -> smoke-detector   changed  distance 1.0
smoke-detector-reliability-drops       -> air-quality      unchanged distance 0.0
smoke-detector-runtime-plus-15s        -> smoke-detector   changed  distance 1.0
```

Read that carefully — there are three separate lessons hiding in it.

**Lesson 1 — ties are broken deterministically, not randomly.** With scores dead
equal, the engine falls back to: earliest deadline, then oldest arrival, then
alphabetical ID. `air-quality` beats `smoke-detector` on the letter *a*. That is
arbitrary, but it is *repeatable* — run it a thousand times and you get the same
answer, which is what you need for reproducible experiments.

**Lesson 2 — `distance` is how much the selection moved.** 0.0 means the same
set of tasks was chosen; 1.0 means completely different. It is the Jaccard
distance between the two selected sets.

**Lesson 3 — the counter-intuitive one.** Look at
`smoke-detector-runtime-plus-15s`. Making the task **slower** got it
**selected**. That is not a bug. A longer runtime eats into the slack, which
makes the deadline tighter, which raises the urgency score. The policy rewards
tight slack — so a task can win a slot by being slow. Worth knowing before you
trust `estimatedRuntime` values submitted by other people.

**Conclusion:** `chaoslab` does not break anything or inject faults. It re-runs
the same decision with one input nudged, and reports whether the answer moved.
That is how you find out which factor a decision is actually resting on.

---

# Part 2 — The SAGE integration

Everything so far knew nothing about SAGE. Now we cross the boundary.

## Step 10 — The adapter, and why it exists at all

**What you are testing:** that the SAGE integration compiles against the real
Waggle interface without contacting anything.

```bash
go run ./integrations/sage/cmd/waggle-nodescheduler \
  -policy-config config/policy.example.yaml \
  -validate-config
```

```text
configuration is valid
```

That command loaded the real Waggle `NodeScheduler` configuration, loaded the
policy, and **exited before connecting to anything.** No Kubernetes, no
RabbitMQ, no SAGE services.

Why a separate Go binary at all? Because Waggle's `NodeScheduler` picks its
scheduling policy from a list that is **compiled into its binary**. You cannot
drop in a Python script. So the integration provides a second entry point that
builds the normal Waggle scheduler and swaps in this policy.

What the adapter does on each scheduling round:

1. Take a snapshot of the ready queue and the running queue.
2. Convert each Waggle `PluginRuntime` into a generic engine `Task`.
3. Ask the engine to decide.
4. Return pointers to the tasks it selected.

What it deliberately does **not** do: push, pop, or reorder anything in a SAGE
queue. Creating Pods stays with the upstream `NodeScheduler`.

**Conclusion:** the adapter is a translator, not a scheduler. All the
decision-making lives in `pkg/policy`, which is why Steps 4–9 were possible
without SAGE.

---

## Step 11 — Scheduling hints, and why they are switched off

**What you are testing:** understanding a security decision.

A Waggle task has no priority or deadline field. So the adapter reads them from
environment variables in the plugin spec:

```yaml
plugins:
  - name: smoke-detector
    pluginSpec:
      env:
        SAGE_SCHEDULER_PRIORITY: "95"
        SAGE_SCHEDULER_MAX_LATENCY: "30s"
        SAGE_SCHEDULER_ESTIMATED_RUNTIME: "12s"
        SAGE_SCHEDULER_SUCCESS_RATE: "0.92"
```

Now look at your config again:

```yaml
trustWorkloadEnvHints: false
```

**These hints are ignored by default.** Ask yourself why before reading on.

The reason: `pluginSpec.env` is written by whoever submits the job. It is not
checked or signed by SAGE. If the scheduler trusted it, any user could write
`SAGE_SCHEDULER_PRIORITY: "100"` on every job they submit and starve everyone
else. The declaration is not a measurement.

Turning it on is a deliberate operator decision, for a closed pilot where you
know every person who can submit a job.

**Conclusion:** the default is "distrust the workload". Enabling hints in a
shared environment requires SAGE to authorise and bound them per user first —
which does not exist yet.

---

## Step 12 — The optional paired-camera core

**What you are testing:** a part of the repository that has nothing to do with
scheduling.

```bash
go test ./pkg/orchestration/...
```

`pkg/orchestration` is for an application that needs **two images at once** —
for example a normal camera plus a second camera reached over a low-bandwidth
Wi-Fi HaLow link — and wants to be sure they actually belong together before
feeding them to a model.

It captures both concurrently under one request ID, then refuses the pair unless
all of this holds:

- both images carry the **same request ID** (they are the same event)
- each has a camera ID, a capture time, and an `image/*` media type
- a JPEG actually starts with `FF D8` and ends with `FF D9` (not truncated)
- the payload is non-empty and under the size limit
- each image reports **where the camera was and where it was pointing**
- both timestamps are trustworthy, and their skew is within the limit

Only then is the analyser called. For sending large images over a slow link,
there is also a chunked transfer with a SHA-256 checksum, and an acknowledgement
that says *persisted*: a store-and-forward camera may only delete its local copy
after receiving it.

**Conclusion:** this is **additive**. The scheduler does not require it, and an
application that uses one camera is unaffected. Note also what is *not* here: no
camera driver, no MQTT client, no AI model. Those are interfaces waiting for an
implementation.

---

## Step 13 — The intent gateway

**What you are testing:** the safety property of the natural-language feature.

```bash
go test ./pkg/intent/...
```

`intentctl` sends a sentence like *"watch for cloud cover over the north field"*
to an internal Hermes/GLM service and gets back a **draft science goal**: a small
JSON object with a goal, any applications and nodes that were explicitly named,
and — importantly — a list of `questions` about what is still missing.

Running it for real needs the Hermes endpoint, so it is not part of this offline
tutorial:

```bash
export HERMES_CHAT_COMPLETIONS_URL=http://hermes.internal/v1/chat/completions
go run ./cmd/intentctl -input examples/intents/cloud-cover.txt
```

The safety property is worth understanding even without running it. Every draft
carries `humanApprovalRequired: true`, and the validator **rejects any draft
where it is false**. The model is instructed to treat the request as untrusted
data and to invent nothing — no applications, no nodes, no priorities, no
deadlines.

**Conclusion:** the output is a draft for a human to read, not a command. It
cannot be piped into the scheduler, and it cannot set a priority. The language
model is kept outside the decision path entirely.

---

# Part 3 — Knowing the limits

## Step 14 — What this project does not do

**What you are testing:** your own understanding. Try to answer before reading.

> A task was selected but its Pod crashed. What does the scheduler do?

**Nothing.** And that is the honest headline of this project.

| Not implemented | What that means in practice |
|---|---|
| Preemption | A running task is never stopped to make room for an urgent one. |
| Retries | A failed task is not retried by this policy. |
| Checkpoint / recovery | No state is saved or restored. |
| Fallback application | "Fail-open" picks tasks in queue order — it does **not** launch a simpler app. |
| Persistence | How long a task has waited is forgotten on restart. |
| Resource fitting | Off by default, because Waggle currently reports fake capacity. |
| Deployment | Never yet run on a real SAGE node. |

There are also two **upstream** problems in Waggle itself, documented and not
worked around:

1. Its queue removes entries by plugin **name** only, so two tasks with the same
   name can be confused. The adapter refuses to select a same-named task as a
   guard, but the upstream code still needs fixing.
2. Its queue has no atomic snapshot — the REST API can add a task while the
   policy is reading the queue.

Both must be fixed upstream before this runs on real hardware.

**Conclusion:** the project delivers **explainable admission and ordering**, and
is deliberately clear about the difference between that and full resilience.
Being able to state your limits precisely is a feature.

---

## Where to go next

Now that the pieces make sense, read them in this order:

| Read this | To understand |
|---|---|
| `docs/architecture.md` | How the parts fit, and the full limitations table |
| `docs/sage-integration.md` | The exact SAGE contract and the upstream blockers |
| `pkg/policy/engine.go` | The `Decide` function — about 130 lines, all of Steps 4–8 |
| `docs/halow-orchestration.md` | The two-camera flow from Step 12 |
| `docs/intent-translation.md` | The intent boundary from Step 13 |
| `../classroom-notes.md` | The SAGE workflow itself: nodes, `pluginctl`, ECR, jobs |

And the validation path this project still has ahead of it:

```text
offline replay  ->  KWOK  ->  k3s  ->  shadow mode  ->  controlled canary
   (done)                                                  (not started)
```

---

## Quick reference

```bash
make test              # run every test
make example           # the Step 4 decision
make chaos-example     # the Step 9 sensitivity run
make validate-config   # validate both configurations
make ci                # everything CI runs: format, test, vet, build, examples
```

| Reason string | Meaning |
|---|---|
| `selected` | Admitted to start now. |
| `concurrency_limit` | No free slot. Still queued. |
| `gpu_capacity` | No free GPU slot. Still queued. |
| `reliability_threshold` | Predicted success rate below the minimum. |
| `resource_capacity` | Does not fit in reported CPU/memory/GPU. |
| `invalid_task` | Malformed or duplicate. Rejected, not deferred. |
| `fail_open_fallback` | The engine errored; chosen by queue order instead. |
| `sage_queue_name_collision` | Another queued task shares this plugin name. |
