# A turn granted again skips the budget and the leader limit

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

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
No test has a second machine that is down at the time of the grant.

## What would confirm it

A table test of `decideRollout` with a pending entry for a machine
that requests a turn again, beside a leader or a worker that is Lost,
which reads `r.grant`.

## What a fix has to decide

Whether the machine that is granted again waits while another leader
is out of service and while the other machines that are down fill the
budget. When it waits, decide whether the entry stays in
`status.rebootTurns` and still counts against the budget, and what
reason its Disruption's `Granted` condition reports.
