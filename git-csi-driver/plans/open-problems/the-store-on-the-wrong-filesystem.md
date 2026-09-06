# The store on the wrong filesystem

## The problem

The store defaults to `/var/lib/liken/pod-storage/git-csi`, which on a
`liken` node is the pod-storage partition. A node without that
partition, or a cluster that is not `liken`, has no such mount, and the
`hostPath` volume creates the directory on the root filesystem. On a
`liken` node the root is a RAM overlay, so every bare repository and
every work tree is lost at reboot, and a writeable volume that had not
pushed loses its work.

The driver starts, serves, and reports nothing, because the directory
exists and is writable. The loss shows at the next reboot.

## What is known

- The lab found this once already, when the store was at
  `/var/lib/liken/git-csi` and fell through to the overlay. Moving the
  default fixed the lab and left the general case open.
- `/proc/self/mountinfo` says which mount a path is on. The driver
  already reads it, in `records.go`, to learn whether a target is still
  mounted.
- The root overlay on a `liken` node is `overlay` on `tmpfs`. A check
  that refuses `tmpfs`, `overlay`, and `ramfs` would catch the case on
  `liken`. A general cluster may put its store on a filesystem the
  driver cannot judge.

## What would settle it

A rule the driver applies at start: the store's directory has to be on
a mount of its own, or on a filesystem the driver accepts, and a store
that fails the rule stops the driver with the reason in its log and in
the DaemonSet's status. A `--store-filesystems` flag could name what a
cluster owner accepts. Then a lab boot with the `hostPath` pointed at
the overlay, and the pod refused with the line in its log.
