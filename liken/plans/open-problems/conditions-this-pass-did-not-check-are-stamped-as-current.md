# Conditions this pass did not check are stamped as current

Open bug. When machine-operator's read of the `Node` fails, the
`Machine`'s Node conditions keep their previous values but take the
current `observedGeneration`. A person or a controller that reads them
takes old results for results of this pass.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17. The fix rests on milestone 94's rule: when
the code cannot confirm a state, it must refuse or report, and never
act. So a condition that this pass did not check is not stamped as
current again.

## The evidence

The status writer sets `observedGeneration` on every condition except
`RebootApproved` at the end of each pass
(`machine-operator/reconcile.go:466` to `:471`). When the Node read
failed, `NodeHealthy`, `NodeLabelsApplied`, `NodeTaintsApplied`, and
`NodeCurrent` carry forward from the previous status untouched, and
then get stamped with the current generation. A reader takes those
conditions as results of this pass.

## The fix

The first fix proposed adds one condition, `NodeObserved`, that says
whether this pass read the Node. When it is False, the conditions that
depend on the Node are written as `Unknown`, with a message that names
the read error. The drain fix of the open problem
[a reboot skips the drain on any failed Node read](a-reboot-skips-the-drain-on-any-failed-node-read.md)
reports through the same condition. But `Unknown` would turn `Ready`
False on every failed read, and a machine whose phase changes can hold
the fleet's reboot turns. The smaller fix keeps the previous
`observedGeneration` on each condition the pass carried forward.
`NodeObserved` does not exist in the code today.

## What the lab can measure

The drill runs under QEMU, and needs no metal. A guest whose API
server returns 500 to the Node read for one pass while a reboot is
approved: the Node conditions must not claim this pass's generation.
The same drill checks the drain for the open problem on the skipped
drain.
