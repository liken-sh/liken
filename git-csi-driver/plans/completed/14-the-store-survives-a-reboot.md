# 14, The store survives a reboot

Built on 2026-09-27. The unit tests read a copy of the mount table a
node plugin on liken-1 reads, trimmed to the lines that matter. The
fixture for a `liken` node without the pod-storage partition is
written from how `liken`'s init mounts the root overlay, not copied
from a node. The lab drill below has not run yet, and it is what
proves that fixture. This plan closes the open problem
"The store on the wrong filesystem".

## The problem

The store defaults to `/var/lib/liken/pod-storage/git-csi`, which on a
`liken` node is the pod-storage partition. A node without that
partition, or a cluster that is not `liken`, has no such mount, and the
`hostPath` volume creates the directory on the root filesystem. On a
`liken` node the root is an overlay whose upper directory is on a
`tmpfs`, so every bare repository and every work tree is lost at
reboot, and a writeable volume that had not pushed loses its work.

The driver started, served, and reported nothing, because the
directory existed and was writable. The loss showed at the next reboot.

## The design

### The node plugin reads the mount table once, at start

`storemount.go` reads `/proc/self/mountinfo` and finds the mount that
holds the store: the entry with the longest mount point above the
store's path. Two entries on the same mount point are a stack, and the
later line is the one on top. The check runs once, because the store's
`hostPath` mount is fixed for the life of the pod.

The rule reads the filesystem type, not the path:

| The mount that holds the store | Result |
|---|---|
| `tmpfs`, `ramfs`, or `rootfs` | refused |
| an overlay whose upper directory is on `tmpfs`, `ramfs`, or `rootfs` | refused |
| an overlay whose upper directory is on no disk in this mount table | refused |
| an overlay with no upper directory | refused, because it takes no writes |
| any other type, a bind mount of it included | accepted |
| no mount at all, or a table the plugin cannot read | refused |

A bind mount shows the type of the filesystem it binds, so the
`hostPath` bind of the pod-storage partition reads as `ext4`. The
check needs no new dependency.

### An overlay is read one level down

An overlay keeps its writes in its upper directory, so the plugin reads
the mount that holds the path in the `upperdir` option. That path is as
the process that mounted the overlay saw it. From inside a pod, the
path names no mount in the pod's table, and the mount that holds it is
the container's own root, which is an overlay again. That is no
evidence about the node's disk, so an overlay whose upper directory is
on another overlay, or on nothing, is refused. This is how the root
overlay of a `liken` node reads from inside the node plugin's pod.

An overlay on a disk is accepted only when the table names the disk
under its upper directory, which is when the plugin runs outside a pod,
or when the pod mounts that disk too. A node whose root is itself a
container's overlay, as in some test clusters that run nodes in
containers, is refused unless the store's path is on a volume of its
own.

Two readings stay approximate. The mount that holds a path is the one
with the longest mount point, so a later mount on a shorter point that
covers a deeper one reads as the deeper one. An upper directory whose
path falls under a disk the pod also mounts reads as that disk. Neither
fits a `liken` node.

### A refused store refuses new writeable volumes only

A node with a refused store stages no writeable volume whose directory
the store does not have yet. The stage answers `FailedPrecondition`
with the reason, and the kubelet writes it into the pod's events.

It still serves read-only claims and inline volumes. Their trees are
copies of the remote, and a reboot costs one clone again. A writeable
tree holds work that exists nowhere else until it pushes. A read-only
clone on the root overlay of a `liken` node takes space from the
overlay's 128 MB `tmpfs`, which every write to the root shares. That
cost shows on `git_csi_store_bytes`.

A writeable volume whose directory the store already holds still
stages. A volume the plugin resumed after a restart holds no
credential, so a push to a remote that needs one fails until the pod
restarts. The restart unstages the volume and stages it again with
the pod's `Secret`. Refusing that stage would leave the work in the
tree until the reboot deletes it. The check also comes after the
lookup of volumes the node already staged, so the kubelet's repeated
stage call for a staged volume succeeds.

### What the driver reports

- One log line at the error level at start, `the node refuses
  writeable volumes, because the store does not survive a reboot`,
  with the reason. The reason names the store, the mount point, the
  filesystem type, and the upper directory for an overlay.
- `git_csi_store_refuses_writeable`, a gauge on the node plugin, one
  when the store is refused and zero when it is not.
- The `FailedPrecondition` answer to each refused stage, in the pod's
  events.

The CSI `Probe` still reports ready. A node plugin that is not ready
would refuse read-only volumes too, and the kubelet does not read
`Probe` in this deployment. `NodeGetInfo` has no field for health.

## Considered and set aside

- **Stop the driver.** The open problem named this: fail the start,
  and let the `DaemonSet`'s status show the reason. It loses because a
  node plugin that does not run cannot unpublish a volume, so a pod on
  that node cannot stop cleanly, and a writeable volume already staged
  cannot push its last work. It also takes read-only volumes away from
  the node for a fault that costs them nothing.
- **A `--store-filesystems` flag naming the types a cluster owner
  accepts.** Every type the driver has no rule for is accepted already,
  so the flag would only accept an overlay the table cannot place.
  From inside a pod that overlay is the case this plan exists for. A
  cluster owner with an overlay on a disk points the store at a
  directory on the disk.
- **`statfs` on the store.** It returns the overlay's magic number and
  nothing about the upper directory, so it reads the same for an
  overlay on a disk and one on a `tmpfs`.

## The drill

Owed. In the lab, patch the `store` `hostPath` and `--store` to
`/var/lib/liken/git-csi`, which is on the root overlay.

1. The node plugin logs the refusal at start, and
   `git_csi_store_refuses_writeable` reads one.
2. A `ReadWriteOncePod` claim's pod stays `ContainerCreating`, and its
   events carry the reason.
3. A `ReadOnlyMany` claim's pod reads the greeting.
4. Point the store back at the pod-storage partition. The gauge reads
   zero, and the writeable pod starts.

## What is done when

- The node plugin reads the mount table at start and refuses a store
  on `tmpfs`, `ramfs`, `rootfs`, or an overlay it cannot place on a
  disk.
- A refused store refuses new writeable volumes, serves read-only
  volumes, and stages a writeable volume whose tree it already holds.
- The log line, the gauge, and the stage's answer carry the reason.
- The unit tests read fixture tables for a real disk, a bind mount,
  `tmpfs`, `ramfs`, `rootfs`, a `tmpfs` stacked on a disk, an overlay
  on a `tmpfs`, an overlay whose upper directory has an escaped comma,
  an overlay on a disk, the root overlay of a `liken` node as
  its pod reads it, an overlay with no upper directory, a store with no
  entry of its own, an empty table, and a table that is not there.
  Coverage stays at 100%.
- The install guide has a section on the store's filesystem, the
  writeable guide links it, and the metrics reference has the gauge.
