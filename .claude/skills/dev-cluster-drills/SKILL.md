---
name: dev-cluster-drills
description: Runs, upgrades, breaks, and inspects liken's dev cluster, the QEMU fleet under dev-cluster/ whose Cluster resource is named lab. Use when a session must boot or stop lab guests, build a lab release with make release VERSION=yyyy.mm.dd-9xx, drive an upgrade or fallback drill, reach the lab with kubectl or stern, read a running machine's filesystem or disks, re-found the fleet after a reinstall, or probe a CRD schema or CEL change.
---

# Dev-cluster drills

The dev cluster is the deployment liken develops against. Break it
freely. It carries no work worth keeping, and
`make -C dev-cluster clean` puts every guest back to blank disks.

Longer flows live beside this file:

* Old-release baselines, deliberate-failure releases, the metal
  hardware shape, and reading a guest's disks offline:
  [references/cross-version-drills.md](references/cross-version-drills.md)
* Testing a CRD schema or CEL change before you commit it:
  [references/schema-drills.md](references/schema-drills.md)

## What the dev cluster is

`dev-cluster/` holds five machines, `node-1` through `node-5`, each a
QEMU guest. `dev-cluster/cluster.yaml` declares the Cluster resource
named `lab`: `node-1`, `node-2`, and `node-3` are leaders, and
`node-1` is the founding leader. `node-4` and `node-5` are followers.
Every machine's `rebootPolicy` is `Auto`, so no drill in this lab
needs `liken approve-reboot`.

Each guest keeps its hardware under `dev-cluster/guests/<node>/`:
`state.qcow2` (2G), `pods.qcow2` (4G), `boot.qcow2` (4G), and, under
UEFI, its own `OVMF_VARS.fd` firmware variable store. The directory is
gitignored and holds no source.

Each guest has two NICs. `eth0` is a QEMU user-mode uplink on
10.0.2.0/24 with NAT, DHCP, and DNS, so a guest can pull an image from
the internet. `eth1` is the cluster segment, a multicast socket that
every guest joins, carrying 10.10.0.0/24. Only `node-1` forwards a
host port: 127.0.0.1:16443 reaches its API server on 6443.

Defaults in `dev-cluster/Makefile`: `FIRMWARE=uefi`, `BOOT=disk`,
`HARDWARE=virtio`, `SMP=4`, `CONSOLE=stdio`, and `MEM` 1024 on a disk
boot or 4096 on a `BOOT=kernel` boot.

## Reach the cluster

Mint the admin credential once, from the repo root:

```bash
make kubeconfig
```

That writes `dev-cluster/identity/kubeconfig` pointed at
`https://127.0.0.1:16443`. After that, use the wrappers:

```bash
dev-cluster/kubectl get nodes
dev-cluster/kubectl get machines
dev-cluster/kubectl get cluster lab -o yaml
dev-cluster/stern -n liken-system liken-machine-operator
```

The CLI passthrough reaches the same cluster and needs no wrapper:

```bash
cli/dist/liken kubectl -server https://127.0.0.1:16443 dev-cluster get machines
```

## Where consoles land

`make run` sends the guest's serial console to `stdio`, which is the
running task's own output. It does **not** write a file. Grepping
`guests/<node>/console.log` after a plain `make run` reads a stale log
from some earlier drill.

To keep a console, name the file:

```bash
make run NODE=node-2 CONSOLE=file:guests/node-2/console.log
```

`smoke.sh` always writes files. It keeps one pair per boot under
`dev-cluster/guests/node-1/`: `console.log` and `qemu.log` for the disk
boot, `install-console.log` and `install-qemu.log` for the install, and
`report-console.log` and `report-qemu.log` for the hardware report. The
console file is the machine's own output. The QEMU file is the only
record of a boot that failed before the console produced anything.

## Start and stop guests

Start a guest from the repo root, so make builds the artifacts first:

