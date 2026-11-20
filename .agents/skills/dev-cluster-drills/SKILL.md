---
name: dev-cluster-drills
description: Runs, upgrades, breaks, and inspects liken's dev cluster, the QEMU fleet under dev-cluster/ whose Cluster resource is named lab, and serves lab releases to the physical testbed liken-1. Use when a session must boot or stop lab guests, run a smoke drill such as make smoke-uefi or make smoke-bios, build a lab release with make release VERSION=yyyy.mm.dd-9xx, serve it with make serve, roll a lab release onto liken-1 and stick-1, drive an upgrade or fallback drill, reach the lab with kubectl or stern, push a locally built image to the lab registry, boot a guest with Secure Boot keys or capsule firmware, read a running machine's filesystem or disks, re-found the fleet after a reinstall, or probe a CRD schema or CEL change.
---

# Dev-cluster drills

The dev cluster is the cluster that `liken` is developed against. You
can break it freely. It runs no work worth keeping, and
`make -C dev-cluster clean` resets every guest to blank disks.

Run every command in this skill from `liken/`. Every path is relative
to `liken/` unless the text gives another base. A path that starts
with `/`, such as `/run/liken/`, is a path on a guest.

Longer procedures are in separate files. Read a file when the drill
needs what its line describes:

* [references/cross-version-drills.md](references/cross-version-drills.md):
  old-release baselines, releases that fail on purpose, the metal
  hardware shape, and reading a guest's disks while it is off.
* [references/schema-drills.md](references/schema-drills.md): testing
  a CRD schema or CEL change before you commit it.
* [references/testbed-release.md](references/testbed-release.md):
  serving a lab release to the testbed, `liken-1` and `stick-1`.
* [references/lab-registry.md](references/lab-registry.md): a guest
  that pulls an image you built on the laptop.
* [references/firmware-guests.md](references/firmware-guests.md): a
  guest with Secure Boot keys or capsule firmware, and the test
  firmware catalog, for `firmware-operator` drills.
* [references/guest-boot-faults.md](references/guest-boot-faults.md):
  a guest that hangs after `reboot: machine restart`, and a
  `BOOT=kernel` guest that reboots onto its old initramfs.
* [references/inspect-a-machine.md](references/inspect-a-machine.md):
  reading or writing a running machine's filesystem from a busybox
  pod, enabling or removing a feature with a live patch, and
  rebooting a machine on purpose.

## What the dev cluster is

`dev-cluster/` defines the machines `node-1` through `node-5`, and
each one is a QEMU guest. `dev-cluster/cluster.yaml` declares the
Cluster resource named `lab`. Its `leaders` list names the leaders,
and each other machine is a follower. `node-1` is the founding
leader, the leader that creates the cluster. Every machine's
`rebootPolicy` is `Auto`, so no drill in this lab needs
`liken approve-reboot`.

Each guest's files are in `dev-cluster/guests/<node>/`: the disks
`state.qcow2`, `pods.qcow2`, and `boot.qcow2`, whose sizes
`dev-cluster/Makefile` sets. Under UEFI, the directory also has the
guest's own `OVMF_VARS.fd`, its firmware variable store. git ignores
the directory, and it contains no source.

Each guest has two network cards. `eth0` is a QEMU user-mode uplink on
10.0.2.0/24. QEMU provides NAT, DHCP, and DNS on it, so a guest can
pull an image from the internet. `eth1` is the cluster segment,
10.10.0.0/24. It is a multicast socket that every guest joins. Only
`node-1` forwards a port from the host: 127.0.0.1:16443 on the host
connects to its API server on 6443. `API_FORWARD` in
`dev-cluster/Makefile` sets the forward.

`dev-cluster/Makefile` sets the defaults of `FIRMWARE`, `BOOT`,
`HARDWARE`, `SMP`, `MEM`, and `CONSOLE`. A guest boots under UEFI from
its own disk, with virtio hardware, unless you set one of them.

## Reach the cluster

Create the admin credential once, from `liken/`:

```bash
make kubeconfig
```

This writes `dev-cluster/identity/kubeconfig`, which points at
`https://127.0.0.1:16443`. After that, use the wrappers:

```bash
dev-cluster/kubectl get nodes
dev-cluster/kubectl get machines
dev-cluster/kubectl get cluster lab -o yaml
dev-cluster/stern -n liken-system liken-machine-operator
```

The `kubectl` command of the `liken` CLI passes its arguments to the
same cluster, with no wrapper:

```bash
cli/dist/liken kubectl -server https://127.0.0.1:16443 dev-cluster get machines
```

