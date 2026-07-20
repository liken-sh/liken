# Fleet visibility: phases, heartbeats, and the sweep

Milestone 10 — Done

Every state that a fleet listing shows is now an enumerated word,
instead of a boolean. Machines carry `status.phase`, derived from
the conditions on every reconcile pass: Ready, UpdatePending,
Updating, Blocked, Degraded, Booting, Unknown, or Lost. The Ready
condition remains, for `kubectl wait` to use; only its printer
column went away.

The time boolean became `status.time.state`, with values
Synchronized, FreeRunning, or Unsynchronized. Free-running by design
and unsynchronized by outage are different situations, so they need
different states. The convergence columns now print the conditions'
*reasons* (Converged, RebootPending, RejectedLastBoot, and others),
which say what kind of problem exists and what would fix it.

The fleet also detects machines that have stopped reporting. Every
operator sends a heartbeat by updating `status.observedAt`. The
leaders run a fleet sweep that marks an unresponsive machine Lost.
Leaders run the sweep because a follower that can reach the API is,
by definition, reaching a leader. The sweep is a safe multi-writer,
because it only touches machines whose own writer has provably
stopped.

The sweep also publishes the Cluster's first status: a phase (Ready,
Updating, or Degraded) and a ready-out-of-total headcount, shown as
"4/5" in `kubectl get clusters`.

A NodeHealthy condition mirrors the Node's Ready condition onto the
Machine. This catches a gap that the heartbeat alone cannot: the
operator runs on the host network, so it can keep reporting even
while the kubelet under it has stopped running.

One state is deliberately not shown: quorum lost. Losing a leader
majority takes the API down, and it takes the status writer down
with it. So a frozen status is itself the symptom of quorum loss.

The design surveyed several health checks and deferred them: leaders
cross-checking each other's clocks, etcd quorum margin as a Cluster
condition (which pairs with milestone 13), storage-capacity
watermarks (a full machineState silently breaks staging), and the
cluster-wide clock spread. The fleet sweep already reads every
Machine's status, so it could publish the difference between the
highest and lowest reported time offset on the Cluster: one number
that shows how well timekeeping works across the whole fleet.

The first lab run produced two findings. First, the heartbeat
created a feedback loop. The operator reconciles on every watch
event, including the event that its own status write causes. A
timestamp that moved on every pass made every write count as a real
change, so the operator wrote to the API server as fast as it could
loop. Renewing observedAt on a fixed cadence instead restores the
no-op writes that let the loop settle.

Second, three leaders sweeping at the same time worked correctly:
the verdicts are deterministic, and optimistic concurrency
serialized the writes. But it filled the logs with 409 responses
that were all noise. The sweep now runs under a coordination.k8s.io
Lease, the same mechanism that kube-controller-manager uses for
leader election to run hot standbys. This build constructs the Lease
from a GET and two conditional writes.

A later idiom-review pass carried this further: the heartbeat itself
moved out of status and into a per-machine Lease, stored beside the
operator in liken-system, using the same mechanism as
kube-node-lease. This avoids the same write-amplification problem.
The whole API was also brought up to metav1's conventions: typed
string vocabularies; conditions validated like metav1.Condition,
with observedGeneration; list-type annotations; admission patterns
on spec strings; Cluster conditions listed beneath its phase; watch
bookmarks; and a coverage gate raised past half.
</content>
