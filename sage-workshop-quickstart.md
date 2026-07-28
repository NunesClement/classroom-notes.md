# SAGE workshop quickstart

This guide uses placeholders such as `YOUR_NODE_ID`. Never commit usernames,
tokens, passwords, private keys, personal paths, or camera captures.

The workflow is:

```text
local code → container → pluginctl test → ECR → scheduled job → SAGE data API
```

## 1. Connect and update Hermes

From your computer:

```bash
SAGE_NODE="YOUR_NODE_ID"
ssh "waggle-dev-node-${SAGE_NODE}"
```

On the development node:

```bash
cp ~/.hermes/profiles/sage/config.yaml \
   ~/.hermes/profiles/sage/config.yaml.before-update

hermes profile update sage --force-config
hermes profile info sage
hermes -p sage doctor
hermes -p sage skills list
hermes -p sage config get model
```

If needed, select the assigned provider and model without displaying or
re-entering credentials:

```bash
hermes -p sage model
```

## 2. Run Hermes in `tmux`

Check for an existing session:

```bash
tmux ls
```

Create a session, or attach to an existing detached session:

```bash
tmux new -s hermes
# or
tmux attach -t hermes
```

Inside `tmux`, start Hermes and ask a question:

```bash
hermes -p sage
```

Detach without stopping Hermes: press `Ctrl-B`, release both keys, then press
lowercase `d`.

Verify or reconnect:

```bash
tmux ls
tmux capture-pane -pt hermes | tail -30
tmux attach -t hermes
```

If `tmux` warns about nested sessions, you are already inside one; do not
attach again. If the key sequence fails, press `Ctrl-B`, then `:`, enter
`detach-client`, and press Enter.

Detaching does not stop Hermes. When finished, use `/quit` inside Hermes, or
remove the entire session:

```bash
tmux kill-session -t hermes
```

## 3. Inspect the development node

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

Do not install packages or run an edge app directly on the node. Use a
container through `pluginctl`.

## 4. Develop an edge app locally with `uv`

On your computer:

```bash
uv --version
# If uv is missing on macOS:
brew install uv

uvx cookiecutter gh:waggle-sensor/cookiecutter-sage-app
cd YOUR_REPOSITORY
uv venv
uv pip install --requirement requirements.txt
PYWAGGLE_LOG_DIR=test-run uv run python main.py
```

The generated app should include `main.py`, `requirements.txt`, `Dockerfile`,
and `sage.yaml`.

Keep local output and secrets out of Git:

```gitignore
.env
.env.*
!.env.example
.venv/
test-run/
snapshot.jpg
*.key
*.pem
```

## 5. Build and test on the node

Push the local repository, then connect to the development node:

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

## 6. Query published data

Replace the placeholders:

```bash
NODE_ID="YOUR_NODE_ID"
TASK_NAME="YOUR_TASK_NAME"

curl -sS \
  -H "Content-Type: application/json" \
  https://data.sagecontinuum.org/api/v1/query \
  -d "{\"start\":\"-5m\",\"filter\":{\"vsn\":\"${NODE_ID}\",\"task\":\"${TASK_NAME}\"}}"
```

## 7. Publish and schedule

Before publishing, verify `sage.yaml`, the version, repository URL, and ECR
metadata:

```bash
sed -n '1,240p' sage.yaml
find ecr-meta -maxdepth 1 -type f
git status
```

Push the changes, then register the repository through the
[SAGE Apps portal](https://portal.sagecontinuum.org/apps/). Increment the
version before every new ECR build.

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

Remove the job when testing is complete; a submitted job may otherwise keep
running:

```bash
sesctl rm --force YOUR_JOB_ID
unset SES_USER_TOKEN
```

## Official references

- [SAGE architecture](https://sagecontinuum.org/docs/about/architecture)
- [Create an edge app](https://sagecontinuum.org/docs/tutorials/edge-apps/creating-an-edge-app)
- [Test an edge app](https://sagecontinuum.org/docs/tutorials/edge-apps/testing-an-edge-app)
- [Publish to ECR](https://sagecontinuum.org/docs/tutorials/edge-apps/publishing-to-ecr)
- [Submit a job](https://sagecontinuum.org/docs/tutorials/schedule-jobs)
