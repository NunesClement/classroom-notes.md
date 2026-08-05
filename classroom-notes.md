# Classroom notes — Sage Summer Camp 2026

Running notes and consolidated workshop tutorials for the **resilient urgent
SAGE orchestration** project. All commands use public placeholders. Never
commit usernames, tokens, passwords, private keys, personal paths, or camera
captures.

## 2026-07-21 — SAGE development workflow

- A SAGE edge app packages its code, dependencies, and models so it can be
  built and scheduled on Waggle nodes.
- Develop and test locally first. Do not install application packages or run
  an unpackaged edge app directly on a SAGE node.
- The normal application path is:

  ```text
  local code → container → pluginctl test → ECR → scheduled job → SAGE data API
  ```

- A generated application should include `main.py`, `requirements.txt`,
  `Dockerfile`, and `sage.yaml`.
- `sage.yaml` and `Dockerfile` should be at the repository root unless the
  manifest explicitly points at an application subdirectory.

## 2026-07-27 — Resilient urgent scheduler

- Published the reusable scheduler in
  [`NunesClement/sage-resilient-urgent-scheduler`](https://github.com/NunesClement/sage-resilient-urgent-scheduler).
- Kept the policy engine application-agnostic; Mortimus is the first integration
  and reaches the engine through the SAGE/Waggle adapter.
- Defined the challenge as explainable admission for urgent and routine
  edge-AI tasks competing for constrained SAGE resources.
- Implemented deterministic ranking from priority, deadline slack, queue age,
  and predicted reliability.
- Added global and GPU concurrency limits, optional resource fitting, strict
  configuration validation, and per-candidate decision reasons.
- Kept application-provided scheduling hints disabled by default because they
  are declarations rather than trusted measurements.
- Added a bounded FIFO fail-open path. This fallback protects scheduler
  continuity; it does not select an alternative AI model.
- Added offline replay (`policyctl`) and deterministic sensitivity analysis
  (`chaoslab`).
- Added the SAGE/Waggle policy adapter and replacement NodeScheduler build,
  while leaving Pod creation and lifecycle management with the upstream
  controller.
- Added an optional Hermes/GLM intent gateway that returns a human-reviewed
  science-goal draft rather than an executable scheduler command.

## 2026-07-27 — Paired-camera and HaLow design

- Added an optional paired-camera coordinator that captures a primary and
  additional image under one request identity.
- The coordinator validates camera identity, capture correlation, media type,
  image size, JPEG completeness, synchronized timestamps, maximum skew,
  capture-time position, and PTZ state before calling an injected analyzer.
- HaLow remains a transport seam rather than a scheduler requirement.
- The transfer contract supports out-of-order chunks, SHA-256 verification,
  and an acknowledgement tied to the exact request, camera, and digest.
- A camera may delete its cached image only after a matching
  `persisted: true` acknowledgement.
- Concrete camera, GPS/PTZ, MQTT, durable-storage, and AI implementations
  remain outside this repository.

## Consolidated workshop tutorial

### 1. Local Python setup with `uv`

Check whether `uv` is installed:

```bash
uv --version
```

If it is missing on macOS:

```bash
brew install uv
```

For a new Python project:

```bash
uv init
uv add package-name
uv run python main.py
```

For an existing project with `pyproject.toml` and `uv.lock`:

```bash
uv sync
uv run python main.py
```

Useful dependency commands:

```bash
uv add package-name
uv remove package-name
uv run python script.py
```

To start from the SAGE application template:

```bash
uvx cookiecutter gh:waggle-sensor/cookiecutter-sage-app
cd YOUR_REPOSITORY
uv venv
uv pip install --requirement requirements.txt
PYWAGGLE_LOG_DIR=test-run uv run python main.py
```

Keep local environments, output, captures, and secrets out of Git:

```gitignore
.env
.env.*
!.env.example
.venv/
__pycache__/
*.py[cod]
test-run/
snapshot.jpg
*.key
*.pem
```

Reference: [Installing uv](https://docs.astral.sh/uv/getting-started/installation/)

### 2. Connect to a SAGE development node

Your SAGE account must have access to the target node, and the SAGE SSH setup
must already be installed. Access is managed through the
[SAGE portal](https://portal.sagecontinuum.org/account/access).

From your computer:

```bash
SAGE_NODE="YOUR_NODE_ID"
ssh "waggle-dev-node-${SAGE_NODE}" hostname
ssh "waggle-dev-node-${SAGE_NODE}"
```

To run one command and disconnect:

```bash
ssh "waggle-dev-node-${SAGE_NODE}" tmux ls
```

SSH may request the key passphrase interactively. Do not store it in this
repository.

### 3. Update and verify Hermes

On the development node, back up and update the SAGE profile:

```bash
cp ~/.hermes/profiles/sage/config.yaml \
   ~/.hermes/profiles/sage/config.yaml.before-update

hermes profile update sage --force-config
hermes profile info sage
hermes -p sage doctor
hermes -p sage skills list
hermes -p sage config get model
```

If needed, select the assigned provider and model interactively without
putting credentials in the repository:

```bash
hermes -p sage model
```

Remove old and archived sessions when stale context must not be retrieved:

```bash
hermes sessions prune --older-than 0 --include-archived --yes
```

### 4. Run Hermes in `tmux`

Inspect sessions:

```bash
tmux ls
```

Create or attach to the Hermes session:

```bash
tmux new -s hermes
# or
tmux attach -t hermes
```

Inside `tmux`:

```bash
hermes -p sage
```

Detach without stopping Hermes by pressing `Ctrl-B`, releasing both keys, and
then pressing lowercase `d`.

Verify or reconnect:

```bash
tmux ls
tmux capture-pane -pt hermes | tail -30
tmux attach -t hermes
```

If `tmux` reports a nested session, do not attach again. Press `Ctrl-B`, then
`:`, enter `detach-client`, and press Enter if the normal detach sequence does
not work.

When finished, use `/quit` inside Hermes or remove the session:

```bash
tmux kill-session -t hermes
```

A one-off prompt can be run outside `tmux`:

```bash
hermes -p sage -s sage-waggle -z "Your prompt"
```

### 5. Inspect the development node

These commands are read-only:

```bash
hostname
uptime
df -h
free -h
sudo kubectl get nodes -o wide
sudo kubectl get pods -A
sudo pluginctl ps
```

Use the supported container and `pluginctl` workflow for applications rather
than installing packages directly on the node.

### 6. Build and test an edge application

Push the application repository, then clone and build it on the development
node:

```bash
git clone https://github.com/YOUR_GITHUB_USER/YOUR_REPOSITORY.git
cd YOUR_REPOSITORY
sudo pluginctl build .
```

Copy the image name printed by the build:

```bash
IMAGE="IMAGE_PRINTED_BY_PLUGINCTL"
RUN_NAME="sage-test"

sudo pluginctl run --name "$RUN_NAME" "$IMAGE"
sudo pluginctl ps
sudo pluginctl logs "$RUN_NAME"
sudo pluginctl rm "$RUN_NAME"
```

Always remove test runs when finished.

### 7. Query published SAGE data

For a quick query, replace the node and task placeholders:

```bash
NODE_ID="YOUR_NODE_ID"
TASK_NAME="YOUR_TASK_NAME"

curl -sS \
  -H "Content-Type: application/json" \
  https://data.sagecontinuum.org/api/v1/query \
  -d "{\"start\":\"-5m\",\"filter\":{\"vsn\":\"${NODE_ID}\",\"task\":\"${TASK_NAME}\"}}"
```

For repeatable analysis, use the SAGE Python data client. For protected
downloads, load credentials from the environment:

```bash
curl -L -u "$SAGE_USERNAME:$SAGE_TOKEN" "$URL" -o output-file
```

Never print those variables or commit the `.env` file that defines them.

### 8. Store a secret in macOS Keychain

Choose a non-sensitive service label:

```bash
KEYCHAIN_SERVICE="sage-api"
```

Add or update the secret interactively:

```bash
security add-generic-password \
  -a "$USER" \
  -s "$KEYCHAIN_SERVICE" \
  -U \
  -w
```

Retrieve it only when needed:

```bash
security find-generic-password \
  -a "$USER" \
  -s "$KEYCHAIN_SERVICE" \
  -w
```

The retrieved value remains sensitive. Do not copy it into logs, screenshots,
source files, or committed shell configuration.

### 9. Publish to ECR and schedule

Before publishing, verify the manifest, version, repository URL, and ECR
metadata:

```bash
sed -n '1,240p' sage.yaml
find ecr-meta -maxdepth 1 -type f
git status
```

Push the changes, register the repository through the
[SAGE Apps portal](https://portal.sagecontinuum.org/apps/), and increment the
version before each new ECR build.

Only schedule on an approved node. Load the scheduler token without printing
it:

```bash
export SES_HOST="https://es.sagecontinuum.org"
read -rsp "SAGE scheduler token: " SES_USER_TOKEN
printf '\n'
export SES_USER_TOKEN

sesctl ping
sesctl create --file-path myjob.yaml
sesctl stat
sesctl submit --job-id YOUR_JOB_ID
sesctl stat --job-id YOUR_JOB_ID
```

Remove the job when testing is complete:

```bash
sesctl rm --force YOUR_JOB_ID
unset SES_USER_TOKEN
```

## Backlog / questions

- Confirm whether the first SAGE integration should be a replacement
  NodeScheduler image or an upstream `resilient-urgent` policy contribution.
- Obtain the exact deployed Waggle image/commit and `k3s version`.
- Confirm namespace, Deployment/container names, service account, CPU
  architectures, image digest, RabbitMQ secrets, and port 8080 exposure.
- Fix upstream full-identity queue removal, atomic queue snapshots, and
  requeue behavior before a canary.
- Run offline replay, race tests, shadow mode, and then a controlled canary.
- Decide whether paired-camera work is in the first deployment scope; if so,
  select concrete camera, GPS/PTZ, MQTT, storage, and analyzer adapters.

## Official references

- [SAGE architecture](https://sagecontinuum.org/docs/about/architecture)
- [Create an edge app](https://sagecontinuum.org/docs/tutorials/edge-apps/creating-an-edge-app)
- [Test an edge app](https://sagecontinuum.org/docs/tutorials/edge-apps/testing-an-edge-app)
- [Publish to ECR](https://sagecontinuum.org/docs/tutorials/edge-apps/publishing-to-ecr)
- [Submit a job](https://sagecontinuum.org/docs/tutorials/schedule-jobs)