## Console output

`make run` sends the guest's serial console to `stdio`, which is the
output of the task that runs it. It does not write a file. If you
grep `dev-cluster/guests/<node>/console.log` after a plain `make run`,
you read an old log from an earlier drill.

To keep a console, give a file name:

```bash
make run NODE=node-2 CONSOLE=file:guests/node-2/console.log
```

`dev-cluster/smoke.sh` always writes files. It writes one pair for
each boot, in `dev-cluster/guests/node-1/`:

* `console.log` and `qemu.log` for the disk boot.
* `install-console.log` and `install-qemu.log` for the install.
* `report-console.log` and `report-qemu.log` for the hardware report.

The console file is the machine's own output. The QEMU file is the
only record of a boot that failed before the console printed
anything.

## Start and stop guests

Start a guest from `liken/`, so that make builds the artifacts first:

```bash
make run NODE=node-1
make run NODE=node-4
```

Run each guest in its own task, and start it in the background under
`setsid`. Without `setsid`, the guest stops when the harness cleans
up a stopped task, because the background wrapper then sends SIGTERM
to its child QEMU process:

```bash
setsid make run NODE=node-2 CONSOLE=file:guests/node-2/console.log \
    </dev/null >/tmp/node-2.make.log 2>&1 &
```

A `CONSOLE=file:` path is relative to `dev-cluster/`, whichever
directory you start make from. The root target passes the build to
`make -C dev-cluster`, so the recipe always runs in `dev-cluster/`.

**Never use `pkill -f` or `pgrep -f`.** The pattern text is part of
the command line of the Bash tool's own `bash -c` wrapper, so the
pattern matches that shell. The shell is killed in the middle of the
command. The later steps of `a; b && c` never run, the exit code is 1
or 144, and output is missing. When the pattern also matches a guest,
the guest is killed too, and the failure looks like a problem with
the host.

Match on the process name, `comm`, instead. The kernel truncates
`comm` to 15 characters, so `qemu-system-x86_64` matches as
`qemu-system-x86`:

```bash
# every guest
pkill -x qemu-system-x86

# one guest. The bracket class keeps the calling shell out of the match.
pgrep -x qemu-system-x86 -a | grep 'node-[2]' | awk '{print $1}' | xargs -r kill
```

Put a timeout on anything that waits. Poll with a deadline, never
with a loop that has no end:

```bash
deadline=$(( $(date +%s) + 300 ))
while (( $(date +%s) < deadline )); do
    dev-cluster/kubectl get nodes --no-headers 2>/dev/null | grep -q Ready && break
    sleep 5
done
```

`make storage` starts the lab's storage server. It is a stock Debian
guest at 10.10.0.100 on the cluster segment. It serves an NFSv4
export, an iSCSI LUN, and a pull-through registry that requires
authentication. Use it for drills of the `nfs` and `iscsi` features
and of `spec.registries`. Run it in its own task, like a node.
`dev-cluster/storage/Makefile` has the details.

## The upgrade drill

This drill moves a running fleet forward to a new release. Start the
fleet first.

**Step 1. Build a lab release.**

The build takes tens of minutes, so put a timeout on it:

```bash
timeout 3000 make release VERSION=$(date +%Y.%m.%d)-901 \
    > /tmp/release.log 2>&1; echo rc=$?
grep -A2 'catalog entry' /tmp/release.log
```

`VERSION` has no default, so `make release` alone fails. The serial
is the three digits at the end of the version. `releases/Makefile`
checks it with a regular expression before any build starts. Use a
`-9xx` serial for a lab build. The working-tree channel that the
media targets bundle uses serial `000`, and published releases count
up from `001`. So a `-9xx` serial collides with neither, and you can
recognize it at a glance.

The build ends by printing the catalog entry, and the `grep` above
shows it:

```
catalog entry for a Cluster's spec.releases.catalog:
  - version: <yyyy.mm.dd>-901
    digest: sha256:<64 hex digits>
```

The digest is the sha256 of the bytes of that release's
`release.yaml`. It is the start of the trust chain. The Cluster lists
the digest of `release.yaml`, and `release.yaml` lists the digest of
every artifact.

**Step 2. Serve the channel.**

Check that no earlier `liken serve` uses the port. Then start the
server under `setsid`, as for a guest:

```bash
ss -ltnp | grep ':8017' || echo free
setsid make serve </dev/null >/tmp/serve.log 2>&1 &
```

