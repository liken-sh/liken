# A failed step prints its error twice

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Some steps of a machine-operator pass print their error to the log and
also record it in the pass outcome. The loop then prints the recorded
failures again, so the log has each error twice. The rule in
`brand/messages.md` says to return an error or log it, not both.

## The evidence

Each of these steps prints the error and then calls `out.fail` or
`out.failSoon` with the same error:

- `settleSystemReleaseLifecycle` in `machine-operator/release.go`,
  when it promotes the system release and when it records the running
  release as proven.
- `settleClusterLifecycle` in `machine-operator/cluster.go`, when it
  promotes the cluster document and when it records the seed document
  as proven.
- `carryOutConvergence` in `machine-operator/staging.go`, when it
  withdraws a staged document and when it clears a rejection record.
- `run` in `machine-operator/loop.go`, when the facts watch does not
  sync.
- `refreshCDISpecs` in `machine-operator/cdi.go`, when one claim's CDI
  specification does not refresh.

After the pass, `run` in `loop.go` prints "the pass did not finish
..." with `describeUnfinished`, which gives the step and the error of
each recorded failure.

## What would confirm it

A test, or the log of a lab guest, in which one of these steps fails,
for example a promotion on a read-only `machineState`. The log shows
the error once from the step and once from the loop.

## What a fix has to decide

Whether the loop's line is the one record of a failed step, so that
the steps stop printing, or whether a step keeps a log line that
holds detail that the outcome does not record.
