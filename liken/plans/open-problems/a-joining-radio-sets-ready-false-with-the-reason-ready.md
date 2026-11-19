# A joining radio sets Ready False with the reason Ready

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When the only condition of a `Machine` that is not `True` is
`WirelessJoined` `False` with the reason `Joining`, the phase is
`Ready`, but the `Ready` condition is `False` with the reason `Ready`.
The guide `docs/content/docs/guides/add-nodes.md` says that during the
join the machine stays `Ready`, and `kubectl wait --for=condition=Ready`
does not return until the radio joins.

## The evidence

`readyCondition` in `machine-operator/phase.go` sets `Ready` to
`False` for every condition that is not `True`, except `Ready`,
`RebootApproved`, and the conditions that `reportsOnly` accepts. It
takes the reason from `decidePhase`. `conditionPhase` returns no phase
for the reason `Joining`, so `decidePhase` returns `PhaseReady`, and
the `False` condition gets the reason `Ready`.

`TestARadioStillJoiningLeavesTheMachineReady` in
`machine-operator/phase_test.go` checks only `decidePhase`. No test
calls `readyCondition` with a `Joining` condition.

## What would confirm it

A test that calls `readyCondition` with every condition `True` except
`WirelessJoined` `False` with the reason `Joining`, and reads the
result. On a lab guest with a wired interface and a radio that has not
joined yet, `kubectl get machine -o yaml` shows the `Ready` condition
during the join.

## What a fix has to decide

Whether `Joining` leaves `Ready` `True`, as the guide says, by making
`readyCondition` skip a condition that `conditionPhase` gives no phase,
or whether `Ready` stays `False` while a radio joins, with a reason
other than `Ready` and a change to the guide.