```bash
make run NODE=node-1
make run NODE=node-4
```

Run each guest in its own task. Launch it under `setsid` and in the
background, because a background wrapper sends SIGTERM to its child
QEMU when the harness cleans up a stopped task, and that drops the
guest:

```bash
setsid make run NODE=node-2 CONSOLE=file:guests/node-2/console.log \
    </dev/null >/tmp/node-2.make.log 2>&1 &
```

A `CONSOLE=file:` path is relative to `dev-cluster/`, whichever
directory you start make from. The root target hands the build off to
`make -C dev-cluster`, so the recipe always runs from there.

**Never use `pkill -f` or `pgrep -f`.** The pattern matches the Bash
tool's own `bash -c` wrapper, because the pattern text sits in that
shell's own command line. The shell dies mid-command, later steps of
`a; b && c` never run, and the exit code is 1 or 144 with output
missing. When the pattern also matches a guest, the guest dies too and
the failure reads as host flakiness.

Match on `comm` instead, which the kernel truncates to 15 characters,
so `qemu-system-x86_64` matches as `qemu-system-x86`:

```bash
# every guest
pkill -x qemu-system-x86

# one guest; the bracket class keeps the calling shell out of the match
pgrep -x qemu-system-x86 -a | grep 'node-[2]' | awk '{print $1}' | xargs -r kill
```

Put a timeout on anything that waits. Poll with a deadline, never with
an unbounded loop:

```bash
deadline=$(( $(date +%s) + 300 ))
while (( $(date +%s) < deadline )); do
    dev-cluster/kubectl get nodes --no-headers 2>/dev/null | grep -q Ready && break
    sleep 5
done
```

**QEMU reads `-kernel` and `-initrd` once, at process start,** and
restores them from its own ROM cache on every guest reset. A
`BOOT=kernel` guest that reboots comes back on the same initramfs it
started with, even after make rebuilt the file. To deliver new code to
such a guest, kill its QEMU process and start it again. A `BOOT=disk`
guest does not have this problem, because it loads from its own slots.

## The upgrade drill

This is the full forward roll. It needs a running fleet.

**Step 1. Build a lab release.**

```bash
make release VERSION=$(date +%Y.%m.%d)-901
```

`VERSION` has no default and `make release` alone fails. The serial is
three digits, checked by a regular expression in `releases/Makefile`
before any build starts. Use a `-9xx` serial for a lab build. Serial
`000` is taken by the working-tree channel the media targets bundle,
and published releases run from `001` up, so `-9xx` collides with
neither and is recognizable at a glance.

The build ends by printing the catalog entry:

```
catalog entry for a Cluster's spec.releases.catalog:
  - version: 2026.08.12-901
    digest: sha256:<64 hex digits>
```

The digest is the sha256 of that release's `release.yaml` bytes. It is
the start of the trust chain: the Cluster names the document, and the
document names every artifact.

**Step 2. Serve the channel.**

```bash
make serve
```

This serves `releases/dist` on `:8017` under the path `/releases/`.
Guests reach the host's loopback at 10.0.2.2, so the URL from inside a
guest is `http://10.0.2.2:8017/releases`, which is exactly the
`spec.releases.source` that `dev-cluster/cluster.yaml` declares. Leave
this task running. It logs every fetch, so a stalled or repeated
download is visible as it happens.

**Step 3. Patch the Cluster.**

```bash
dev-cluster/kubectl patch cluster lab --type=merge -p '{
  "spec": {
    "version": "2026.08.12-901",
    "releases": {
      "catalog": [
        {"version": "2026.08.11-901", "digest": "sha256:<old>"},
        {"version": "2026.08.12-901", "digest": "sha256:<new>"}
      ]
    }
  }
}'
```

Three rules govern this patch:

* **A JSON merge patch replaces an array.** `spec.releases.catalog` is
  an array, so resend every entry you want to keep. Dropping the entry
  the fleet runs now strands any machine that has not moved yet.
