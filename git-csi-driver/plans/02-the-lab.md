# 02, The lab

Proposed. Full fidelity.

## The problem

The driver cannot be installed on a real cluster until a person has
seen it work, so every drill needs a cluster that exists only for this
repository. The lab is one `liken` machine in QEMU, booted from the
public release channel, and a git forge on the host that the machine
can reach.

## The design

### The machine

`lab/` holds `cluster.yaml`, a `Cluster` named `lab` with one leader,
`node-1`, and `machines/node-1.yaml`, its `Machine`, in the shape of
`liken`'s single-node `gitops-cluster/`. `releases.source` is
`https://releases.liken.sh`. The `Machine` has `rebootPolicy: Auto`,
two interfaces (`eth0` for the user-mode network, `eth1` with the
cluster address), and the seven storage roles a UEFI node needs on
three virtio disks: `state.qcow2` for `machineState` and
`clusterState`, `pods.qcow2` for `machineEphemeral`, `podStorage`,
and `podEphemeral`, and `boot.qcow2` for `systemA` and `systemB`. No
`features.flux`: manifests reach the lab with `kubectl` from the host.

### The Makefile

`lab/Makefile` copies the targets, disk names, firmware paths, network
devices, and MAC scheme of `liken`'s `dev-cluster/Makefile`, for one
node, and replaces the in-repo build with the public channel:

| Target | What it does |
|---|---|
| `fetch` | Reads `channel.yaml`, downloads that version's `liken` toolkit and `release.yaml`, and runs `liken fetch` into `channel/<version>/`. A `VERSION=` override pins a version. |
| `identity` | `liken mint identity/`. Idempotent. |
| `media` | `liken layer` the manifests and identity into `deployment.cpio`, then `liken media` into `install.cpio`, with the fetched release's own toolkit. |
| `install` | Creates the three disks and the NVRAM copy under `guests/node-1/`, boots `install.cpio` with `-kernel`, waits for the install verdict on the serial console, and stops QEMU. |
| `run` | Boots from disk with 1 GiB, the API port forwarded to `127.0.0.1:16443`, and the console to `guests/node-1/console.log`. Runs QEMU in the background and writes its pid. |
| `stop` | Stops the guest by pid. |
| `kubeconfig` | `liken kubeconfig -server https://127.0.0.1:16443 .` into `identity/kubeconfig`. |
| `wait` | Polls `kubectl get nodes` until `node-1` is `Ready`, with a five-minute deadline. |
| `forge` | Starts `git daemon` on the host, serving `forge/` on port 9418 with `receive-pack` enabled, in the background with a pid file. `forge-stop` stops it. |
| `repo NAME=<name>` | Creates `forge/<name>.git`, a bare repository seeded from `fixtures/<name>/` with one commit on `main`. |
| `deploy` | Applies `deploy/` with the image pinned to `IMAGE_TAG`, and waits for the `DaemonSet` to be ready with a deadline. |
| `smoke` | `clean`, `fetch`, `identity`, `media`, `install`, `run`, `kubeconfig`, `wait`, `forge`, `repo NAME=hello`, `deploy`, then `kubectl get csinode` shows `git.liken.sh`. Fails on the first deadline. |
| `clean` | Stops the guest and the forge and removes `guests/`. `distclean` also removes `channel/` and `identity/`. |

`kubectl` is the wrapper `lab/kubectl`, which sets `KUBECONFIG` to the
lab's identity, so a drill never reaches another cluster.

The guest reaches the host at `10.0.2.2`, the user-mode network's
gateway. A repository on the forge is `git://10.0.2.2:9418/<name>.git`
from the guest, and `git://127.0.0.1:9418/<name>.git` from the host.
`git daemon` needs no credentials, so lab volumes name no `Secret`;
credentials are drilled in plan 03 with a second forge over SSH only if
a lab needs it.

`channel/`, `identity/`, `guests/`, `forge/`, `deployment.cpio`, and
`install.cpio` are ignored by git. `fixtures/` is committed.

### What the drill checks about the store

The skeleton stores repositories at `/var/lib/liken/git-csi`. The smoke
target's last step runs a privileged pod on `node-1` with `findmnt
--target /var/lib/liken/git-csi` and prints the source. If the
directory is not on the cluster-state partition, the path moves, in
plan 01's manifests and in the program's default, before plan 03 lands.

### Memory

The guest has 1 GiB, the `liken` envelope. The `DaemonSet` requests
32 MiB and limits itself to 128 MiB. A driver that does not fit there
is a bug, not a reason to raise the guest's memory.

## Considered and set aside

- **gitea in the lab cluster.** It is the shape the house cluster has,
  but it costs a database and a second image on a 1 GiB guest, and
  `git daemon` exercises the same driver code.
- **A stock distribution with k3s.** Lighter, but the driver's store
  lives on `liken`'s cluster-state partition and its shutdown push
  depends on `liken`'s stop order. The lab has to be the real target.
- **A registry on the host for locally built images.** Every drill
  runs a development build that CI already pushed to `ghcr.io`, so
  the lab pulls the same image a person would. The cost is one CI
  round trip per change.

## Proof

- `make -C lab smoke` ends with one `Ready` node, a `Ready`
  `DaemonSet`, `git.liken.sh` in `kubectl get csinode`, and the
  store's mount source printed, inside the deadlines, from a clean
  checkout on `vega`.
- A pod in the lab clones `hello` from the host forge and pushes a
  commit to it.
