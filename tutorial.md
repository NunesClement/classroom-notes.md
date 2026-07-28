# SAGE development notes

These notes use placeholders so they are safe to share. Replace values such as
`your-node-id` locally; do not commit usernames, tokens, passwords, private
keys, or private file paths.

## 1. Local Python setup with `uv`

Develop and test the application on your computer first. Do not install Python
packages directly on a SAGE node.

Check whether `uv` is already installed:

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

For an existing project that already contains `pyproject.toml` and `uv.lock`:

```bash
uv sync
uv run python main.py
```

Useful commands:

```bash
uv add package-name
uv remove package-name
uv run python script.py
```

Official reference: [Installing uv](https://docs.astral.sh/uv/getting-started/installation/)

## 2. SAGE edge-app workflow

A SAGE edge app contains its code, dependencies, and models, packaged so that
it can be scheduled on Waggle nodes.

Use this order:

1. Develop and test the Python code locally.
2. Add the edge-app files, including `Dockerfile` and `sage.yaml`.
3. Build and test the packaged app on a development node.
4. Publish it to the Edge Code Repository (ECR).
5. Schedule it on a node and inspect the published results.

Do not run an app or install packages directly on a node. Use its Docker
container or the supported `pluginctl` workflow.

References:

- [Introduction to edge apps](https://sagecontinuum.org/docs/tutorials/edge-apps/intro-to-edge-apps)
- [Creating an edge app](https://sagecontinuum.org/docs/tutorials/edge-apps/creating-an-edge-app)
- [Developer quick reference](https://sagecontinuum.org/docs/reference-guides/dev-quick-reference)

## 3. Connect to a SAGE development node

### Prerequisite

Your SAGE account must have access to the target node, and the SAGE SSH setup
must already be installed on your computer. Access is managed in the
[SAGE portal](https://portal.sagecontinuum.org/account/access).

Set the node identifier for the current terminal session:

```bash
SAGE_NODE="your-node-id"
```

Check that the node is reachable:

```bash
ssh "waggle-dev-node-${SAGE_NODE}" hostname
```

Open an interactive shell:

```bash
ssh "waggle-dev-node-${SAGE_NODE}"
```

Run one command and disconnect:

```bash
ssh "waggle-dev-node-${SAGE_NODE}" tmux ls
```

Your SSH key passphrase may be requested. SSH handles it; never store the
passphrase in this repository.

## 4. Reconnect and update Hermes

Run these commands after connecting to the development node.

### Stop an old session

List the `tmux` sessions:

```bash
tmux ls
```

If a session named `hermes` exists:

```bash
tmux kill-session -t hermes
```

### Back up and update the profile

```bash
cp ~/.hermes/profiles/sage/config.yaml \
   ~/.hermes/profiles/sage/config.yaml.before-update

hermes profile update sage --force-config
```

The update may reset the selected model. It should not require copying an API
key into this repository.

### Verify Hermes

```bash
hermes profile info sage
hermes -p sage doctor
hermes -p sage skills list
hermes -p sage config get model
```

If the model is incorrect, select the intended provider and model
interactively:

```bash
hermes -p sage model
```

### Restart Hermes in `tmux`

```bash
tmux new -s hermes
hermes -p sage
```

To leave Hermes running, press `Ctrl-B`, release the keys, and then press `d`.

From outside `tmux`, a one-off prompt can be run with:

```bash
hermes -p sage -s sage-waggle -z "Your prompt"
```

## 5. Query SAGE data

Use the Python client for repeatable analysis and `curl` for quick terminal
checks. Both use the same SAGE query service.

### Quick HTTP query

Replace `your-node-id` and the task pattern:

```bash
curl https://data.sagecontinuum.org/api/v1/query \
  -d '{
    "start": "-1h",
    "filter": {
      "vsn": "your-node-id",
      "task": "your-task-pattern"
    }
  }'
```

### Protected downloads

Keep credentials outside source files and load them from the environment only
when needed:

```bash
curl -L -u "$SAGE_USERNAME:$SAGE_TOKEN" "$URL" -o output-file
```

Never print these variables or commit the `.env` file that defines them.

## 6. Store a secret in macOS Keychain

Keychain stores a secret without putting it in a script, configuration file,
or shell history.

Choose a non-sensitive service label:

```bash
KEYCHAIN_SERVICE="sage-api"
```

Add or update the entry. The final `-w` requests the secret interactively:

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

The retrieved value is still sensitive. Do not copy it into logs, screenshots,
source files, or committed shell configuration.
