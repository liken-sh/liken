# Two cluster operators can run at once

Open problem. The cluster operator must be one instance for the whole
cluster. It rewrites the AddOn manifests, grants rollout turns through
the conductor, and writes the `Cluster` status. Two instances doing
that at once race across the whole fleet: both grant a turn, or both
rewrite a manifest, or two status writes overwrite each other. The
`Deployment` sets `replicas: 1`, which asks for one instance without
enforcing it.

## What breaks the single replica

Three cases run a second operator, and `replicas: 1` prevents none of
them.

- A node partition makes the `Deployment` controller start a
  replacement while the old pod still runs on the unreachable node.
  Two operators run until the partition heals.
- A person edits `replicas` to `2`. The field is part of the
  `Deployment` spec, so anyone who can patch the `Deployment` can set
  it, by hand or by a bad automation. Two operators then run by
  design.
- `strategy: Recreate` on the `Deployment` closes only the rollout
  case. It stops the old pod before it starts the new one, so a normal
  release never overlaps. A partition and a `replicas` patch stay
  open, because `Recreate` governs when pods roll over and leaves the
  replica count to the spec.

The last case is the reason the fix belongs in the operator code. A
guard in the `Deployment` spec is a guard a person can patch away. A
guard in the code holds whatever the spec says.

## The fix is a leader lease

The operator acquires a named `Lease` in `coordination.k8s.io` before
it acts, renews it on an interval, and stops acting the moment it
cannot renew. Only the holder rewrites manifests, grants turns, and
writes status. A second operator, from a partition or a `replicas: 2`
patch, cannot take the `Lease`, so it waits and changes nothing. The
replica count stops mattering, because one `Lease` has one holder.

liken already runs on this primitive. The machine operator renews a
heartbeat `Lease` so the cluster operator can read the fleet's
liveness in `fleet.go`, the same way the kubelet renews a node's
`Lease`. A leader `Lease` for the cluster operator is that mechanism
turned on the operator itself, and the machine operator's lease-write
loop is the model for the acquire and renew code.

## The media operator has the same problem

The `media-operator`, which runs on a liken cluster above the hardware
operators, is also a cluster singleton `Deployment` with the same gap.
Its own `plans/open-problems/two-operators-can-run-at-once.md` records
it. The two want the same answer, a leader `Lease` enforced in code,
and neither should rely on the `Deployment` spec to bound its replica
count.

## The quasi-HA path

The `Lease` also makes two replicas safe, which this operator does not
need yet. The instance without the `Lease` waits and holds no work,
and it takes over within one `Lease` expiry when the holder dies. A
crash then costs one expiry of downtime. The path is worth naming, and
building it is a later choice.
