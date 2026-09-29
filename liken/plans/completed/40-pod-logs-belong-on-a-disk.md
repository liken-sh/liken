# Pod logs belong on a disk

Milestone 40. Completed. A bind mount puts the pod log directory on
the podEphemeral storage role and keeps it visible at `/var/log/pods`.

liken's root is a read-only squashfs with a 128 MiB tmpfs upper layer.
That layer is the write budget for everything that no storage role
sends to a disk. The budget is small on purpose. The runtime's writes
under `/` are small and fixed, so a boot that fills the layer shows
that something writes where it must not.

Pod logs were landing there. A container appends to its log for as
long as it runs. kubelet caps one container at `containerLogMaxSize`
multiplied by `containerLogMaxFiles`, which is 10Mi and five files by
default, and nothing caps the sum of all the containers. Three
containers that log heavily are larger than the whole root. On a
machine with no shell, nobody can release the space afterwards.

This is not a fault in the arrangement of `/var`. The storage roles
are the paths that liken sends through to a disk, and each role was
chosen for a writer that grows with use. Pod logs are a writer of that
kind that nobody had named. This milestone does not rearrange `/var`.
It adds one more path to the set that reaches a disk: anything that
appends for as long as the machine runs belongs on a role, not on the
root's write budget.

## The path stays and the filesystem under it changes

The KubeletConfiguration setting `podLogsDir` gives the directory that
kubelet writes to, so the obvious correction is to move it under
`/var/lib/kubelet`. liken does not do that. Too much depends on the
canonical path: a log collector mounts it by hostPath, the symlinks in
`/var/log/containers` resolve into it, and every runbook names it. If
a node keeps its logs somewhere else, each tool and each operator must
be told, and that cost is paid again at every tool and every runbook.

A bind mount changes the filesystem under the path and leaves the path
alone. A directory on podEphemeral appears at `/var/log/pods`, so
every reader finds the logs where it expects them, and the writes land
on the disk that the operator sized for kubelet.

podEphemeral is the correct role for a second reason. kubelet's
`nodefs` is the filesystem of its own root directory, which is this
role. Logs written here occupy space that kubelet's disk-pressure
eviction already watches and that `ephemeral-storage` limits describe.
liken sets no eviction thresholds of its own, and kubelet's defaults
apply. The logs are now inside what those defaults measure instead of
outside it.

## A machine without the role gets no mount

When the manifest does not declare podEphemeral, `/var/lib/kubelet` is
on the root overlay too. A bind would move bytes from one overlay
directory to another and add a mount that claims a separation the
machine does not have. The boot therefore skips the bind and prints
the reason on the console. A mount table that describes something
untrue costs more than the bind is worth, and the printed reason is
what an operator needs when they later ask why the root filled up.

## The bind comes off before the disks do

A bind holds a second reference to podEphemeral's filesystem. If the
shutdown unmounts `/var/lib/kubelet` with the bind still in place, it
detaches that mount point and leaves the disk in use. A machine that
cannot release its disks on the way down is a worse problem than the
one this milestone corrects. The shutdown path therefore detaches the
bind first, with the same flags and the same reporting that the roles
get, and the role unmounts that follow can then release the
filesystem. Both shutdown paths do this, because they share one
function.

## What the lab measured

Both firmware drills passed, and each console reported the bind before
k3s started. The bind then held on a cluster of three machines and on
both hardware shapes: the virtio disks of the ordinary lab and the
AHCI disks of the metal drill. Init's own mount table showed one
partition at both `/var/lib/kubelet` and `/var/log/pods` on every
node. Every pod on each machine had its log directory on that disk:
coredns, metrics-server, local-path-provisioner, both liken operators,
and the log relay.

The readers that depend on the path all still work. `kubectl logs`
read a pod's output through the canonical path, which is the most
important check, because every other reader uses that path. Every
symlink in `/var/log/containers` resolved into the disk, and none
dangled on any node. A pod with a hostPath mount of `/var/log` had the
same disk under `pods/`, which is the shape a log collector uses, so
the bind propagates into a container that asks for it.

The volume is the point, and the numbers show it. Under a load of
several containers that logged hard, one machine put 139 MiB of logs
on podEphemeral and another put 157 MiB. Each amount is larger than
the whole 128 MiB overlay could hold. The overlay itself moved by
96 KiB on both machines.

The reboot was the other half. The lab drilled it on all three
machines while the cluster stayed up. Every console showed
`/var/log/pods` unmounted first, then `/var/lib/kubelet`, with the
kernel's own `EXT4-fs: unmounting filesystem` line between them. The
disk released every time, no unmount reported a busy target, and each
machine came back Ready and bound the logs again in under a minute.

The drill also showed what changes now that these logs outlive their
boot. A container came back with `0.log` from the previous boot and
`1.log` from this one, which is the ordinary restart numbering, kept
across a reboot instead of erased with a tmpfs. After the drill's Pod
objects were deleted, kubelet's own garbage collection took one node
from 161 MiB of logs back to 1.3 MiB. liken does not clean up after
it.

One kubelet behaviour is on record here, because it adds to the reason
for this milestone. A container that writes in bursts goes past
`containerLogMaxSize` before the rotation catches it, so the cap is
looser under a burst than its number shows. The bound that matters is
the filesystem that the bytes land on.

The machine that skips the bind is the one case the lab did not run.
Every machine in the dev fleet declares podEphemeral, so a unit test
pins the skip instead.

## The manual

An operator runs no different command because of this, so the guides
do not change. One thing an operator can see does change: bytes now
land on a disk that the operator sized. The podEphemeral description
in the Machine schema said that the role holds emptyDir volumes and
per-pod scratch. It now also says that the pod logs are written there,
that they stay visible at `/var/log/pods`, and that the size must
account for them. The Machine reference page regenerates from that
description, so the schema is the whole correction.
