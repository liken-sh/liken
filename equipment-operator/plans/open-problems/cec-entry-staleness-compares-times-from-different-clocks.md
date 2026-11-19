# CEC entry staleness compares times from different clocks

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

`podLiveness.stale` in `cecpods.go` compares the `lastTransitionTime`
of a node workload pod's `Ready` condition with the `reportedAt` of
that machine's entry in a `CECBus`. The comment on `cecPod.readiness`
says that the kubelet stamps `lastTransitionTime` from the node's
clock, which is the clock the node workload uses for `reportedAt`.
When a node stops reporting, the node lifecycle controller in
`kube-controller-manager` marks its pods not Ready, and that write
takes the controller's clock. The grace check also compares
`reportedAt` with `now`, which is the clock of the Deployment's pod.

When the controller's clock runs behind the node's, a pod that
stopped being Ready after the last write can have a
`lastTransitionTime` before `reportedAt`. `stale` then keeps the entry
current until `cecStartupGrace`, 60 seconds, after `reportedAt`. The
delay is at most the grace, because the entry becomes stale when the
grace ends.

## Evidence

- `stale` branches on `state.since.After(written)` and on
  `l.now.Before(ends)`.
- The `readiness` comment states the single-clock assumption.

## The reproduction

A test of the derived `CECBus` conditions gives the machine a pod
that is not Ready since 2 seconds before the entry's `reportedAt`, as
a node lifecycle controller whose clock runs 2 seconds behind marks
it, and derives the conditions 5 seconds after the write. It wants
the `Joined` condition to have the reason `Stale`. It failed with:

```
condition Joined is {Type:Joined Status:True ObservedGeneration:3 Reason:Joined Message:Every adapter has a logical address LastTransitionTime:2026-09-26 12:00:00 +0000 UTC} 5 seconds after the write, want reason Stale, because the pod is not Ready
```

The test is not in the tree, because a failing test cannot land on
`main`.

The pod data in this test is the same as that of a pod that is still
starting, which `TestAnEntryIsCurrentWhileItsMachinesPodIsReadyOrStarting`
in `cecbus_derive_test.go` expects to read as current. So `stale`
cannot tell the two cases apart from the times alone.

## What a fix has to decide

An entry can read as current for up to `cecStartupGrace`, 60
seconds, after its pod stopped, when the clock that marked the pod
not Ready runs behind the node's clock.

- Does the operator accept that delay, and the comment on `stale`
  state it?
- Or does `stale` allow for clock skew? That changes how a pod that
  is still starting is judged.