* **The target field is `spec.version`.** There is no
  `spec.releases.version`. apiextensions prunes an unknown field before
  validation, so a patch that names it reports success and changes
  nothing.
* **Both version fields take `yyyy.mm.dd-nnn`** and the digest takes
  `sha256:` plus 64 hex digits. The CRD's patterns refuse a typo at
  admission.

**Step 4. Watch the rollout.**

Each machine's operator compares its running version
(`status.version.liken`) against `spec.version` on every pass, and the
pass runs every 10 seconds. The upgrade starts by itself. The
`liken.sh/check-releases` annotation is not part of this flow: it
forces the fleet observer to poll the channel's `channel.yaml` now,
which fills in `status.releases.available`. Set it when the drill is
about channel polling:

```bash
dev-cluster/kubectl annotate cluster lab --overwrite \
    liken.sh/check-releases="$(date -Is)"
```

Watch the fleet move:

```bash
dev-cluster/kubectl get machines -w
```

The cluster grants one reboot turn at a time. Each machine downloads
the release onto its inactive slot, verifies every byte against the
digest chain, stages the record, reboots to prove the slot, and
promotes it.

## Fleet hygiene

**Install a machine.** Run this from the repo root, once per node,
before its first disk boot:

```bash
make install NODE=node-2
make run NODE=node-2
```

`make install` boots the install image through `-kernel`, claims the
blank boot disk, copies the release into slot A, writes the firmware's
boot chain, and powers off.

**A reinstall does not clear cluster state.** The state disk survives,
so a follower whose disk holds etcd members from an older cluster
incarnation never rejoins a re-founded leader. It reports "failed to
find remote peer in cluster" forever. An old bootHome can also flip
BootOrder back to the old proven slot right after the reinstall wrote
the new one.

To re-found a follower, delete its whole guest directory:

```bash
pgrep -x qemu-system-x86 -a | grep 'node-[4]' | awk '{print $1}' | xargs -r kill
rm -rf dev-cluster/guests/node-4
make install NODE=node-4
make run NODE=node-4
```

**Keep the founding leader's disks.** `spec.endpoint` points at
`node-1`, and the join token that brings followers back is the
cluster's identity, not the disk's. Wiping `node-1` re-founds the whole
cluster.

`make -C dev-cluster clean` deletes `guests/` entirely. That is a
factory reset of all five machines, not one.

**A machine heals back to `spec.version`.** The operator compares the
running version against the target on every 10-second pass, so a
`make install` of a dirty dev build onto a cluster that declares a
release gets pulled back onto the proven release. To keep a dev build
running, either declare that version in the Cluster or leave
`spec.version` unset.

**A fresh machine has no proven system-release record.** The
machine-operator writes the first one
(`settleSystemReleaseLifecycle` in `machine-operator/release.go`), and
until that record exists, no boot-order healing runs at all. A drill
that kills a guest seconds after Ready measures nothing. Wait for the
line in the operator's pod log:

```
recorded the running release <version> on slot <slot> as proven
```

## Inspect a running machine

**A liken machine has no shell and no SSH.** `image/oci.sh` packages
exactly one static binary per image, so `kubectl exec` into
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
      image: busybox:1.37
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

Each field is required for a reason:

* `nodeSelector: {liken.sh/machine: "true"}` is the label every liken
  node registers with. Without it the pod can land on a node that has
  no liken image.
* `tolerations: [{operator: Exists}]` schedules the pod on a cordoned
  or not-yet-ready node, which is when a drill matters most.
* `privileged: true` is required to read or write a raw block device.
  Without it, `dd` against `/dev/vdc` fails silently under the device
  cgroup.
* `hostNetwork: true` puts the pod in the host's network namespace,
  which is where the abstract unix socket to `iscsid` lives.
* Host `/sbin` at `/host-sbin` runs liken's own vendored tools:
  `/host-sbin/iscsiadm`, `/host-sbin/mount.nfs`.
