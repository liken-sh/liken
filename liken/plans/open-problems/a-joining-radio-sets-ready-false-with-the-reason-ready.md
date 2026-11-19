# A joining radio sets Ready False with the reason Ready

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

A unit test built the `WirelessJoined` condition for a machine with a
wired port and a radio that is still associating, put it beside
conditions that are all `True`, and called `readyCondition`. It
asserted that `Ready` is `True`, and failed with:

```
with a radio still joining, Ready is False/Ready (WirelessJoined is False), want True
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What would confirm it

The unit test above confirms it. On a lab guest with a wired interface
and a radio that has not joined yet, `kubectl get machine -o yaml`
shows the `Ready` condition during the join.

## What a fix has to decide

While a machine's wifi joins or roams, does `kubectl wait
--for=condition=Ready` return?

- Yes, as the guide says. `readyCondition` then skips a condition that
  `conditionPhase` gives no phase.
- No. `Ready` stays `False` while a radio joins, with a reason other
  than `Ready`, and the guide changes to say so.
