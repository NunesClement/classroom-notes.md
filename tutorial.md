# tutorial.md
```bash
personnal notes (formatted using an llm)
```

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

# API & code

print(df.head())
```

Run the repo examples:

```bash
uv run python temperature.py
uv run python avg-temp.py
uv run python avg-temp-5-mn.py
uv run python urls.py
```

What they do:

- `temperature.py`: query one node;
- `avg-temp.py`: average one hour of temperature data;
- `avg-temp-5-mn.py`: show recent temperatures across nodes;
- `urls.py`: find and download the latest five images from `W06C`.

`urls.py` reads `NAME` and `SAGE_TOKEN` from `.env`.
Do not put their values in the script.

### Way 2 — HTTP with curl

Query image-sampler data directly:

```bash
curl https://data.sagecontinuum.org/api/v1/query \
  -d '{
    "start":"-1h",
    "filter":{
      "vsn":"W06C",
      "task":"imagesampler-.*"
    }
  }'
```

Get the latest image URL:

```bash
URL=$(curl -s \
  https://data.sagecontinuum.org/api/v1/query \
  -d '{
    "start":"-1h",
    "filter":{
      "vsn":"W06C",
      "task":"imagesampler-mobotix"
    }
  }' \
  | jq -r '.value' \
  | tail -1)
```

Check the URL, then download the file:

```bash
echo "$URL"
curl -L "$URL" -o ~/Downloads/picture.jpg
open ~/Downloads/picture.jpg
```

`open` is for macOS. On Linux, use `xdg-open` instead.

For a protected download, use the credentials from the environment:

```bash
curl -L \
  -u "$NAME:$SAGE_TOKEN" \
  "$URL" \
  -o ~/Downloads/picture.jpg
```

Get the latest audio URL and download it:

```bash
URL=$(curl -s \
  https://data.sagecontinuum.org/api/v1/query \
  -d '{
    "start":"-24h",
    "filter":{
      "vsn":"W06C",
      "name":"upload",
      "task":".*audio.*"
    }
  }' \
  | jq -r '.value' \
  | tail -1)

curl -L "$URL" -o ~/Downloads/audio.flac
```

## Which API way to use?

Use the Python client for analysis or repeatable scripts. It turns the
response into a pandas DataFrame, which makes numeric conversion,
grouping, sorting, averaging, and multi-file downloads easier.

Use `curl` for a fast terminal check or a shell script. It has no Python
setup, shows the API response directly, and combines well with `jq`,
`tail`, and other command-line tools.

The Python client is not a separate data service. It sends the query to
the same `/api/v1/query` endpoint, then parses the newline-delimited JSON
response into a DataFrame.

In short: use `curl` to inspect or fetch something quickly; use Python
when the result needs processing, reuse, or error handling.

---

# Stocker un mot de passe dans le Trousseau macOS

Le Trousseau macOS (`Keychain`) permet de conserver des mots de passe et
d'autres secrets de façon chiffrée, au lieu de les enregistrer en clair dans
un script, un fichier de configuration ou une variable d'environnement
persistante. macOS contrôle également quelles applications peuvent accéder à
chaque entrée et peut demander une confirmation ou une authentification avant
de révéler un secret.

## Ajouter une entrée

Pour créer une entrée nommée `test`, associée à l'utilisateur courant, et lui
attribuer le mot de passe `1234` :

```bash
security add-generic-password \
  -a "$USER" \
  -s "test" \
  -w "1234" \
  -U
```

L'option `-U` met à jour l'entrée si elle existe déjà. Sans cette option, la
commande échoue lorsqu'une entrée portant le même nom est déjà présente.

> **Attention**
>
> Écrire `1234` directement dans la commande l'enregistre généralement dans
> l'historique du terminal. La méthode recommandée consiste à demander le mot
> de passe de manière interactive :

```bash
security add-generic-password \
  -a "$USER" \
  -s "test" \
  -U \
  -w
```

Saisir `1234` lorsque macOS le demande. Placer `-w` en dernière position, sans
valeur, déclenche la saisie interactive et évite d'ajouter le mot de passe à
l'historique du terminal.

## Récupérer le mot de passe

```bash
security find-generic-password \
  -a "$USER" \
  -s "test" \
  -w
```

L'option `-w` affiche uniquement le mot de passe. Cette sortie reste sensible :
éviter de la copier dans des journaux, des captures d'écran ou des scripts qui
l'enregistreraient en clair.

## Pourquoi utiliser le Trousseau sur macOS ?

- les secrets sont stockés dans un espace chiffré géré par macOS ;
- les droits d'accès peuvent être limités par application ;
- le Trousseau peut demander le mot de passe de session ou Touch ID avant
  d'autoriser un accès ;
- les scripts peuvent récupérer un secret au moment où ils en ont besoin, sans
  l'intégrer directement à leur code ;
- une entrée peut être mise à jour ou supprimée de manière centralisée sans
  modifier chaque script qui l'utilise.
