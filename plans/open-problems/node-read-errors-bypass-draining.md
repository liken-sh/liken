# Keep Node-read errors from bypassing the drain

Open bug. Review priority: medium. A failed `Node` read lets an approved
reboot proceed without draining. The code treats transient API failures
like the expected absence of a deleted node during demotion.

## Failure path

`disruptions.gate` in [reconcile.go](../../machine-operator/reconcile.go)
calls `gateThroughDrain` only when a reboot is requested, the conductor
has granted a turn, and `nodeErr == nil`. On a timeout, server error,
or any other read failure, it leaves `requestReboot` enabled.

The comment explains the intended exception: after demotion deletes the
`Node`, there is no object to cordon, but the machine still needs to
reboot. The condition does not distinguish that lifecycle state from an
API error on a node that still runs workloads.

A transient read failure on the relevant pass is sufficient. The
machine then follows its shutdown sequence without first using the
Eviction API. Its workloads get no drain-level protection from their
`PodDisruptionBudget`, and the convergence still reports the normal
reboot-requested result rather than a drain-read failure.

## Evidence

A temporary fixture called the gate with a read error, a granted turn,
and a convergence requesting reboot. The result still requested reboot
without making a drain client call. This verifies the gate's behavior;
no live workload eviction or reboot drill was performed.

The existing drain logic is in [drain.go](../../machine-operator/drain.go).
The defect is the decision to skip it, not the eviction step itself.

## Proposed safeguard

Hold the reboot when the `Node` read fails unless an explicit, expected
lifecycle state authorizes bypass. A bare `404` is not sufficient proof
that demotion caused the absence. Retry the read and expose the reason
for waiting through the existing convergence condition.

Check demotion separately. `carryOutDemotion` in
[demotion.go](../../machine-operator/demotion.go) writes its reboot
intent before deleting the `Node`, because deletion can terminate its
own pod. That intent uses the runtime channel on tmpfs; it is not a
power-loss-durable record. The repair must preserve this ordering and
must not strand a machine whose `Node` was deliberately removed.

## Remedy scope

**Focused fail-closed reliability work.** Distinguishing an expected
absence from an unavailable API preserves the intended behavior: drain
before an ordinary approved reboot, and complete authorized demotion.
It does not require a new user-facing disruption policy.

The existing five-minute `drainDeadline` deliberately allows reboot
when workloads do not leave in time. This fix must not silently change
that limit, `rebootPolicy`, or the conductor's grant rules. Whether PDBs
should block indefinitely is a separate policy question.

## Tests needed

Cover timeouts, `500` responses, unrelated `404` responses, and the
explicit demotion path. Assert that unexpected read failures hold the
reboot and become visible in status. Verify successful retry resumes
draining, the existing deadline still applies, and a demoted machine
can still reboot and re-register after its `Node` is deleted.
