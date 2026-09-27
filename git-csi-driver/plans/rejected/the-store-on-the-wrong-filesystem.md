# The store on the wrong filesystem

Rejected on 2026-09-27. This problem proposed that the driver refuse
to serve when its store is on a filesystem that the kernel keeps in
memory. A check was built and then removed, for these reasons:

- A read-only volume is a copy of the remote. Where its tree lives
  does not matter, and a reboot costs one clone.
- A writeable volume on a store in memory is valid too. A pod can
  want a writeable tree that lasts no longer than the node's uptime,
  and the loss of a commit that did not push is an accepted cost.
- Where the store lives is the cluster owner's choice, made in the
  `hostPath` of the DaemonSet. The driver does not second-guess it.

The original text follows.

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
  `/var/lib/liken/git-csi` and ended up on the overlay. Moving the
  default fixed the lab and left the general case open.
- `/proc/self/mountinfo` says which mount a path is on. The driver
  already reads it, in `records.go`, to learn whether a target is still
  mounted.
- The root overlay on a `liken` node is `overlay` on `tmpfs`. A check
  that refuses `tmpfs`, `overlay`, and `ramfs` would catch the case on
  `liken`. A general cluster may put its store on a filesystem that
  the driver has no rule for.

## What would settle it

A rule the driver applies at start: the store's directory has to be on
a mount of its own, or on a filesystem the driver accepts, and a store
that fails the rule stops the driver with the reason in its log and in
the DaemonSet's status. A `--store-filesystems` flag could name what a
cluster owner accepts. Then a lab boot with the `hostPath` pointed at
the overlay, and the pod refused with the line in its log.
