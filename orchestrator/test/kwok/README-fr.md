# Test de fumée du plan de contrôle KWOK (optionnel)

[English version](README.md)

Ce jeu de test vérifie les objets d’ordonnancement Kubernetes et les labels
compatibles avec SAGE à l’échelle du plan de contrôle. Il n’exécute **ni** les
conteneurs de plugins, **ni** les capteurs, **ni** les GPU, **ni** le
vérificateur de règles Waggle, **ni** RabbitMQ, **ni** le processus
`sage-nodescheduler`. Utilisez-le uniquement comme complément préliminaire aux
tests purs de la politique.

Base de compatibilité épinglée :

- KWOK et `kwokctl` : `v0.8.0`
- Kubernetes : `v1.23.17`

Kubernetes `v1.23.17` correspond volontairement aux bibliothèques clientes
`k8s.io/* v0.23.1` utilisées par l’intégration épinglée de Waggle
edge-scheduler.

## Sécurité

Ces commandes sont volontairement optionnelles et utilisent un kubeconfig
dédié. Ne remplacez jamais ce kubeconfig par un fichier pointant vers un site
SAGE.

```sh
export RUN_KWOK_TESTS=1
export KWOK_VERSION=v0.8.0
export KWOK_KUBE_VERSION=v1.23.17
export KWOK_CLUSTER_NAME=sage-policy-mvp
export KUBECONFIG="$PWD/test/kwok/.state/kubeconfig"

test "$RUN_KWOK_TESTS" = "1"
test "$(kwokctl --version | grep -c 'v0.8.0')" -ge 1
mkdir -p "$(dirname "$KUBECONFIG")"
```

Installez la version officielle `v0.8.0` de `kwokctl` pour votre plateforme
avant de continuer. N’utilisez pas de binaire `latest` non épinglé.

## Créer le cluster isolé

```sh
test "$RUN_KWOK_TESTS" = "1"
kwokctl create cluster \
  --name "$KWOK_CLUSTER_NAME" \
  --kubeconfig "$KUBECONFIG"

kubectl --kubeconfig "$KUBECONFIG" apply -f test/kwok/nodes.yaml
kubectl --kubeconfig "$KUBECONFIG" apply -f test/kwok/workloads.yaml

kubectl --kubeconfig "$KUBECONFIG" get nodes -o wide
kubectl --kubeconfig "$KUBECONFIG" \
  get pods -n sage-policy-kwok -o wide
```

Placement attendu :

- `routine-image-sampler` est affecté à `sage-edge-cpu-0`.
- `urgent-cloud-cover` est affecté à `sage-edge-gpu-0`.

Ce placement vérifie uniquement les sélecteurs Kubernetes et les ressources
GPU étendues. Exécutez `make example` séparément pour vérifier que la politique
d’urgence choisit la tâche urgente avant la tâche de routine.

## Panne GPU contrôlée

Marquez le nœud GPU simulé comme non planifiable, recréez le Pod urgent et
vérifiez qu’il reste Pending. Le rendre de nouveau planifiable doit permettre
son ordonnancement.

```sh
test "$RUN_KWOK_TESTS" = "1"
kubectl --kubeconfig "$KUBECONFIG" cordon sage-edge-gpu-0
kubectl --kubeconfig "$KUBECONFIG" \
  delete pod urgent-cloud-cover -n sage-policy-kwok --ignore-not-found
kubectl --kubeconfig "$KUBECONFIG" apply -f test/kwok/workloads.yaml
kubectl --kubeconfig "$KUBECONFIG" \
  get pod urgent-cloud-cover -n sage-policy-kwok -o wide

kubectl --kubeconfig "$KUBECONFIG" uncordon sage-edge-gpu-0
kubectl --kubeconfig "$KUBECONFIG" \
  get pod urgent-cloud-cover -n sage-policy-kwok -o wide
```

## Nettoyage

```sh
test "$RUN_KWOK_TESTS" = "1"
kwokctl delete cluster \
  --name "$KWOK_CLUSTER_NAME" \
  --kubeconfig "$KUBECONFIG"
```

Le patch du Deployment SAGE sous `integrations/sage/deploy/` est
volontairement indépendant de ce jeu de test et ne doit pas être appliqué au
cluster KWOK.
