# Rolling reboots at the cluster level

Milestone 13 — Done

Rolling reboots at the *cluster* level: the fleet applies
staged changes without a human supervising it, one machine at
a time. (This milestone was written as "rolling upgrades,"
but the sequencing turned out to be independent of what the
reboot applies: a config change today, a version upgrade once
milestone 12 exists. The machinery is the same either way.)
On a cluster member, rebootPolicy: Auto now means "reboot
when the cluster says it's safe": the machine stages its
change, publishes AwaitingTurn, and waits for the sweep
leader (already elected, already reading the whole fleet) to
grant its turn by writing a RebootApproved condition onto the
Machine, the way the scheduler owns PodScheduled on Pods it
doesn't manage. The budget is one field,
spec.disruption.maxUnavailable (default 1), a machine-level
PodDisruptionBudget reduced to one number. It counts
unplanned trouble too, so a fleet that is already degraded
pauses its own rollout, and the leaders have an automatic
floor no budget can raise: one leader down at a time, because
quorum depends on a majority of members and no budget setting
changes that. A granted machine drains itself first: it
cordons its own Node, evicts everything movable through the
Eviction API so workloads' own PDBs hold, and then writes the
reboot intent. The drain proceeds incrementally across
reconcile passes, since a blocked pass would stop the
heartbeat and read as a death. The machine uncordons itself
after converging; a human's cordon stays put. The sweep
treats silence from a granted machine as the reboot it asked
for, and a machine that never returns flips the Cluster's
Progressing condition to False/RolloutStalled (Deployment's
vocabulary) and halts granting until someone looks.
Demotion reboots join the same queue. Still owed, someday:
workload-aware ordering; a drain that waits on more than a
deadline when a PDB can never be satisfied; and strict
workers-first ordering at rollout start. Today the order is
among machines that have *asked* by sweep time, so a leader
that stages quickly (the sweep leader itself, whose sweep
runs in the same pass as its own staging) can take the first
turn before a slower worker has asked. That is safe either
way; one leader at a time holds regardless.
