# CI never fetches the flux seed

Open bug. The two tests that exist to catch a Flux bump that breaks
cluster-operator's seed table skip on every CI run, so CI passes a
bump that breaks the seed.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

`TestTheRealSeedParsesAndMapsWhole` and its companion
`TestTheTeardownAndItsRightsCoverTheSeed` in
`cluster-operator/flux_test.go` call `t.Skip` (lines 213 and 369) when
`seed/gotk-components.yaml` is absent. That file is a build product
that `cluster-operator/Makefile` copies from `flux/dist`, and the `go`
job of the check stage of `.github/workflows/component-liken.yaml`
runs `make test-go` with no step that fetches or copies it. The two
tests skip on every CI run. They pass on a workstation because the
seed happens to be there.

## The fix

The `go` job fetches the flux domain before the tests, and the tests
fail instead of skipping when the seed is absent under CI.

## What the lab can measure

CI: the checks run must show the two seed tests as run, not skipped.
