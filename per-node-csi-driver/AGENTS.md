# Working on per-node-csi-driver

`per-node-csi-driver` is a Go CSI driver. `README.md` says what it is
for and describes the objects that declare a volume.

## How it runs

The driver name is `per-node.liken.sh`. The node plugin is the only
workload. It is a `DaemonSet` in `liken-system` that tolerates every
taint. A `liken` node registers with taints, and the plugin must run
on every node where a pod can run, because the plugin makes the pod's
copy. The plugin's driver container is privileged so that it can make
the bind mount. It runs beside the upstream
`csi-node-driver-registrar`.

To publish a volume, the plugin runs `mkdir` for the copy and
bind-mounts the copy onto the pod's target path. The node's store
contains each copy, and a record of each target path where a copy is
published. The plugin watches the `PersistentVolume`s of this driver,
and it removes a copy when no `PersistentVolume` has its handle. It
posts a `Warning` `Event` on the pod for each handle that it rejects
and each mount that fails. It serves the size of each copy in bytes
as a metric. Its RBAC lets it read and watch `PersistentVolume`s and
write `Event`s, and nothing else.

## Design

One volume has one directory on each node, and the copy outlives its
pod. An unpublish unmounts the copy and removes nothing. The plugin
removes a copy only when its volume is deleted, only after the watch
has synced, and only while no pod uses the copy. The plugin learns
which pods use a copy from its records on disk, not from memory
alone. So a restart of the plugin loses no record of a pod that uses
a copy, and the cleanup does not remove a copy that a pod has
mounted.

The access mode decides which pods mount a copy. Every pod on a node
that publishes a handle gets that node's copy. Kubernetes, not the
driver, enforces `ReadWriteOncePod` and `ReadWriteMany`. Every user
can write a copy, and the kubelet changes no owners in it. So a pod
that runs as any user can write the copy, and the kubelet does not
change the owner of every file in a large database at each start.

Nothing in this directory is installed on a real cluster until a
person has seen it work, so every drill runs in `lab/`. `lab/` boots
one `liken` machine in QEMU from the public release channel.

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.
