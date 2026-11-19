# A turn granted again skips the budget and the leader limit

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

cluster-operator can give a machine its reboot turn a second time
while another machine is down. Then two machines are out of service
when the budget allows one, and two leaders can be out of service at
once. The header of `cluster-operator/rollout.go` states that at most
one leader is out of service or has a turn, and that the budget
counts every machine that is not available.

## The evidence

In `decideRollout` in `cluster-operator/rollout.go`, a machine goes
into `regranting` when its `status.rebootTurns` entry outlived its
grant because one of the entry's Disruptions is still open, the
machine is available, and it requests a turn again. The loop over
`regranting` checks only `noTurnBecause`, which refuses for a stalled
rollout or for heartbeats that are too new. It does not read
`leaderBusy`, and it does not call `budgetUse.refusal`. The comment
above the loop gives the reason: "A machine that is granted again
already counts against the budget".

The entry counts against the budget, but the machine is available
while it waits. Another machine can go down in that time:

- Leader `node-1` has a pending entry and is granted again. Leader
  `node-2` is Lost with no turn. The first loop sets `leaderBusy` for
  both, but the grant to `node-1` is written, so both leaders are out
  of service when `node-1` reboots.
- With a budget of one, worker `node-4` has the entry and worker
  `node-3` goes down. The budget count is two of one, and `node-4`
  reboots anyway.

`TestAMachineWhoseEntryOutlivedItsGrantIsGrantedAgain` and
`TestAStalledRolloutGrantsNoEntryAgain` in
`cluster-operator/requests_test.go` cover the grant and the stall.
No test in the tree has a second machine that is down at the time of
the grant.

A unit test reproduced both cases. Each case gave `decideRollout` a
machine with an entry in `status.rebootTurns` at an older
resourceVersion, an open Disruption, and a request for a turn again,
beside a machine in the phase Lost. The first case is leader `node-1`
beside leader `node-2`, with a budget of two. The second is worker
`node-4` beside worker `node-3`, with a budget of one. The test
asserted that `decideRollout` grants no turn. It failed with:

```
grants = [node-1] while node-2 is Lost, want none
grants = [node-4] while node-3 is Lost, want none
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When a machine is due its reboot turn a second time and another
machine is down, should it wait, as a first grant waits for the
leader limit and the budget?

- **Wait.** Then decide whether the machine keeps its entry in
  `status.rebootTurns` while it waits, so the entry still counts
  against the budget, and what reason its Disruption's `Granted`
  condition reports.
- **Go ahead.** The rollout then states, in the header of
  `cluster-operator/rollout.go`, that a machine granted again can
  break the leader limit and the budget.
