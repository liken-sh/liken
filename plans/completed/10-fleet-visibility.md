# Fleet visibility: phases, heartbeats, and the sweep

Milestone 10. Completed. A fleet listing shows a named phase for each
machine, and the leaders mark a machine that stops reporting as Lost.

Every state that a fleet listing shows is now an enumerated word,
instead of a boolean. Machines have `status.phase`, derived from the
conditions on every reconcile pass: Ready, UpdatePending, Updating,
Blocked, Degraded, Booting, Unknown, or Lost. The Ready condition
remains, for `kubectl wait` to use. Only its printer column went away.

The time boolean became `status.time.state`, with the values
Synchronized, FreeRunning, or Unsynchronized. Free-running by design
and unsynchronized by outage are different situations, so they need
different states. The convergence columns now print the conditions'
*reasons* (Converged, RebootPending, RejectedLastBoot, and others),
which say what kind of problem exists and what would correct it.

The fleet also detects machines that stopped reporting. Every operator
sends a heartbeat with an update to `status.observedAt`. The leaders
run a fleet sweep that marks an unresponsive machine Lost. Leaders run
the sweep because a follower that can reach the API is, by definition,
reaching a leader. The sweep is a safe multi-writer, because it only
writes to machines whose own writer has stopped.

The sweep also publishes the Cluster's first status: a phase (Ready,
Updating, or Degraded) and a ready-out-of-total headcount, shown as
"4/5" in `kubectl get clusters`.

A NodeHealthy condition mirrors the Node's Ready condition onto the
Machine. This catches a gap that the heartbeat alone cannot. The
operator runs on the host network, so it can continue to report while
the kubelet under it has stopped.

One state is not shown: quorum lost. The loss of a leader majority
takes the API down, and it takes the status writer down with it. A
frozen status is the symptom of quorum loss.

The design surveyed several health checks and deferred them: leaders
that cross-check each other's clocks, etcd quorum margin as a Cluster
condition (which pairs with milestone 13), storage-capacity watermarks
(a full machineState breaks staging silently), and the cluster-wide
clock spread. The fleet sweep already reads every Machine's status, so
it could publish the difference between the highest and lowest reported
time offset on the Cluster: one number that shows how well timekeeping
works across the whole fleet.

The lab run found two problems. First, the heartbeat created a feedback
loop. The operator reconciles on every watch event, including the event
that its own status write causes. A timestamp that moved on every pass
made every write count as a real change, so the operator wrote to the
API server as fast as it could loop. A renewal of observedAt on a fixed
cadence restores the no-op writes that let the loop settle.

Second, three leaders that swept at the same time worked correctly. The
verdicts are deterministic, and optimistic concurrency serialized the
writes. But it filled the logs with 409 responses that were all noise.
The sweep now runs under a coordination.k8s.io Lease, the same
mechanism that kube-controller-manager uses for leader election to run
hot standbys. This build constructs the Lease from a GET and two
conditional writes.

A later review pass moved the heartbeat itself out of status and into a
per-machine Lease, stored next to the operator in liken-system, with
the same mechanism as kube-node-lease. This avoids the same
write-amplification problem. The whole API also came up to metav1's
conventions: typed string vocabularies; conditions validated like
metav1.Condition, with observedGeneration; list-type annotations;
admission patterns on spec strings; Cluster conditions listed beneath
its phase; watch bookmarks; and a coverage gate raised above half.
