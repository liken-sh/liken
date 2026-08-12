# Cross-version and failure drills

Contents:

* Old-release baselines: install a fleet on a published release
* The lab channel at a chosen version
* Deliberate-failure releases: the FAULT knob
* Reading a guest's disks offline
* The metal hardware shape
* The install stick and the hardware report

## Old-release baselines

A cross-version drill needs the fleet on an older release before the
new one arrives. Compose the install media from a published release,
and compose it with **that release's own bundled CLI**, not with
`cli/dist/liken`. A release's media format is the format its own
toolkit writes.

```bash
# 1. Download the release into a local channel directory.
cli/dist/liken fetch https://releases.liken.sh 2026.07.31-001 /tmp/oldchannel

# 2. Take that release's own toolkit. The bundled binary ships
#    without the execute bit.
cp /tmp/oldchannel/2026.07.31-001/liken /tmp/old-liken
chmod +x /tmp/old-liken

# 3. Compose media from the old release and the lab's current layer.
/tmp/old-liken media /tmp/oldchannel/2026.07.31-001 \
    dev-cluster/image/deployment.cpio \
    dev-cluster/image/old-install.cpio
```

An old release plus the current deployment layer is the composition
design working as intended. The release carries no deployment, and the
layer carries no OS.

Install each node against that image. `INSTALL_CPIO` is the knob, and
its path is relative to `dev-cluster/`:

```bash
make -C dev-cluster install NODE=node-1 FIRMWARE=bios \
    INSTALL_CPIO=image/old-install.cpio \
    CONSOLE=file:guests/node-1/install-console.log
```

Use `make -C dev-cluster` here on purpose. The root `make install`
rebuilds `dev-cluster/image/install.cpio` from the working tree first,
which is the current build, not the old one.

Machines seed only themselves, so a three-of-five fleet reports 3/3 and
never reports Degraded. A three-leader rollout completes in roughly two
and a half minutes, one reboot at a time.

## The lab channel at a chosen version

The root Makefile bundles the working tree into a release-shaped
channel under `dev-cluster/image/channel/<version>/`. `LAB_VERSION`
names that version and defaults to today's date with serial `000`.
Override it on the command line to build the lab channel at a version
of your choosing:

```bash
make dev-cluster/image/install.cpio LAB_VERSION=$(date +%Y.%m.%d)-901
```

The knob is `LAB_VERSION`, not `LIKEN_VERSION`. Serve that channel
directly when the drill is about this bundle rather than about
`releases/dist`:

```bash
cli/dist/liken serve dev-cluster/image/channel :8017
```

Only one server can hold `:8017`. Stop `make serve` first.

## Deliberate-failure releases: the FAULT knob

A blue-green upgrade system is only as good as its fallbacks, so the
releases domain can stamp a fault into `init` at link time. Do not hand-
build a broken bundle. Build one:

```bash
make release VERSION=$(date +%Y.%m.%d)-902 FAULT=panic
```

Run this from the repo root. The root target builds the vendored
inputs the bundle needs before it hands off to `releases/Makefile`, and
`FAULT` reaches the sub-make on its own.

The two faults cover the two fallback paths, and
`init/fault.go` explains both:

* **`FAULT=panic`.** init panics at startup. PID 1 dying panics the
  kernel. The baked `panic=10` argument reboots ten seconds later, and
  the firmware falls back to BootOrder and the proven slot, because it
  already consumed its one-shot BootNext. No liken code takes part in
  the recovery, which is the whole point of this fault.
* **`FAULT=wedge-k3s`.** The machine boots and k3s never starts, so the
  node never joins and the operator that would promote the release
  never runs. BootNext cannot detect this: the kernel is healthy and
  nothing panics. The proving watchdog waits ten minutes, then reboots
  onto the proven slot.

An empty `FAULT`, which every real release has, injects no fault. Only
init carries the fault, because the drill is about a broken boot.

Catalog and serve a faulted release exactly like a good one. The
digests match, so the download verifies and the failure happens where
the drill wants it: at boot. Watch for `RejectedLastBoot` in
`status.boot.systemRejection`.

## Reading a guest's disks offline

To inspect a machine's FAT system slots with no root and no shell,
convert the qcow2 to raw and parse it. **The guest must be off.**
QEMU holds the write lock while it runs, and a read of a live image is
not a read of a consistent filesystem.

```bash
pgrep -x qemu-system-x86 -a | grep 'node-[1]' | awk '{print $1}' | xargs -r kill
qemu-img convert -O raw dev-cluster/guests/node-1/boot.qcow2 /tmp/boot.raw
```

Then parse the GPT and walk FAT32 from Python. Skip any directory entry
whose first byte is `.`, or the walk recurses forever. A directory
entry with a correct long name but `size=0 first_cluster=0` is the
signature of a rename whose data never reached the medium.

## The metal hardware shape

`HARDWARE=virtio` (the default) hides a whole class of faults: a virtio
guest never loads a storage or a network module, so a driver that a
real controller needs stays untested until a real machine boots.

`HARDWARE=metal` gives the shape a real machine has. The disks sit on
an AHCI SATA controller and the guest names them `/dev/sda`,
`/dev/sdb`, `/dev/sdc`. The network cards are e1000. Both controller
classes ship as kernel modules, so this shape exercises the module path.

The parity deployment lives in `dev-cluster/hardware/`. Its
`cluster.yaml` is a symlink to the dev cluster's, so it is the same
cluster with the same identity, and only the machine manifest differs.
Run it through its own drill:

```bash
make smoke-hardware
```

That drill boots the hardware report first, checks the proposal the
report writes to the stick, installs, boots the installed disk, and
waits for Ready.

## The install stick and the hardware report

`make install` is the lab's fast unattended path. Two targets drill
what real hardware does instead:

* `make install-stick NODE=node-2` refreshes the node's variable store
  to a blank vendor copy first, which is what a new machine's firmware
  looks like. A firmware with no boot entries falls through to the
  removable-media path, so systemd-boot's menu appears on the serial
  console and a person picks the machine.
* `make -C dev-cluster reset-nvram NODE=node-2` resets one node's
  firmware variables to the vendor defaults, which is what a firmware
  update or a dead coin-cell battery does. The next boot then has to
  come through the fallback loader on the proven slot, and that boot
  has to write the entries again. Run it only while the machine is off.
