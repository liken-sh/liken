# A failed step prints its error twice

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19 for `carryOutConvergence`.

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

A unit test staged a spec, made the store's directory read-only, and
called `carryOutConvergence` with a decision to withdraw the staged
spec, while it read the operator's standard output. It asserted that a
step that records its failure in the outcome prints nothing, and
failed with:

```
the step recorded its failure and also printed "withdrawing the staged spec: remove /tmp/TestAStepThatRecordsItsFailurePrintsNothing1817336013/001/manifests/staged.yaml: permission denied\n", want the loop's line as the only record
```

The test is not in the tree, because a failing test cannot land on
`main`. The other steps in the list print and record the same way, at
`release.go:242` and `:260`, `cluster.go:259` and `:280`,
`loop.go:154`, and `cdi.go:202`.

The comment on `describeUnfinished` in `loop.go` says that each step
already logs its own failure, and the loop's line names only the first
three failures and counts the rest.

## What would confirm it

The unit test above confirms it for one step.

## What a fix has to decide

Is the loop's "the pass did not finish" line the one record of a
failed step?

- Yes. The steps stop printing. The loop's line then is the only
  record of the failures past the first three that it names.
- No. A step keeps a log line when the line holds detail that the
  outcome does not record.
