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
new one arrives. Build the install media from a published release.
Build it with **that release's own bundled CLI**, not with
`cli/dist/liken`, because each release's toolkit writes the media
format of that release.

```bash
# 1. Download the release into a local channel directory.
old=<an older published version>
cli/dist/liken fetch https://releases.liken.sh "$old" /tmp/oldchannel

# 2. Take that release's own toolkit. The bundled binary ships
#    without the execute bit.
cp "/tmp/oldchannel/$old/liken" /tmp/old-liken
chmod +x /tmp/old-liken

# 3. Compose media from the old release and the lab's current layer.
/tmp/old-liken media "/tmp/oldchannel/$old" \
    dev-cluster/image/deployment.cpio \
    dev-cluster/image/old-install.cpio
```

The media combines the old release with the current deployment layer,
and the design allows that. The release contains no deployment, and
the layer contains no OS.

Install each node from that image. Set `INSTALL_CPIO` to the image's
path, relative to `dev-cluster/`:

```bash
make -C dev-cluster install NODE=node-1 FIRMWARE=bios \
    INSTALL_CPIO=image/old-install.cpio \
    CONSOLE=file:guests/node-1/install-console.log
```

Use `make -C dev-cluster` here on purpose. `make install` in `liken/`
first rebuilds `dev-cluster/image/install.cpio` from the working
tree, which gives the current build, not the old one.

Each machine seeds only itself. So a fleet that runs only some of
the machines counts only those, as in 3/3, and never reports
Degraded.

## The lab channel at a chosen version

`liken/Makefile` bundles the working tree into a channel with the
layout of a release, under `dev-cluster/image/channel/<version>/`.
`LAB_VERSION` sets that version. Its default is today's date with
serial `000`. To build the lab channel at another version, set it on
the command line:

```bash
make dev-cluster/image/install.cpio LAB_VERSION=$(date +%Y.%m.%d)-901
```

The variable is `LAB_VERSION`, not `LIKEN_VERSION`. When the drill is
about this bundle and not about `releases/dist`, serve the channel
directly:

```bash
cli/dist/liken serve dev-cluster/image/channel :8017
```

Only one server can listen on `:8017`. Stop `make serve` first.

## Deliberate-failure releases: the FAULT knob

An upgrade with two boot slots is only safe if the fallback to the
old slot works. To test the fallback, the release build can link a
fault into `init`. Do not build a broken bundle by hand. Build one
with `FAULT`:

```bash
make release VERSION=$(date +%Y.%m.%d)-902 FAULT=panic
```

Run this from `liken/`. The target first builds the vendored inputs
that the bundle needs, and then passes the build to
`releases/Makefile`. `FAULT` reaches that sub-make with no other
step.

Each fault tests one fallback path, and `init/fault.go` explains
each one:

* **`FAULT=panic`.** init panics at startup. When PID 1 dies, the
  kernel panics. The built-in `panic=10` argument reboots the machine
  ten seconds later. The firmware already used its one-shot BootNext,
  so it falls back to BootOrder and the proven slot. No `liken` code
  takes part in the recovery, and that is what this fault tests.
* **`FAULT=wedge-k3s`.** The machine boots, but k3s never starts. So
  the node never joins, and the operator that would promote the
  release never runs. BootNext cannot detect this, because the kernel
  is healthy and nothing panics. The proving watchdog waits ten
  minutes, then reboots onto the proven slot.

An empty `FAULT` injects no fault, and every real release has an
empty `FAULT`. Only init has the fault, because the drill is about a
broken boot.

Add a faulted release to the catalog and serve it exactly like a good
one. The digests match, so the download passes verification, and the
failure happens at boot, where the drill expects it. When the machine
falls back, two things change on its `Machine`:

* The condition `VersionConverged` becomes `False` with the reason
  `RejectedLastBoot`.
* The field `status.boot.systemRejection` records the fallback, with
  the `hash` of the rejected release record, a `reason`, and
  `rejectedAt`.

The drill ends when the machine is back on the version that worked.
The wait loop of "The upgrade drill" waits for the faulted release, so
it runs to its deadline. Read the condition and the field instead:

```bash
dev-cluster/kubectl get machine node-2 -o json | jq '{
    converged: (.status.conditions[] | select(.type == "VersionConverged")),
    rejection: .status.boot.systemRejection}'
```

## Reading a guest's disks offline

To inspect a machine's FAT system slots with no root and no shell,
convert the qcow2 file to raw and parse it. **The guest must be
off.** QEMU has the write lock while it runs, and a read of a live
image does not see a consistent filesystem.

```bash
pgrep -x qemu-system-x86 -a | grep 'node-[1]' | awk '{print $1}' | xargs -r kill
qemu-img convert -O raw dev-cluster/guests/node-1/boot.qcow2 /tmp/boot.raw
```

Then parse the GPT and walk FAT32 from Python. Skip each directory
entry whose first byte is `.`, or the walk recurses forever. A
directory entry with a correct long name but `size=0 first_cluster=0`
is the sign of a rename whose data was never written to the disk.

## The metal hardware shape

`HARDWARE=virtio` (the default) hides a whole class of faults. A
virtio guest never loads a storage module or a network module, so a
driver that a real controller needs is not tested until a real
machine boots.

`HARDWARE=metal` gives the guest the hardware of a real machine. The
disks are on an AHCI SATA controller, and the guest names them
`/dev/sda`, `/dev/sdb`, and `/dev/sdc`. The network cards are e1000.
The drivers for both controllers are kernel modules, so this shape
tests the module path.

The parity deployment is in `dev-cluster/hardware/`. Its
`cluster.yaml` is a symlink to the dev cluster's `cluster.yaml`, so
it is the same cluster with the same identity. Only the machine
manifest is different. Run it through its own drill:

```bash
make smoke-hardware
```

That drill boots the hardware report first, and checks the proposal
that the report writes to the stick. Then it installs, boots the
installed disk, and waits for Ready.

## The install stick and the hardware report

`make install` is the lab's fast unattended path. These targets test
what happens on real hardware:

* `make install-stick NODE=node-2` first resets the node's variable
  store to a blank vendor copy, which is what a new machine's
  firmware has. A firmware with no boot entries falls through to the
  removable-media path. So systemd-boot's menu shows on the serial
  console, and a person selects the machine.
* `make -C dev-cluster reset-nvram NODE=node-2` resets one node's
  firmware variables to the vendor defaults. A firmware update or a
  dead coin-cell battery does the same. The next boot must then come
  through the fallback loader on the proven slot, and that boot must
  write the entries again. Run it only while the machine is off.