This serves `releases/dist` on `:8017`, the default port of
`liken serve`, under the path `/releases/`.
A guest reaches the host's loopback address at 10.0.2.2, so the URL
from inside a guest is `http://10.0.2.2:8017/releases`. That is the
exact `spec.releases.source` in `dev-cluster/cluster.yaml`. Leave this
task running. It logs every fetch, so you see a stalled or repeated
download when it happens.

**Step 3. Patch the Cluster.**

```bash
dev-cluster/kubectl patch cluster lab --type=merge -p '{
  "spec": {
    "version": "<new version>",
    "releases": {
      "catalog": [
        {"version": "<old version>", "digest": "sha256:<old>"},
        {"version": "<new version>", "digest": "sha256:<new>"}
      ]
    }
  }
}'
```

These rules apply to the patch:

* **A JSON merge patch replaces an array.** `spec.releases.catalog`
  is an array, so send every entry that you want to keep. If you drop
  the entry of the release that the fleet runs now, you strand any
  machine that has not moved yet.
* **The target field is `spec.version`.** There is no
  `spec.releases.version`. apiextensions prunes an unknown field
  before validation, so a patch that sets it reports success and
  changes nothing.
* **Both version fields use the format `yyyy.mm.dd-nnn`**, and the
  digest is `sha256:` followed by 64 hex digits. The CRD has a pattern
  for each field, so the API server rejects a typo at admission.

**Step 4. Watch the rollout.**

On every pass, each machine's operator compares the running version,
`status.version.liken`, with `spec.version`. The operator watches the
`Cluster`, so a change to its spec starts a pass at once, and the
upgrade starts with no other step from you. If no event arrives, a
backstop pass runs after `backstopEvery`, 5 minutes, which is set in
`machine-operator/backstop.go`.

The `liken.sh/check-releases` annotation is not part of this flow. It
makes the fleet observer poll the channel's `channel.yaml` at once,
and the poll fills in `status.releases.available`. Set it when the
drill is about channel polling:

```bash
dev-cluster/kubectl annotate cluster lab --overwrite \
    liken.sh/check-releases="$(date -Is)"
```

Wait with a deadline, and print the machines only when a line
changes:

```bash
V=<new version>
deadline=$(( $(date +%s) + 2400 )); last=""
while (( $(date +%s) < deadline )); do
    s=$(dev-cluster/kubectl get machines --no-headers 2>/dev/null)
    [[ -n "$s" && "$s" != "$last" ]] && { date -u +%T; echo "$s"; last="$s"; }
    dev-cluster/kubectl get machines -o json 2>/dev/null | jq -e --arg v "$V" \
        '[.items[] | select(.status.version.liken != $v or .status.phase != "Ready")] | length == 0' \
        >/dev/null && break
    sleep 20
done
```

The cluster gives a reboot turn to one machine at a time. Each machine
downloads the release onto its inactive slot and verifies every byte
against the digest chain. Then it stages the record, reboots to prove
the slot, and promotes the slot.

## Install and reinstall machines

**Install a machine.** Run this from `liken/`, once for each node,
before its first disk boot:

```bash
make install NODE=node-2 CONSOLE=file:guests/node-2/install-console.log
make run NODE=node-2
```

`make install` boots the install image through `-kernel`. The install
claims the blank boot disk, copies the release into slot A, writes the
firmware's boot chain, and powers off.

**A reinstall does not clear cluster state.** The state disk survives
a reinstall. A follower whose state disk has etcd members from an
older instance of the cluster never rejoins a re-founded leader. It
reports "failed to find remote peer in cluster" forever. Also, an old
bootHome can set BootOrder back to the old proven slot right after the
reinstall wrote the new one.

To re-found a follower, delete its whole guest directory, and install
it again:

```bash
pgrep -x qemu-system-x86 -a | grep 'node-[4]' | awk '{print $1}' | xargs -r kill
rm -rf dev-cluster/guests/node-4
make install NODE=node-4 CONSOLE=file:guests/node-4/install-console.log
make run NODE=node-4
```

**Keep the founding leader's disks.** `spec.endpoint` points at
`node-1`. The join token that lets the followers join again comes from
the cluster's identity, not from a disk. If you wipe `node-1`, you
re-found the whole cluster.

`make -C dev-cluster clean` deletes all of `dev-cluster/guests/`. That
is a factory reset of every machine, not of one.

**A machine moves back to `spec.version` by itself.** The operator
compares the running version with the target on every pass. So if you
`make install` a dirty dev build onto a cluster that declares a
release, the operator moves the machine back onto the proven release.
To keep a dev build running, declare that version in the Cluster, or
leave `spec.version` unset.

