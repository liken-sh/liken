# Two operators can run at once

The operator must run as one instance. It creates pods, deletes them,
and writes every `Play`'s status, and two instances that do that at the
same time race: both create a pod for one `Play`, or one deletes what
the other just made, or two status writes overwrite each other. The
`Deployment` sets `replicas: 1`, which asks for one instance but does
not enforce it.

Three cases run a second instance, and `replicas: 1` prevents none of
them. A rolling update starts the new pod before it stops the old one,
so a deploy runs two operators for a few seconds. A node partition
makes the `Deployment` controller start a replacement while the old pod
still runs on the unreachable node, so two operators run until the
partition heals. And a person edits `replicas` to `2`, by hand or by a
bad automation, and two operators run by design.

The last case is the reason the fix belongs in the operator code. A
person can patch a guard in the `Deployment` spec away. A guard in the
code applies whatever the spec says.

This operator differs from the hardware operators. The
`audio-operator`, `display-operator`, and `bluetooth-operator` are
`DaemonSet`s: one instance per machine is correct, because each one
manages the hardware on its own machine. This operator is a cluster
singleton, one instance for the whole cluster, and a `Deployment` has
no built-in limit of one instance.

A `Lease` in `coordination.k8s.io` is the fix. The operator acquires a
named `Lease` before it reconciles, renews it on an interval, and stops
reconciling as soon as it cannot renew. Only the holder acts, so a
second pod that cannot take the `Lease` waits and changes nothing.
This is leader election, and it covers all three cases: the extra pod
in a rollout, the replacement pod in a partition, and the extra pod
from a `replicas: 2` patch each wait for a `Lease` they cannot hold. A
`Lease` has one holder, so the replica count no longer matters.

The operator uses no client library, so it implements the acquire and
renew loop against the `Lease` API by hand, the same way it does the
rest of its API access. The loop is small: create the `Lease` if it is
absent, take it if its holder's renewal has expired, write a new
renewal time on the interval, and stop reconciling if a write fails.

The `Lease` also makes a partial high-availability setup possible,
which this operator does not need yet. With leader election in place,
two replicas are safe: the one without the `Lease` waits and does no
work, and it takes over within one `Lease` expiry when the holder
dies. A crash then costs one expiry of downtime instead of a full pod
restart. Building this is a later choice.

`strategy: Recreate` on the `Deployment` fixes only the rollout case.
It stops the old pod before it starts the new one, so a rollout no
longer overlaps, and the `cluster-operator` in `liken` already does
this. A partition and a `replicas` patch still run two operators,
because `Recreate` controls when pods are replaced and leaves the
replica count to the spec.
