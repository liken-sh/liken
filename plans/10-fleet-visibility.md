# Fleet visibility: phases, heartbeats, and the sweep

Milestone 10 — Done

Every state a fleet listing shows is now an enumerated word
instead of a boolean. Machines carry `status.phase`, derived
from the conditions on every pass: Ready, UpdatePending,
Updating, Blocked, Degraded, Booting, Unknown, or Lost. (The
Ready condition remains for `kubectl wait`; only its printer
column went away.) The time boolean became `status.time.state`
(Synchronized/FreeRunning/Unsynchronized, because free-running
by design and unsynchronized by outage are different
situations), and the convergence columns print the conditions'
*reasons* (Converged, RebootPending, RejectedLastBoot, …),
which say what kind of problem exists and what would fix it.
The fleet also detects machines that have gone silent: every
operator heartbeats `status.observedAt`, and the leaders run a
fleet sweep (leaders, because a follower that can reach the
API is by definition reaching a leader) that marks a silent
machine Lost. The sweep is a safe multi-writer because it only
touches machines whose own writer has provably stopped. It
also publishes the Cluster's first status: a phase
(Ready/Updating/Degraded) and a ready-out-of-total headcount
("4/5" in `kubectl get clusters`). A NodeHealthy condition
mirrors the Node's Ready onto the Machine, catching the one
gap the heartbeat can't: the operator lives on the host
network and can keep reporting while the kubelet under it is
dead. One state is deliberately not shown: quorum lost.
Losing a leader majority takes the API down, and the status
writer with it, so a frozen status is itself the symptom.
Health checks surveyed and deferred: leaders cross-checking
each other's clocks, etcd quorum margin as a Cluster condition
(pairs with milestone 13), storage-capacity watermarks (a full
machineState silently breaks staging), and the cluster-wide
clock spread. The fleet sweep already reads every Machine's
status, so it could publish max minus min of the reported
time offsets on the Cluster, one number that says how well
timekeeping is working across the whole fleet. Two findings
came from the first lab run. First, the heartbeat created a
feedback loop: the operator reconciles on every watch event,
including the event its own status write causes, and a
timestamp that moved every pass made every write real, so the
operator wrote to the API server as fast as it could loop.
Renewing observedAt on a cadence instead restores the no-op
writes that let the loop settle. Second, three leaders
sweeping at once worked (the verdicts are deterministic and
optimistic concurrency serialized the writes) but filled the
logs with 409s that were all noise. The sweep now runs under a
coordination.k8s.io Lease, the same leader election
kube-controller-manager uses to run hot standbys, built here
from a GET and two conditional writes. A later idiom-review
pass carried this further: the heartbeat itself moved out of
status into a per-machine Lease in liken-machine-lease,
kube-node-lease's arrangement, escaping the same write
amplification. The whole API was also brought up to metav1's
conventions: typed string vocabularies, conditions validated
like metav1.Condition with observedGeneration, list-type
annotations, admission patterns on spec strings, Cluster
conditions beneath its phase, watch bookmarks, and a coverage
gate ratcheted past half.
