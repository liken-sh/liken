# Credentials after a restart

## The problem

A writeable volume's `Secret` reaches the driver in the stage call and
nowhere else. The driver keeps it in memory for the life of the volume
and writes it to disk only around a git call. When the driver's node
pod restarts, the volume resumes from its record with no credential,
the report says so, and every push fails until the pod that mounts the
volume restarts and the kubelet stages it again.

For a public repository nothing is lost. For the first real writeable
use, an application's configuration on a private forge, a routine
driver upgrade turns into a push outage that lasts until someone
restarts the application.

## What is known

- The driver has an API client on the node, and `arming.claimOf` already
  finds the `PersistentVolumeClaim` bound to a volume handle by listing
  `PersistentVolume`s. The `PersistentVolume` carries
  `nodeStageSecretRef`, a name and a namespace.
- Reading that `Secret` needs `get` on `secrets`, which the node's
  `ClusterRole` does not grant today. Granting it cluster-wide gives a
  privileged DaemonSet read access to every `Secret` in the cluster,
  which is a wider grant than the driver needs.
- Writing the credential to the store would survive a restart with no
  grant, and would put a private key on the node's disk for the life of
  the volume instead of the length of a git call.

## What would settle it

A decision between the two shapes: read the `Secret` back through the
API at resume, with the grant that needs, or keep the credential on the
node's disk with the store's permissions. Then a lab drill: stage a
writeable volume with a deploy key against the forge, delete the
driver's pod, write in the application pod, and see the push land.
