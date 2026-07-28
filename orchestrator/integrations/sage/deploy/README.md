# Prepared deployment files

[Version française](README-fr.md)

These files are examples only. This repository never applies them.

Before using them, confirm the live namespace, Deployment/container names,
service account, image digest, Waggle commit, k3s version, and the upstream
`edge-scheduler` license.

The patch deliberately does not redefine RabbitMQ environment variables.
Confirm that the existing Deployment keeps its Secret-backed `RABBITMQ_URI`,
`RABBITMQ_USERNAME`, and `RABBITMQ_PASSWORD`; do not put credentials in this
ConfigMap or rely on the compatibility defaults inherited from Waggle.

Do not expose or call `POST /api/v1/schedule` from the pinned Waggle API
server. That upstream path does not fully initialize a `PluginRuntime` and can
race with queue iteration. Preserve or add network controls around port 8080
until the route is disabled or fixed upstream.

The policy is loaded once when `sage-nodescheduler` starts. Updating the
ConfigMap volume alone does not reload it. In the real chart or Kustomization,
generate a content-hashed ConfigMap name or inject its checksum into the Pod
template annotation
`scheduling.sagecontinuum.org/config-revision`.

For a controlled manual pilot, an operator must deliberately restart the
confirmed Deployment after changing the ConfigMap:

```sh
kubectl rollout restart deployment/wes-plugin-scheduler
kubectl rollout status deployment/wes-plugin-scheduler
```

Do not run those commands until the real WES deployment target has been
confirmed with the SAGE team.