* Host `/run/liken` holds the facts tree init publishes, and
  `/run/liken/operator/` is the operator's writable channel to init.

The lab's guests reach the internet through their uplink NIC, so the
`busybox:1.37` pull works with no registry mirror.

**Enabling a feature on an installed machine takes a live Cluster
patch,** not an edit to `dev-cluster/cluster.yaml`. The seed manifests
apply only on a machine's first boot. After that, init follows the
Cluster resource.

```bash
dev-cluster/kubectl patch cluster lab --type=merge \
    -p '{"spec":{"features":{"metrics-server":{},"iscsi":{}}}}'
```

**A merge patch merges map keys and does not replace the map.**
`spec.features` is a map, so resending a shorter map retracts nothing
and the retraction fails silently. Removing a feature takes an explicit
null:

```bash
dev-cluster/kubectl patch cluster lab --type=merge \
    -p '{"spec":{"features":{"flux":null}}}'
```

The same holds one level down: `{"flux":{"prune":null}}` restores one
parameter's default. The feature vocabulary is
`cluster/features.go`: `traefik`, `servicelb`, `metrics-server`,
`helm`, `network-policy`, `iscsi`, `nfs`, and `flux`. A vendored
feature converges with a k3s restart in place, and the console prints
`liken: modules: <mod>: loaded` and then
`liken: features: <slug>: active`.

**To reboot a machine on purpose,** write
`/run/liken/operator/reboot-intent.yaml` from the drill pod. Only PID 1
can shut a machine down, so the operator asks init through this file.
**The reason value must be valid YAML.** A bare colon breaks the parse.
A broken file still reboots the machine, but init logs "an unreadable
reboot intent" and the reason is lost.

A sysctl edit is the wrong trigger for a reboot drill. The operator
applies sysctls live and reboots nothing.

## Gotchas that waste hours

* **`make` inside `dev-cluster/` does not rebuild the OS image.**
  `make -C dev-cluster install` and `make -C dev-cluster run` boot
  whatever `image/install.cpio` already holds. A stale file drills the
  wrong spec, silently. Build from the repo root, or through
  `make smoke-uefi` / `make smoke-bios` / `make smoke-hardware`.
* **The smoke drills factory-reset `node-1`.** `smoke.sh` starts with
  `rm -rf guests/node-1`. All three drills use `node-1`, so a UEFI
  smoke wipes a BIOS-installed disk and the reverse. Never run a smoke
  drill while poking `node-1` by hand.
* **A guest that outlives its drill holds the qcow2 write locks** and
  stays on the multicast cluster segment. The next boot then fails to
  open its own disks and reports that the node never became Ready,
  which sends the reader to the machine instead of to the leftover
  process.
* **Resize a disk only while the machine is off.**
  `make -C dev-cluster grow-pods NODE=node-2` resizes a live qcow2 file
  and corrupts it if the guest is running. The same holds for
  `reset-nvram`, because QEMU writes the variable store back when the
  guest exits.
* **`SMOKE_DEADLINE`** (default 120 seconds) bounds only the disk
  boot's wait for Ready. The attended boots have their own 180-second
  bound inside `smoke.sh`.
* **A conductor or operator fix cannot be verified by the rollout
  that delivers it.** System pods run each node's own `:installed`
  image, so the old release's binaries make every decision during the
  window the fix governs. Land the fix in lab release N, roll the
  fleet onto N, and then drill N to N+1 to watch the fixed code act.
* **An edit to the cluster document costs a fleet reboot round.**
  Machines converge the document by reboot, so even a field the
  cluster operator reads live, such as `spec.disruption`, drifts
  every machine when it changes. A drill that raises the budget and
  restores it pays that round twice.

## Clean up

Report what is still running when the drill ends: which guests, which
`make serve`, and which background tasks. A `liken serve` holds
`:8017`, and a leftover guest holds `node-N`'s disks.
