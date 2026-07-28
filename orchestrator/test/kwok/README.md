# KWOK control-plane smoke test (opt-in)

[Version française](README-fr.md)

This fixture checks Kubernetes scheduling objects and SAGE-compatible labels at
control-plane scale. It does **not** run plugin containers, sensors, GPUs, the
Waggle rule checker, RabbitMQ, or the `sage-nodescheduler` process. Use it only
as a preliminary complement to the pure policy tests.

Pinned compatibility baseline:

- KWOK and `kwokctl`: `v0.8.0`
- Kubernetes: `v1.23.17`

Kubernetes `v1.23.17` intentionally matches the `k8s.io/* v0.23.1` client
libraries used by the pinned Waggle edge-scheduler integration.

## Safety

These commands are deliberately opt-in and use a dedicated kubeconfig. Never
replace that kubeconfig with one pointing to a SAGE site.

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

Install the official `kwokctl` `v0.8.0` release for your platform before
continuing. Do not use an unpinned `latest` binary.

## Create the isolated cluster

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

Expected placement:

- `routine-image-sampler` is assigned to `sage-edge-cpu-0`.
- `urgent-cloud-cover` is assigned to `sage-edge-gpu-0`.

This placement checks Kubernetes selectors and extended GPU resources only.
Run `make example` separately to verify that the urgency policy chooses the
urgent task before the routine task.

## Controlled GPU outage

Cordon the simulated GPU node, recreate the urgent Pod, and observe that it
stays Pending. Uncordoning the node should let it schedule again.

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

## Cleanup

```sh
test "$RUN_KWOK_TESTS" = "1"
kwokctl delete cluster \
  --name "$KWOK_CLUSTER_NAME" \
  --kubeconfig "$KUBECONFIG"
```

The SAGE Deployment patch under `integrations/sage/deploy/` is intentionally
unrelated to this fixture and must not be applied to the KWOK cluster.
