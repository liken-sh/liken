---
name: install
description: "Install git-csi-driver from its kustomize base and set the plugin's flags. Use when a cluster must mount git repositories as volumes."
---

This skill is the guide at https://liken.sh/git/docs/guides/install/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Install the driver

At the end of this guide, every node runs the driver's node plugin
and can mount git repositories as volumes. You need `kubectl` with
cluster-admin rights and the `liken-system` namespace.

Add the kustomize base in the driver's `deploy/` directory to your own
kustomization, with `<tag>` set to the driver's version. The base
creates:

* the `CSIDriver` object, and the `ServiceAccount`s and their roles.
* a `DaemonSet` that runs the node plugin and the
  `csi-node-driver-registrar`, which registers the plugin with the kubelet.
* a `Deployment` that runs the controller plugin with the
  `external-resizer` and the `external-provisioner`. The
  `external-resizer` sends a change of a claim's class to the driver.
  The `external-provisioner` creates no volumes. It only deletes a
  released `PersistentVolume` that has the `Delete` reclaim policy.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/liken-sh/liken//git-csi-driver/deploy?ref=<tag>

images:
  - name: ghcr.io/liken-sh/git-csi-driver
    newTag: <tag>
```

The driver's version is the tag of the release that last published
it. The [GitHub release](https://github.com/liken-sh/liken/releases) for each
tag lists every component and its version.

The base puts its objects in `liken-system`, so your kustomization
needs no `namespace` field. That field would also move the dashboard
of the `deploy/monitoring` component out of the `monitoring`
namespace.

Apply your kustomization. Then check that every node lists
`git.liken.sh` among its drivers. A node lists the driver after its
node plugin pod is running and the registrar has registered it with
the kubelet, so a node without it has no running plugin pod yet.

```console
kubectl get csinode -o custom-columns=NODE:.metadata.name,DRIVERS:.spec.drivers[*].name
```

## The plugin's flags

One binary runs both plugins. The subcommand `node` runs the node
plugin, and `controller` runs the controller plugin. Each subcommand
accepts only its own flags. To change a flag, write a kustomize patch
on the container's `args`.

`git-csi-driver node` takes these flags.

| Flag | Default | Meaning |
|---|---|---|
| `--endpoint` | `unix:///csi/csi.sock` | The socket the kubelet and the sidecars call. |
| `--node-id` | none, required | The node's name. The base sets it from the pod's `spec.nodeName`. |
| `--store` | `/var/lib/liken/pod-storage/git-csi` | The directory where the node plugin keeps its bare repositories and work trees. On `liken` this directory is on the pod-storage partition. |
| `--metrics` | `:9200` | The address where the node plugin serves its Prometheus metrics. An empty value turns the metrics off. |
| `--demand-min-interval` | `10s` | The shortest time between two demanded pulls of the same repository on the node. Demands that arrive inside that interval cause one pull. |

`git-csi-driver controller` takes these flags.

| Flag | Default | Meaning |
|---|---|---|
| `--endpoint` | `unix:///csi/csi.sock` | The socket the sidecars call. |
| `--metrics` | `:9200` | The address where the controller serves its Prometheus metrics. An empty value turns the metrics off. |
| `--webhook` | `:8080` | The address where the controller receives webhooks from a forge. An empty value turns the webhook off. |

`git-csi-driver --version` prints the version and exits.

The base includes a `Service` named `git-csi-driver-webhook` that
forwards port 80 to the controller's webhook port. The [read-only
guide](https://liken.sh/git/docs/guides/read-only/#webhooks) describes how a forge sends requests to it.

The base runs one controller pod. To run two, patch the `Deployment`
named `git-csi-driver-controller` to `replicas: 2`. Either pod can
receive a webhook. The two sidecars elect a leader through a `Lease`
in `liken-system`, so only one pod at a time writes a claim's status
or deletes a `PersistentVolume`. The `external-resizer` uses the
`Lease` named `external-resizer-git-liken-sh`, and the
`external-provisioner` uses `git-liken-sh`.
