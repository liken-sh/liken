# A reboot skips the drain on any failed Node read

Open bug, of medium priority. When machine-operator's read of the
`Node` fails on the pass that carries out an approved reboot, the
machine reboots without a drain. No `PodDisruptionBudget` protects its
workloads, and status reports a normal reboot.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17. The fix rests on milestone 94's rule: when
the code cannot confirm a state, it must refuse or report, and never
act.

## The evidence

`disruptions.gate` (`machine-operator/disruptions.go`) calls
`gateThroughDrain` only when a reboot is requested, the conductor
granted a turn, and the Node read succeeded
(`machine-operator/disruptions.go:69`). On a timeout, a server error,
or any other read failure, it leaves `requestReboot` set. The comment
gives demotion as the reason, when there is no Node to cordon. The
condition does not distinguish that lifecycle state from an API error
on a node that still runs workloads.

A 500 or a timeout on one pass has the same effect: an approved reboot
skips eviction. The machine then follows its shutdown sequence without
the Eviction API, so no `PodDisruptionBudget` protects its workloads.
The convergence still reports the normal reboot-requested result, and
it reports no drain-read failure.
`TestAGrantedRebootWithNoNodeSkipsTheDrain`
(`machine-operator/disruptions_test.go:16`) asserts the skip on
purpose, with a server error as its input.

A temporary fixture called the gate with a read error, a granted turn,
and a convergence that requested a reboot. The result still requested
the reboot, and the gate made no call to the drain client. This
verifies only the gate's behavior. No live eviction or reboot drill
ran. The defect is the decision to skip the drain, not the eviction
step in `machine-operator/drain.go`.

## The fix

The fix holds the reboot when the Node read fails, unless an explicit,
expected lifecycle state allows the bypass. A 404 during a demotion
skips the drain. A bare 404 does not show that demotion caused the
absence. Any other error stops the reboot, retries the read, and
reports why through the convergence condition and `NodeObserved`. The
open problem
[conditions this pass did not check are stamped as current](conditions-this-pass-did-not-check-are-stamped-as-current.md)
proposes `NodeObserved`, which does not exist in the code today, so the
two fixes share it.

The demotion path keeps its order. `carryOutDemotion`
(`machine-operator/demotion.go:105`) writes its reboot intent before it
deletes the Node, because the deletion can terminate its own pod. That
intent uses the runtime channel on tmpfs, so it does not survive a
power loss. The fix must not strand a machine whose Node was removed
on purpose.

The fix needs no new user-facing disruption policy. The five-minute
`drainDeadline` (`machine-operator/drain.go:65`) allows a reboot on
purpose when workloads do not leave in time. The fix does not change
that limit, `rebootPolicy`, or the conductor's grant rules. Whether a
`PodDisruptionBudget` should block a reboot with no limit is a separate
policy question.

The tests cover a timeout, a `500`, an unrelated `404`, and the
explicit demotion path. An unexpected read failure must hold the
reboot and show in status. A successful retry must resume the drain,
the existing deadline must still apply, and a demoted machine must
still reboot and register again after its Node is deleted.

## What the lab can measure

The drill runs under QEMU, and needs no metal. A guest whose API
server returns 500 to the Node read for one pass while a reboot is
approved: the reboot must hold, and the drain must not be skipped. The
same drill checks the Node conditions for the open problem on stamped
conditions.
