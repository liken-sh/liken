# Working on liken

`liken/` is the OS. It contains one Go module, with a directory for
each program, and the shell scripts, manifests, and pins that build
the image. `README.md` explains what the OS is for and describes its
kinds and its programs.

## How it runs

A machine has two boot slots, A and B, and each slot contains one
release. An installed UEFI machine has no bootloader. `init` writes a
boot entry for each slot, and the firmware uses the entry to start the
kernel's EFI stub directly. A BIOS machine boots through GRUB. The
install stick boots through systemd-boot. Then the kernel loads
`microcode.cpio`, unpacks `boot.cpio`, and runs `init` as PID 1.

`init` loop-mounts the read-only `liken.sqfs` from slot A or slot B
and puts a tmpfs overlay over it. It copies the deployment layer onto
the overlay. The deployment layer is the manifests and the cluster's
identity. Then `init` switches root. Next it starts the machine plane,
which is the goroutines that set up what k3s needs before it can
start: storage, the network, the clock, and the identity files. k3s is
the only child process that `init` supervises. `init` reboots the
machine only when `machine-operator` writes an intent file that asks
for a reboot.

On a leader, k3s applies the OS's manifests from its auto-deploy
directory. They install the CRDs, `machine-operator`,
`cluster-operator`, and the `machine-logs` `DaemonSet`, which shows
the host's logs as pod logs. The images of these programs are stored
as OCI tarballs inside the OS image, so the OS runs without a pull
from a registry.

`machine-operator` is a privileged `DaemonSet` pod on each machine.
`init` writes the facts it finds about the machine to `/run/liken`,
and `machine-operator` reads them and writes the `Machine`'s status.
When the spec changes, `machine-operator` applies each change that
needs no reboot. It writes every other change to the machine's disk,
for the next boot. It downloads the `Cluster`'s target release into
the slot that the machine is not running from. It installs the
release only when the `Cluster`'s catalog lists the digest of the
release's `release.yaml`. When `cluster-operator` grants the machine's
`Disruption`, `machine-operator` drains the node and writes the intent
file that tells `init` to reboot. It also renews the machine's
heartbeat `Lease`.

`machine-operator` is also the DRA driver `liken.sh`. It lists each
device that has a kernel driver in the node's `ResourceSlice`, and its
kubelet plugin prepares each claim.

`cluster-operator` is one unprivileged `Deployment` that uses leader
election. It reads and writes only through the Kubernetes API. It
marks a machine `Lost` when the machine's heartbeat `Lease` is stale.
It writes the `Cluster`'s status. It grants each `Disruption` when the
`MachineDisruptionBudget`s allow it. It also reads the release channel
at `releases.liken.sh` to report the newest release.

## Design

The machine has no shell, no SSH, and no package manager. So after
the boot, every operation goes through the Kubernetes API.

Every goroutine in `init` runs as root in PID 1. A fatal runtime error
in any of them ends PID 1, and then the kernel panics. So `init` runs
a loop only when k3s cannot start without it.

Every change to a machine goes through the same steps. The machine
writes the change to its own disk and boots it once as a trial. If the
boot succeeds, the machine keeps the change. If the boot fails, the
machine goes back to the slot that booted before. Only one leader can
be out of service at a time, because the loss of two etcd members can
leave etcd without a majority.

The OS publishes each machine's hardware as devices of the `liken.sh`
driver. A device's attributes record the facts about it that no other
layer can observe. The OS also ships the firmware that the kernel's
drivers load. An extension operator claims a device through the
standard DRA interfaces, the same ones that work with any other DRA
driver. No code in the OS reads an operator's kinds. When an operator
needs a new fact about a device, the OS adds the fact as an attribute.
The logic of the feature stays in the operator.

The scripts, manifests, and Go files are the documentation. Their
comments explain how a system boots, for example why an initramfs is
a cpio archive. The tree is organized by domain. Each directory, such
as the one for the kernel or the one for the image, contains its own
scripts, configuration, and notes.

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.

## Finding things

- When you change a CRD's schema, raise its `liken.sh/schema-revision`
  annotation. `TestTheSeededCRDsPinTheirSchemaRevision` in `init/`
  fails when you don't. `init` applies whichever copy of a CRD has the
  higher revision. So a machine that boots an older slot does not
  replace the cluster's schema with an older one.
- The ordinary lab guests have virtio devices, which need no kernel
  module. `dev-cluster/hardware/README.md` describes a lab guest that
  does load its storage and network modules. Milestone 32 lists what
  only a physical machine can prove.
- The scripts in `licensing/` write each release's `LICENSES.md` from
  `licensing/NOTICES.md`. `licensing/sources.sh` copies the source of
  each copyleft component to the release channel.
