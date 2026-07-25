# Rolling reboots at the cluster level

Milestone 13 — Completed. The fleet applies staged changes one machine
at a time, with no manual steps.

Rolling reboots work at the cluster level. The milestone was first
written as "rolling upgrades", but the sequencing is independent of
what the reboot applies. A reboot can apply a config change. After
milestone 12, a reboot can also apply a version upgrade. The machinery
is the same for both.

On a cluster member, `rebootPolicy: Auto` means the machine reboots
when the cluster says it is safe. The machine stages its change and
publishes `AwaitingTurn`. It then waits for the sweep leader to grant
its turn. The sweep leader is already elected and already reads the
whole fleet. The sweep leader grants a turn by writing a
`RebootApproved` condition onto the Machine, in the same way the
scheduler owns `PodScheduled` on Pods it does not manage.

The budget is one field: `spec.disruption.maxUnavailable`, default 1.
This field is a machine-level PodDisruptionBudget reduced to one
number. The budget also counts unplanned trouble, so a fleet that is
already degraded stops its own rollout. The leaders have an automatic
floor that no budget can raise: only one leader can be down at a time.
This floor holds because quorum needs a majority of the members, and
no budget setting changes that.

A granted machine drains itself first. It cordons its own Node. It
evicts everything movable through the Eviction API, so the workloads'
own PodDisruptionBudgets still hold. It then writes the reboot intent.
The drain runs in small steps across the reconcile passes, because a
blocked pass stops the heartbeat, and the sweep reads that as the
machine's death.

The machine uncordons itself after it converges. A cordon that a
person set stays in place.

The sweep reads silence from a granted machine as the reboot it asked
for. If a machine does not return, the sweep sets the Cluster's
`Progressing` condition to `False/RolloutStalled`, which is
Deployment's vocabulary, and stops granting turns until a person
examines the problem. Demotion reboots use the same queue.

Some work is still open: workload-aware ordering; a drain that waits
longer than a deadline when a PodDisruptionBudget can never be
satisfied; and strict workers-first ordering at the start of a
rollout. Today the order follows which machines asked before the sweep
time. A leader that stages its change quickly can take the first turn
before a slower worker asks, because the sweep leader's own sweep runs
in the same pass as its own staging. The order is safe either way,
because only one leader can be down at a time.
