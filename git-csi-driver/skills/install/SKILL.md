---
name: install
description: "Install git-csi-driver from its kustomize base and set the plugin's flags. Use when a cluster must mount git repositories as volumes."
---

This skill is the guide at https://git.liken.sh/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

Install the driver from the kustomize base in the repository's
`deploy/` directory. You need a cluster with standard CSI plumbing,
`kubectl` with cluster-admin rights, and the `liken-system` namespace.

Add the base to your own kustomization and pin `<tag>` to a release,
so the install is the same every time you apply it. The base creates
the `CSIDriver` object, the `ServiceAccount`s and their roles, the
`DaemonSet` that runs the node plugin beside the kubelet's registrar,
and the `Deployment` that runs the controller plugin beside the
`external-resizer`. The `external-resizer` sends a claim's class
change to the driver.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: liken-system

resources:
  - https://github.com/liken-sh/git-csi-driver//deploy?ref=<tag>

images:
  - name: ghcr.io/liken-sh/git-csi-driver
    newTag: <tag>
```

Check that every node lists `git.liken.sh` among its drivers. A node
appears there after the registrar tells its kubelet about the plugin
and the kubelet calls the plugin. A node missing from the answer has
no plugin pod running yet.

```console
kubectl get csinode -o custom-columns=NODE:.metadata.name,DRIVERS:.spec.drivers[*].name
```

## The plugin's flags

One binary runs both plugins, and a subcommand picks which one runs.
The base passes `node` to the `DaemonSet` and `controller` to the
`Deployment`, and each subcommand accepts only its own flags. Change
a flag through a kustomize patch on the container's `args`.

`git-csi-driver node` takes these flags.

| Flag | Default | Meaning |
|---|---|---|
| `--endpoint` | `unix:///csi/csi.sock` | The socket the kubelet and the sidecars call. |
| `--node-id` | none | The node's name, which the base takes from the pod's `spec.nodeName`. |
| `--store` | `/var/lib/liken/pod-storage/git-csi` | Where the node plugin keeps its bare repositories, trees, and records. On `liken` this is the pod-storage partition. |
| `--metrics` | `:9200` | Where the node plugin serves its Prometheus metrics. An empty value serves none. |
| `--sweep-after` | `720h` | How long the plugin keeps a work tree that nothing stages, and how old an object that no ref names must be before `git gc` prunes it. |
| `--demand-min-interval` | `10s` | How long a demanded pull waits after the last pull of the same repository on the node. A burst of demands inside that interval costs one pull. |

`git-csi-driver controller` takes these flags.

| Flag | Default | Meaning |
|---|---|---|
| `--endpoint` | `unix:///csi/csi.sock` | The socket the sidecars call. |
| `--metrics` | `:9200` | Where the controller serves its Prometheus metrics. An empty value serves none. |
| `--webhook` | `:8080` | Where the controller serves the webhook listener. An empty value serves none. |

`git-csi-driver --version` prints the version and exits.

## The store's filesystem

The store has to be on a filesystem that survives a reboot. The
`hostPath` volume creates the store's directory wherever `--store`
leads. On a `liken` node with no pod-storage partition, that is the
root overlay, and the root overlay keeps its writes in memory.

The node plugin reads `/proc/self/mountinfo` once at start. It finds
the mount that holds the store. It refuses the store when that mount
is `tmpfs`, `ramfs`, or `rootfs`, or when it is an overlay whose upper
directory is on one of those. It also refuses an overlay whose upper
directory is on no disk in the pod's own mount table. From inside a
pod, the root overlay of a `liken` node reads that way. A node with a
refused store does these things:

* It logs `the node refuses writeable volumes` at the error level, with
  the reason.
* It sets `git_csi_store_refuses_writeable` to one.
* It answers `FailedPrecondition` to the stage of a writeable volume
  whose directory the store does not have yet. The kubelet writes the
  reason into the pod's events.

It still serves read-only volumes, because their trees are copies of
the remote, and a reboot costs one clone again. A writeable volume
whose tree the store already holds still stages, so its work can push
before a reboot deletes it. To serve new writeable volumes on that
node, point `--store` and the `store` `hostPath` in the base at a
directory on a disk.

The controller pod declares both ports, and the base includes a
`Service` named `git-csi-driver-webhook` on port 80 in front of the
webhook port. The [read-only guide](https://git.liken.sh/docs/guides/read-only/#webhooks) says how a
forge reaches it.
