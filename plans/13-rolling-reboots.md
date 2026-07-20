# Rolling reboots at the cluster level

Milestone 13 — Done

Rolling reboots work at the *cluster* level. The fleet applies staged
changes one machine at a time. No human supervises the process.

(The milestone was first written as "rolling upgrades." But the
sequencing turned out to be independent of what the reboot applies.
Today a reboot can apply a config change. After milestone 12 exists, a
reboot can apply a version upgrade. The machinery is the same either
way.)

On a cluster member, `rebootPolicy: Auto` now means the machine
reboots when the cluster says it is safe. The machine stages its
change and publishes `AwaitingTurn`. Then it waits for the sweep
leader to grant its turn. The sweep leader is already elected and
already reads the whole fleet. The sweep leader grants a turn by
writing a `RebootApproved` condition onto the Machine, the way the
scheduler owns `PodScheduled` on Pods it does not manage.

The budget is one field: `spec.disruption.maxUnavailable` (default 1).
This field is a machine-level PodDisruptionBudget reduced to one
number. The budget also counts unplanned trouble, so a fleet that is
already degraded pauses its own rollout. The leaders have an automatic
floor that no budget can raise: only one leader can be down at a time.
This floor holds because quorum depends on a majority of members, and
no budget setting changes that.

A granted machine drains itself first. It cordons its own Node. It
evicts everything movable through the Eviction API, so that workloads'
own PodDisruptionBudgets still hold. Then it writes the reboot intent.
The drain proceeds in small steps across reconcile passes, because a
blocked pass would stop the heartbeat, and the sweep would read that
as the machine's death.

The machine uncordons itself after it converges. A cordon that a human
set stays in place.

The sweep treats silence from a granted machine as the reboot it asked
for. If a machine never returns, the sweep sets the Cluster's
`Progressing` condition to `False/RolloutStalled` (this is
Deployment's vocabulary), and halts granting until someone looks at
the problem. Demotion reboots join the same queue.

Some work is still owed: workload-aware ordering; a drain that waits
longer than a deadline when a PodDisruptionBudget can never be
satisfied; and strict workers-first ordering at the start of a
rollout. Today, the order follows which machines have *asked* by
sweep time. A leader that stages its change quickly can take the
first turn before a slower worker has asked, because the sweep
leader's own sweep runs in the same pass as its own staging. This
order is safe either way, because only one leader at a time can be
down, regardless of the order.
