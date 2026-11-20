# Inspect and change a running machine

Contents:

* A busybox pod for a machine's filesystem
* Enable or remove a feature with a live patch
* Reboot a machine on purpose

Run every command here from `liken/`, as in `SKILL.md`.

## A busybox pod for a machine's filesystem

A `liken` machine has no shell and no SSH. `image/oci.sh` packages
exactly one static binary in each image, so `kubectl exec` into
`liken.sh/iscsid`, the operators, or the log relays always fails. To
read or write a machine's filesystem, apply a busybox pod:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: drill
  namespace: default
spec:
  hostNetwork: true
  nodeSelector: {liken.sh/machine: "true"}
  tolerations: [{operator: Exists}]
  containers:
    - name: drill
      image: busybox:1.38.0
      command: ["sh", "-c", "sleep infinity"]
      securityContext: {privileged: true}
      volumeMounts:
        - {name: dev, mountPath: /dev}
        - {name: sbin, mountPath: /host-sbin}
        - {name: run-liken, mountPath: /run/liken}
  volumes:
    - {name: dev, hostPath: {path: /dev}}
    - {name: sbin, hostPath: {path: /sbin}}
    - {name: run-liken, hostPath: {path: /run/liken}}
```

Each field is required:

* `nodeSelector: {liken.sh/machine: "true"}` selects the label that
  every `liken` node registers with. Without it, the pod can run on a
  node that has no `liken` image. When the drill reads one machine,
  replace the `nodeSelector` with `nodeName: node-2`, the name of that
  node. The scheduler does not place a pod that sets `nodeName`, so
  the pod runs on that node even when the node is not ready.
* `tolerations: [{operator: Exists}]` lets the pod run on a cordoned
  node or a node that is not ready yet. That is when a drill matters
  most.
* `privileged: true` is required to read or write a raw block device.
  Without it, the device cgroup blocks `dd` against `/dev/vdc`, and
  `dd` fails silently.
* `hostNetwork: true` puts the pod in the host's network namespace.
  The abstract unix socket of `iscsid` is in that namespace.
* Host `/sbin`, mounted at `/host-sbin`, lets you run `liken`'s own
  vendored tools: `/host-sbin/iscsiadm` and `/host-sbin/mount.nfs`.
* Host `/run/liken` contains the facts tree that init publishes. The
  operator writes its requests to init in `/run/liken/operator/`.

The lab's guests reach the internet through their uplink network
card, so the pull of `busybox:1.38.0` works with no registry mirror.

## Enable or remove a feature with a live patch

To enable a feature on an installed machine, patch the live Cluster.
An edit to `dev-cluster/cluster.yaml` does nothing, because the seed
manifests apply only on a machine's first boot. After that, init
follows the Cluster resource.

```bash
dev-cluster/kubectl patch cluster lab --type=merge \
    -p '{"spec":{"features":{"metrics-server":{},"iscsi":{}}}}'
```

A merge patch merges the keys of a map. It does not replace the map.
`spec.features` is a map, so if you send a shorter map, no feature is
removed, and nothing tells you so. To remove a feature, set it to an
explicit null:

```bash
dev-cluster/kubectl patch cluster lab --type=merge \
    -p '{"spec":{"features":{"flux":null}}}'
```

The same rule applies one level down: `{"flux":{"prune":null}}`
restores the default of one parameter. `cluster/features.go` lists
the features: `traefik`, `servicelb`, `metrics-server`, `helm`,
`network-policy`, `iscsi`, `nfs`, and `flux`. A vendored feature takes
effect when k3s restarts in place. The console prints
`liken: modules: <mod>: loaded` and then
`liken: features: <slug>: active`.

## Reboot a machine on purpose

To reboot a machine, write `/run/liken/operator/reboot-intent.yaml`
from the drill pod. Only PID 1 can shut a machine down, so the
operator asks init for a reboot through this file. The reason value
must be valid YAML, and a bare colon breaks the parse. A broken file
still reboots the machine, but init logs "an unreadable reboot
intent", and the reason is lost.

Do not use a sysctl edit to trigger a reboot drill. The operator
applies sysctls live and does not reboot.
