# 01, The skeleton

Proposed. Full fidelity.

## The problem

The driver needs a repository with the same gates, workflows, site, and
release shape as the other `liken` repositories, and it needs a node
plugin that the kubelet accepts before any volume logic exists. The
skeleton is proved when a `liken` node lists `git.liken.sh` among its
CSI drivers.

## The design

### The repository

The repository follows `audio-operator`: one Go module, one flat
package at the root, one file per concern with a test file beside it,
`make test` as the whole gate, the coverage gate on a pinned toolchain,
the untested-package check, `ci.yaml` and `release.yaml` in the shared
shape, a Hugo site in `docs/` with the brand theme as a submodule, and
a kustomize base in `deploy/`.

The coverage floor starts at 90, the project's target, and rises with
measurement. The other repositories set their floors under a mature
measurement; a skeleton has none.

The image's final stage is Debian with `git`, `openssh-client`, and
`ca-certificates`, because the driver runs the git binary. A closure on
`scratch`, the way `audio-operator` ships its daemons, is a later plan.

### The node plugin

The program is `git-csi-driver`. Its flags are `--endpoint`, the
socket to serve, default `unix:///csi/csi.sock`; `--node-id`, the
node's name, which the `DaemonSet` supplies from the downward API;
`--store`, the directory for repositories and work trees, default
`/var/lib/liken/pod-storage/git-csi`; and `--version`.

It serves the CSI `Identity` and `Node` services over gRPC on the
socket, with `github.com/container-storage-interface/spec/lib/go/csi`
at the newest 1.x release and `google.golang.org/grpc`.

| RPC | Answer |
|---|---|
| `GetPluginInfo` | `name: git.liken.sh`, `vendor_version` from the build. |
| `GetPluginCapabilities` | `VOLUME_ACCESSIBILITY_CONSTRAINTS` is not declared. No `CONTROLLER_SERVICE` yet; plan 05 adds it. |
| `Probe` | `ready: true` once the store directory exists and is writeable. |
| `NodeGetInfo` | `node_id` from `--node-id`. No topology. |
| `NodeGetCapabilities` | `STAGE_UNSTAGE_VOLUME`, `GET_VOLUME_STATS`, `VOLUME_CONDITION`. |
| Every other `Node` RPC | `codes.Unimplemented`, with a message that names the plan that adds it. |

The server logs each RPC's name and result at one line per call. The
kubelet's calls are the driver's whole input, and a person reading the
log has to see them.

### The manifests

`deploy/` holds:

- `csidriver.yaml`: the `CSIDriver` object, `attachRequired: false`,
  `podInfoOnMount: true`, `fsGroupPolicy: None`,
  `volumeLifecycleModes: [Persistent, Ephemeral]`,
  `storageCapacity: false`.
- `rbac.yaml`: a `ServiceAccount` `git-csi-driver` in `liken-system`.
  No roles yet; plan 03 adds `events` and plan 04 adds claims.
- `node.yaml`: a `DaemonSet` `git-csi-driver-node` with two
  containers. `driver` runs the image with `--node-id=$(NODE_NAME)`
  from the downward API, privileged, because bind mounts under the
  kubelet's pod directory need it. `registrar` runs
  `registry.k8s.io/sig-storage/csi-node-driver-registrar` at its
  newest release with `--csi-address=/csi/csi.sock` and
  `--kubelet-registration-path=/var/lib/kubelet/plugins/git.liken.sh/csi.sock`.
  Volumes: `hostPath` `/var/lib/kubelet/plugins/git.liken.sh` at
  `/csi`, `hostPath` `/var/lib/kubelet/plugins_registry` at
  `/registration`, `hostPath` `/var/lib/kubelet/pods` at the same path
  with `mountPropagation: Bidirectional`, and `hostPath`
  `/var/lib/liken/pod-storage/git-csi` at the same path for the store. Priority
  class `system-node-critical`. Tolerations for every taint, the way
  a `DaemonSet` on `liken` tolerates the node taints. Update strategy
  `RollingUpdate` with `maxUnavailable: 1`.
- `kustomization.yaml`: namespace `liken-system`, the shared labels,
  and the image entry with `newTag: latest`.

Plan 02's drill checked the store's mount with `findmnt`. The first
path, `/var/lib/liken/git-csi`, fell through to the root overlay, which
is RAM. On `liken` the disk partitions are `/var/lib/rancher` for
cluster state, `/var/lib/kubelet` for pod ephemeral storage, and
`/var/lib/liken/pod-storage` for pod volumes. The store lives on the
last one, because that is what it holds.

### The site

The site has a home page, a manual landing page, an install guide, and
guide and reference sections that later plans fill. The install guide
states the kustomize base, the development-build pin, and the
`kubectl get csinode` check.

## Considered and set aside

- **A controller plugin in the skeleton.** Nothing needs it until plan
  05, and a `Deployment` with no RPCs is noise in the lab.
- **`go-git` instead of the git binary.** The driver's behavior has to
  match what a person sees on the forge, and the git binary is that
  behavior. A pure Go image is not worth a second implementation of
  fetch, rebase, and push.
- **The `livenessprobe` sidecar.** The registrar already fails when the
  socket does not answer, and the `DaemonSet` restarts the pod. A third
  container buys nothing on a 1 GiB node.

## Proof

- `make test` passes locally and in CI, and the coverage gate holds.
  The tests start the gRPC server on a socket in a temporary directory
  and call every RPC through a real client.
- A push to main publishes the site and a development image.
- In the lab, the `DaemonSet` is `Ready`, and `kubectl get csinode`
  lists `git.liken.sh` on the node. That drill belongs to plan 02,
  which needs this plan's image.
