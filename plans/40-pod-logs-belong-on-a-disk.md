# Pod logs belong on a disk

Milestone 40 — Done

liken's root is a read-only squashfs with a 128 MiB tmpfs upper layer,
and that layer is the whole write budget of everything not routed to a
storage role. The budget is deliberate: the runtime's writes under `/`
are small and fixed, and a boot that fills it is a report about
something writing where it should not. Pod logs were landing there. A
container appends to its log for as long as it runs, kubelet caps one
container at `containerLogMaxSize` times `containerLogMaxFiles`, 10Mi
and five files by default, and nothing caps their sum. Three
containers that log heavily are more than the whole root. On a machine
with no shell, nobody can clear the space afterward.

This is not a fault in how `/var` is arranged. The storage roles are
exactly the paths that liken punches through to disk, and each one was
chosen for a writer that grows with use. Pod logs are simply a writer
of that kind that nobody had named yet. So the ask is not to rearrange
`/var`. It is to add one more path to the set that already reaches
disk: **anything that appends for as long as the machine runs belongs
on a role, not on the root's write budget.**

## The path stays and the filesystem under it changes

The directory kubelet writes to is a KubeletConfiguration setting,
`podLogsDir`, so the obvious fix is to point it under
`/var/lib/kubelet`. liken does not do that. Too much depends on the
canonical path: a log collector mounts it by hostPath, the symlinks in
`/var/log/containers` resolve into it, and every runbook names it. A
node whose logs are somewhere else is a node that every tool and every
operator has to be taught about, and that cost is paid again at every
tool and every runbook.

A bind mount moves the bytes and leaves the path alone. A directory on
podEphemeral appears at `/var/log/pods`, so every reader finds the logs
where it expects them, and the writes land on the disk that the
operator sized for kubelet.

podEphemeral is the right role for a second reason. kubelet's `nodefs`
is the filesystem of its own root directory, which is this role, so
logs written here occupy space that kubelet's disk-pressure eviction
already watches and that `ephemeral-storage` limits are meant to
describe. liken sets no eviction thresholds of its own, and kubelet's
defaults apply; the point is that the logs are now inside what those
defaults measure instead of outside it.

## A machine without the role gets no mount

When the manifest does not declare podEphemeral, `/var/lib/kubelet` is
on the root overlay too. A bind would carry bytes from one overlay
directory to another and add a mount that claims a separation the
machine does not have. So the boot skips it and says why on the
console. A mount table that describes something untrue costs more than
the bind is worth, and the honest report is what an operator needs when
they later ask why the root filled up.

## The bind comes off before the disks do

A bind holds a second reference to podEphemeral's filesystem.
Unmounting `/var/lib/kubelet` with the bind still in place detaches
that mount point and leaves the disk in use, and a machine that cannot
release its disks on the way down is a worse problem than the one this
milestone set out to fix. So the shutdown path detaches the bind first,
with the same flags and the same reporting that the roles get, and the
role unmounts that follow can then release the filesystem. Both
shutdown paths get this, because they share one function.

## What the lab proved

Both firmware drills passed. `make smoke-uefi` took node-1 from blank
disks to Ready in 21 seconds, and `make smoke-bios` in 16 seconds. Each
console reported the bind before k3s started.

A hand drill on the same guest proved the rest. Init's own mount table
showed `/dev/vdb3` at both `/var/lib/kubelet` and `/var/log/pods`, and
every pod on the machine had its log directory on that disk: coredns,
metrics-server, local-path-provisioner, both liken operators, the log
relay, and the drill's own logging pod. `kubectl logs` read that
pod's output through the canonical path, which is the check that
matters most, because that is the path every other reader uses. A pod
with a hostPath mount of `/var/log` saw the same disk under
`pods/`, which is the shape a log collector uses, and every symlink in
`/var/log/containers` resolved into it.

The reboot was the other half. A reboot intent written from a pod took
the machine down, and the console showed `/var/log/pods` unmounted
first, then `/var/lib/kubelet`, with the kernel's own
`EXT4-fs (vdb3): unmounting filesystem` line between them. The disk
released. The machine came back Ready and bound the logs again.

The drill also showed what changes now that these logs outlive their
boot. coredns came back with `0.log` from the boot before and `1.log`
from this one, which is the ordinary container-restart numbering, kept
across a reboot instead of erased with a tmpfs. The
directories of the two drill pods that did not survive the reboot were
removed by kubelet's own garbage collection once their Pod objects
were deleted. Nothing here needs liken to clean up after it.

The machine that skips the bind is the one case the lab did not run.
Every machine in the dev fleet declares podEphemeral, and the skip is
pinned by a unit test instead.

## The manual

An operator runs no different command because of this, so the guides do
not change. One thing they can see does change: bytes now land on a
disk they sized. The podEphemeral description in the Machine schema
said the role holds emptyDir volumes and per-pod scratch. It now also
says the pod logs are written there, that they stay visible at
`/var/log/pods`, and that the size should account for them. The Machine
reference page regenerates from that description, so the schema is the
whole fix.