**A new machine has no proven system-release record.** machine-operator
writes the first one, in `settleSystemReleaseLifecycle` in
`machine-operator/release.go`. Until that record exists, no boot-order
healing runs. So a drill that kills a guest seconds after it is Ready
tests nothing. Wait for this line in the operator's pod log:

```
recorded the running release <version> on slot <slot> as proven
```

```bash
dev-cluster/kubectl -n liken-system logs ds/liken-machine-operator | grep 'as proven'
```

`logs ds/...` reads the log of one pod of the `DaemonSet`. To read
the log for one node, use the name of that node's pod, which
`get pods -o wide` lists, in place of the `ds/` name.

## Common failures

Each item starts with what you see, then gives the cause and what to
do.

* **The drill tests an older spec than your working tree, and nothing
  warns you.** `make` inside `dev-cluster/` does not rebuild the OS
  image. `make -C dev-cluster install` and `make -C dev-cluster run`
  boot whatever `dev-cluster/image/install.cpio` already contains.
  Build from `liken/`, or run a smoke drill's root target, which
  builds the artifacts first.
* **`node-1` is back to blank disks.** A smoke drill factory-reset it.
  The smoke drills are the `smoke-*` targets of `liken/Makefile`,
  each run from `liken/`. Each one runs `dev-cluster/smoke.sh`, which
  starts with `rm -rf guests/node-1` in `dev-cluster/`. Each drill
  uses `node-1`, so a UEFI smoke drill wipes a disk installed under
  BIOS, and the reverse. Never run a smoke drill while you work on
  `node-1` by hand. CI runs `smoke-uefi` and `smoke-bios`.
  `smoke-emmc` runs only on a workstation, because QEMU's `emmc`
  device needs QEMU 9.1.
* **A node never becomes Ready after a boot.** A guest from an earlier
  drill is still running. It has the write locks on the qcow2 files,
  and it is still on the multicast cluster segment. The next boot of
  that node fails to open its own disks, and the report says only that
  the node never became Ready, so you look at the machine and not at
  the leftover process. List the guests with
  `pgrep -x qemu-system-x86 -a`, and kill the leftover one by PID.
* **A qcow2 file or a variable store is damaged after a change.**
  `make -C dev-cluster grow-pods NODE=node-2` resizes a qcow2 file,
  and it corrupts the file if the guest is running. The same applies
  to `grow-state`, which enlarges a guest's state disk to
  `STATE_SIZE`. It also applies to `reset-nvram`,
  because QEMU writes the variable store back when the guest exits.
  Resize a disk or reset the variable store only while the machine is
  off.
* **A smoke drill stops with a timeout.** Each kind of boot has its
  own deadline, in seconds. `SMOKE_DEADLINE` limits only the time
  that the disk boot waits for Ready. The attended boots use
  `HOLD_DEADLINE`. `dev-cluster/smoke.sh` sets the default of each,
  and the failure message gives the number of seconds that ran out. A target
  of `dev-cluster/Makefile` can set larger values, as `smoke-emmc`
  does because QEMU's `emmc` device writes slowly. Check which
  deadline applies to the boot that timed out.
* **A conductor or operator fix shows no effect in the rollout that
  delivers it.** System pods run each node's own `:installed` image.
  So during that rollout, the binaries of the old release make every
  decision that the fix changes. Put the fix in lab release N, roll
  the fleet onto N, and then drill N to N+1 to see the fixed code act.
* **Every machine reboots after an edit to the `Cluster`.** Machines
  apply the cluster document by rebooting. A field that the cluster
  operator reads live, such as `spec.disruption`, still marks every
  machine as out of date when it changes. So each edit costs one
  reboot round of the fleet, and a drill that raises the budget and
  then restores it costs two. Allow for one round for each edit.

## Clean up

You may start the dev cluster, `make serve`, and the lab registry
whenever a test needs them. When the testing ends, stop each of them.
Kill each guest's QEMU and the `liken serve` process by PID, and run
`docker stop liken-lab-registry`. Leave them running only when the
maintainer asked for that.

When the drill ends, report what is still running: which guests,
which `make serve`, whether `liken-lab-registry` runs, and which
background tasks. A `liken serve` listens on `:8017`, the lab registry
on `:5000`, and `node-1`'s QEMU on `:16443`. The `registry` target
and `API_FORWARD` in `dev-cluster/Makefile` set the last two. A
leftover guest has the
locks on `node-N`'s disks. This check lists each of them:

```bash
pgrep -x qemu-system-x86 -a
pgrep -x liken -a
ss -ltnp | grep -E ':8017|:5000|:16443'
docker ps --filter name=liken-lab-registry --format '{{.Names}}'
```
