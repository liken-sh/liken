# Two operators can run at once

The operator must be one instance. It creates pods, deletes them, and
writes every `Play`'s status, and two instances doing that at once
race: both create a pod for one `Play`, or one deletes what the other
just made, or two status writes overwrite each other. The
`Deployment` sets `replicas: 1`, which asks for one instance without
enforcing it.

Three cases break the single replica, and `replicas: 1` prevents none
of them. A rolling update starts the new pod before it stops the old
one, so a deploy runs two operators for a few seconds. A node
partition makes the `Deployment` controller start a replacement while
the old pod still runs on the unreachable node, so two operators run
until the partition heals. And a person edits `replicas` to `2`, by
hand or by a bad automation, and two operators run by design.

The last case is the reason the fix belongs in the operator code. A
guard in the `Deployment` spec is a guard a person can patch away. A
guard in the code holds whatever the spec says.

The shape is worth naming, because it differs from the hardware
operators. The `audio-operator`, `display-operator`, and
`bluetooth-operator` are `DaemonSet`s: one instance per machine is
correct, because each answers for the hardware on its own machine.
This operator is a cluster singleton, one instance for the whole
cluster, and a singleton `Deployment` has no built-in bound of one.

A `Lease` in `coordination.k8s.io` is the fix. The operator acquires a
named `Lease` before it reconciles, renews it on an interval, and
stops reconciling the moment it cannot renew. Only the holder acts, so
a second pod that cannot take the `Lease` waits and changes nothing.
This is leader election, and it closes all three cases: the surge pod
in a rollout, the replacement pod in a partition, and the extra pod
from a `replicas: 2` patch each wait for a `Lease` they cannot hold.
The replica count stops mattering, because one `Lease` has one holder.

The operator carries no client library, so it implements the acquire
and renew loop against the `Lease` API by hand, the way it does the
rest of its API access. The loop is small: create the `Lease` if it is
absent, take it if its holder's renewal has expired, write a new
renewal time on the interval, and give up reconciling if a write
fails.

The `Lease` also opens a quasi-HA path, which this operator does not
need yet. With leader election in place, running two replicas is safe:
the one without the `Lease` waits and holds no work, and it takes
over within one `Lease` expiry when the holder dies. A crash then
costs one expiry of downtime instead of a full pod restart. The path
is worth naming, and building it is a later choice.

`strategy: Recreate` on the `Deployment` closes only the rollout case.
It stops the old pod before it starts the new one, so a rollout no
longer overlaps, and the `cluster-operator` in liken already does
this. A partition and a `replicas` patch stay open, because `Recreate`
governs when pods roll over and leaves the replica count to the spec.
Only the `Lease` in the code closes all three.
