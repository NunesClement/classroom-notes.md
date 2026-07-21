# classroom-notes.md

# Reconnect and Update Hermes on H01D

## 1. Reconnect to the node

```bash
ssh waggle-dev-node-H01D
```

---

## 2. Remove any existing Hermes tmux session

List active tmux sessions:

```bash
tmux ls
```

If a session named `hermes` exists, stop it:

```bash
tmux kill-session -t hermes
```

---

## 3. Back up the current configuration

```bash
cp ~/.hermes/profiles/sage/config.yaml \
   ~/.hermes/profiles/sage/config.yaml.before-update
```

---

## 4. Update the Sage profile

```bash
hermes profile update sage --force-config
```

> **Note**
>
> The `.env` file and the NVIDIA API key should remain unchanged.
> However, `--force-config` may reset the selected model to the profile default.

---

## 5. Verify the installation

Display profile information:

```bash
hermes profile info sage
```

Run diagnostics:

```bash
hermes -p sage doctor
```

List installed skills:

```bash
hermes -p sage skills list
```

---

## 6. Verify the active model

Check the configured model:

```bash
hermes -p sage config get model
```

If the output is **not**:

```
z-ai/glm-5.2
```

reconfigure the model:

```bash
hermes -p sage model
```

Choose:

1. **NVIDIA NIM**
2. **Keep existing API key**
3. **z-ai/glm-5.2**

---

## 7. Restart Hermes inside tmux

Create a new tmux session:

```bash
tmux new -s hermes
```

Start Hermes:

```bash
hermes -p sage
```

Detach while leaving Hermes running:

- `Ctrl-B`
- release
- `d`

---

# Running Hermes from outside tmux

Example command:

```bash
hermes -p sage -s sage-waggle -z \
  "Explain in five bullets how data moves from a Sage node to Beehive."
```
