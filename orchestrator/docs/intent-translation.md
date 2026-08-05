# Intent Translation

An intent states the result or desired state a person wants, without prescribing
the mechanism used to reach it. In a computing continuum, the orchestrator then
maps that result to available edge, site, and cloud resources while respecting
the stated requirements. This is the common declarative meaning used by
[3GPP intent-driven management](https://www.3gpp.org/technologies/intent) and
intent-based continuum work such as
[IntentContinuum](https://arxiv.org/abs/2504.04429).

SAGE already has suitable words for the first translation step. A SAGE job is a
science goal made concrete with applications, nodes or node tags, science
rules, and success criteria. The translator therefore produces only these
fields instead of introducing another intent ontology:

| Intent question | Existing SAGE word |
|---|---|
| What result is wanted? | `goal` |
| Which known code performs it? | `applications` |
| Where should it run? | `nodes`, `nodeTags` |
| When should it run? | `scienceRules` |
| When is the goal complete? | `successCriteria` |
| What is missing? | `questions` |

The [official SAGE job editor](https://portal.sagecontinuum.org/jobs/create-job)
exposes the corresponding `plugins`, `nodes`, `nodeTags`, `scienceRules`, and
`successCriteria` fields in a submitted science goal. The draft uses
“applications” because that is the user-facing term used throughout this
repository; selecting a concrete plugin image remains a later, human-reviewed
step.

## Minimal integration

```text
human text -> Hermes / GLM 5.2 -> small JSON science-goal draft -> human review
```

`intentctl` sends one request to the existing OpenAI-compatible Hermes endpoint.
The prompt treats the input as untrusted data and tells the model not to invent
applications, nodes, rules, permissions, or deployment details. The Go side
rejects unknown fields and invalid lists, restores the original text, and forces
`humanApprovalRequired` to `true`.

The result is not a SAGE job and is never sent to the scheduler. In particular,
the translator does not select a container image or assign the scheduler's
priority and deadline hints.

## Usage

```bash
export HERMES_CHAT_COMPLETIONS_URL=http://hermes.internal/v1/chat/completions
export HERMES_MODEL=glm-5.2
export HERMES_API_KEY=replace-if-required

go run ./cmd/intentctl -input examples/intents/cloud-cover.txt
```

Input may also come from stdin or `-text`. If Hermes does not support OpenAI's
JSON response mode, use `-json-mode=false`; local strict decoding still applies.
A dedicated model deployment is unnecessary for this small experiment. It only
becomes useful for independent capacity, latency, availability, or data-isolation
requirements.

Structural validation cannot prove that the translation is faithful. A reviewer
must compare the draft with `sourceText` before completing a real SAGE job.
