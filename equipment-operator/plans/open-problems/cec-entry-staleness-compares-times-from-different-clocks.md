# CEC entry staleness compares times from different clocks

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

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

## What would confirm it

Read `MarkPodsNotReady` in the Kubernetes node lifecycle controller to
confirm which clock stamps the condition. Then a test of `stale` with
a `lastTransitionTime` a few seconds before `reportedAt` and the pod
not Ready.

## What a fix has to decide

- Whether the operator accepts the delay of up to `cecStartupGrace`
  and the comment states it.
- Whether `stale` allows for clock skew, or compares only times from
  one clock.
