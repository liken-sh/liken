# 88. A component takes a reboot turn

Milestone 88. Proposed 2026-10-21. Not built. An add-on that changes
a machine below Kubernetes needs the same protection that a release
gets: the disruption budget, one leader at a time, a drain, and a
reboot that only the rollout conductor orders. This milestone lets
any component ask for a machine's turn, do its work on the drained
machine, and then let the machine reboot. `firmware-operator` is the
first user: its
[plan 06](../../firmware-operator/plans/06-applying-a-database-update.md)
writes a Secure Boot database inside a turn, and its plan 08 applies
a capsule inside one. [Milestone 34](34-gpu-add-ons.md)'s GPU add-ons
can use the same turn. [Milestone 33](33-firmware-updates.md) holds
the rest of the OS's part of firmware updates.

## The problem

Two kinds of reboot get a turn today. A staged document's reboot
waits for the turn as the tail of its convergence. A requested reboot,
the `liken.sh/request-reboot` annotation on a `Machine`, asks for one
reboot with no change to apply. Both take the same path in
`gateDisruption` in `machine-operator/converge.go`:

1. `rebootPolicy` gates the reboot. `Manual` waits for a person's
   `liken.sh/approve-disruption` annotation.
2. machine-operator sets the reason `AwaitingTurn` on the condition,
   and `wantsTurn` in `cluster-operator/rollout.go` reads it.
3. The conductor records the machine in `status.rebootTurns` on the
   `Cluster` ([milestone 82](completed/82-the-cluster-records-each-reboot-turn.md))
   and writes `RebootApproved` on the `Machine`.
4. machine-operator cordons the node and evicts its pods
   (`machine-operator/drain.go`). When the node is clear or the drain
   deadline passes, it asks `init` to reboot.

A component that writes firmware needs a step between 4's drain and
its reboot, and the request annotation has none: the reboot follows
the drain in the same pass. The drain also evicts every pod that is
not a DaemonSet's or a mirror pod, so a component's pod that started
before the drain does not survive it.

The annotation has a second limit. A component that writes it needs
`patch` on `machines`, and that verb also lets it change any
machine's spec. A turn for an add-on must not need a verb that can
edit a `Machine`.

## What the turn must offer

- **A request that any component can write.** It names one machine
  and the component that asks. Writing it needs no verb on
  `Machine`.
- **The same gates as every reboot.** `rebootPolicy`, the person's
  approval for `Manual`, the conductor's budget, one leader at a time,
  and stall detection. The conductor counts a component's turn as it
  counts a release's, so a fleet never loses more machines than its
  budget allows.
- **A visible point where the machine is drained.** The component
  starts its work only after the drain, so the drain does not evict
  it. Its pod tolerates the cordon.
- **A release by the component.** The component says when its work
  is done, and machine-operator then asks `init` to reboot. A
  component that fails says so too, and the machine still reboots,
  because its workloads are already gone. A reboot after a failed
  database write changes nothing, and a reboot after a failed capsule
  stage boots the proven slot.
- **A deadline.** A component that never answers holds the turn
  until a deadline, and the machine then reboots without it. The
  deadline keeps a lost component from holding the fleet's budget.
- **One change per reboot.** A machine that has a staged release
  gets no component turn, and a machine in a component turn stages
  no release until the reboot. Milestone 33 needs this rule for
  capsules. It also gives a failed proving boot one cause.

## Shapes to choose from

None is chosen. Each fits the list above, and they differ in who
owns the request.

- **A request object.** A new resource in `liken.sh`, one for each
  turn, with `spec.machine` and `spec.requester`. The conductor reads
  it and writes the grant into its status, and the component writes
  its release into the same status. It keeps one writer of `Machine`
  status, and RBAC on the new resource scopes the component to
  requests. This is the likely choice.
- **A condition that the component writes on `Machine` status.** The
  conductor already reads `AwaitingTurn` on any condition. This needs
  no new resource, but it gives `Machine` status a second writer,
  which every design so far avoided.
- **A field on the `Cluster`.** The ledger of milestone 82 is already
  on the `Cluster`. A component appends a request there. This puts
  requests from add-ons into the OS's own object.

## Open questions

- Whether the request object is namespaced, so a component's RBAC is
  a `Role` in its own namespace, or cluster-scoped beside `Machine`.
- How the turn reports progress to a person: on the request, on the
  `Machine`'s `Progressing` message, or both.
- The deadline's length, and whether the requester sets it within a
  bound.
- How `kubectl liken request-reboot` and the annotation relate to
  the new request. The annotation can stay as a person's request with
  no work inside the turn.

## Proof

On stick-1, `firmware-operator` asks for a turn and writes nothing.
The machine drains, the component's pod starts after the drain and
releases the turn, and the machine reboots and rejoins. A second run
leaves the component silent, and the deadline releases the turn.
