# The driver forgets its volumes when it restarts

The node plugin holds the set of published volumes in memory: which
volume is at which target path, which repository it follows, and what
its condition is. A `DaemonSet` rollout, a crash, or a node reboot
starts a driver that holds none of it.

The mounts survive, because they belong to the kernel, and a pod keeps
reading its tree. What stops is everything the driver does for a
volume it does not know: the fetch loop, the condition, and the
answer to `NodeGetVolumeStats`, which becomes `NotFound`. The kubelet
publishes a volume again only when it has a reason to, so a volume
can sit unfollowed until its pod restarts.

The plain answer is that the volume's directory in the store already
carries a checkout, and it could carry the attributes and the target
path too, as a small file the driver writes at publish and reads at
start. A driver that starts would then rebuild its set from the store,
check each target path is still a mount, and resume the loops. Plan 04
gives writeable volumes a work tree that has to persist across
restarts anyway, so the record belongs to that plan or to one beside
it.
