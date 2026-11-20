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

Write each log to a scratch directory outside the repository, such as
the session's scratchpad. The commands in this skill write
`<scratch>` for that directory.

## Before the first command

Other sessions use the same ports and the same guest directories. So
before you start anything, run the check at the end of "Clean up". If
it prints a guest, a `liken serve`, a listener, or the registry, stop
and ask the maintainer whether the lab is in use.

## Which section to read

* **Boot, stop, or reach the fleet**: "Reach the cluster", "Start and
  stop guests", and "Install and reinstall machines".
* **An upgrade drill onto a lab release**: "The upgrade drill".
* **A guest that you must watch boot**: "Console output".
* **A drill that fails in a way you do not expect**:
  [references/common-failures.md](references/common-failures.md).
* **The end of every drill**: "Clean up".

To choose the drills for a change before you push, find what the
change touches:

* **You changed the install or the first boot from disk**, such as
  the boot chain that the install writes: run `make smoke-uefi` and
  `make smoke-bios`. Each one proves a first boot under one firmware.
* **You changed how a machine stages a release, boots it once on
  trial, or promotes it**, such as how init picks the boot slot: run
  "The upgrade drill".
* **You changed the fallback to the proven slot**: run the
  upgrade drill onto a release built with `FAULT`, as the section on
  deliberate-failure releases in
  [references/cross-version-drills.md](references/cross-version-drills.md)
  describes. Each value of `FAULT` proves one fallback path.

A change to how init picks the boot slot touches all three, so run
each of them.
* **An older published release as the baseline, a release that fails
  on purpose, the metal hardware shape, or a guest's disks while it is
  off**: [references/cross-version-drills.md](references/cross-version-drills.md).
* **A CRD schema or CEL change before you commit it**:
  [references/schema-drills.md](references/schema-drills.md).
* **A lab release on the testbed, `liken-1` and `stick-1`**:
  [references/testbed-release.md](references/testbed-release.md).
* **A guest that pulls an image you built on the laptop**:
  [references/lab-registry.md](references/lab-registry.md).
* **A guest with Secure Boot keys or capsule firmware, and the test
  firmware catalog, for `firmware-operator` drills**:
  [references/firmware-guests.md](references/firmware-guests.md).
* **A guest that hangs after `reboot: machine restart`, or a
  `BOOT=kernel` guest that reboots onto its old initramfs**:
  [references/guest-boot-faults.md](references/guest-boot-faults.md).
* **A running machine's filesystem from a busybox pod, a feature
  enabled or removed with a live patch, or a reboot on purpose**:
  [references/inspect-a-machine.md](references/inspect-a-machine.md).

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
    </dev/null ><scratch>/node-2.make.log 2>&1 &
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

This drill moves a running fleet forward to a new release. The
baseline is the release that the fleet runs before the drill. A fleet
that you install from the tree runs the tree's own build, with the
version `<yyyy.mm.dd>-000`, where the date is the day of the build. To start from an older published release,
install the fleet from it instead, as
[references/cross-version-drills.md](references/cross-version-drills.md)
describes, and then continue at step 1.

**Step 0. Start the fleet.**

1. Make the kubeconfig, as "Reach the cluster" describes.
2. Install each machine and start it, as "Install and reinstall
   machines" and "Start and stop guests" describe. Start `node-1`
   first, because it founds the cluster. A drill on `node-1` alone is
   valid. Each machine adds only its own `Machine` to the cluster, so
   the rollout counts only the machines that you start. The smoke
   drills boot `node-1` alone. Start `node-2` and `node-3` too when
   the change is about the datastore that several leaders share,
   because `leaders` in `dev-cluster/cluster.yaml` names them.
3. Wait with a deadline until every machine is Ready, with the loop
   in "Start and stop guests".
4. Find the baseline's catalog entry. The tree build's release
   document is `dev-cluster/image/channel/<version>/release.yaml`, and
   its digest is the sha256 of the file. The build deletes the
   directory when the date changes, so check that `<version>` is the
   version that the machines report:

   ```bash
   dev-cluster/kubectl get machines \
       -o jsonpath='{range .items[*]}{.status.version.liken}{"\n"}{end}'
   sha256sum dev-cluster/image/channel/*/release.yaml
   ```

**Step 1. Build a lab release.**

The build takes tens of minutes, so put a timeout on it:

```bash
timeout 3000 make release VERSION=$(date +%Y.%m.%d)-901 \
    > <scratch>/release.log 2>&1; echo rc=$?
grep -A2 'catalog entry' <scratch>/release.log
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
setsid make serve </dev/null ><scratch>/serve.log 2>&1 &
```

This serves `releases/dist` on `:8017`, the default port of
`liken serve`, under the path `/releases/`.
A guest reaches the host's loopback address at 10.0.2.2, so the URL
from inside a guest is `http://10.0.2.2:8017/releases`. That is the
exact `spec.releases.source` in `dev-cluster/cluster.yaml`. Leave this
task running. It logs every fetch, so you see a stalled or repeated
download when it happens.

**Step 3. Patch the Cluster.**

The old entry is the baseline's catalog entry from step 0. For a
published baseline, it is the entry that the release's page on the
channel gives. The new entry is the one that step 1 printed.

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
firmware's boot chain, and powers off. Before `make run`, check that
the install console contains the line that a finished install prints:

```bash
grep 'liken: installed to slot' dev-cluster/guests/node-2/install-console.log
```

An install that failed prints `liken: the install did not finish`
and powers off too.

**Install and run under BIOS.** The install writes the boot chain of
the firmware that it boots under. So set the same `FIRMWARE` on
`make install` and on every `make run` of that machine:

```bash
make install NODE=node-2 FIRMWARE=bios CONSOLE=file:guests/node-2/install-console.log
make run NODE=node-2 FIRMWARE=bios
```

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

## Clean up

You may start the dev cluster, `make serve`, and the lab registry
whenever a test needs them. When the testing ends, stop each of them.
Kill each guest's QEMU and the `liken serve` process by PID, and run
`make registry-stop`, which removes the registry's container and
keeps its images. Leave them running only when the
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
