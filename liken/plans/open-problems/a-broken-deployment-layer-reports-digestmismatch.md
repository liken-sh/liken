# A broken deployment layer reports DigestMismatch

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When machine-operator cannot use the deployment layer of the running
slot, `VersionConverged` reports the reason `DigestMismatch`, the
reason for a release whose bytes do not match the catalog. The cause
is on the machine, and the release server is not at fault.

## The evidence

`fetcher.run` in `machine-operator/fetch.go` sets the state
`fetchRejected` for an error that wraps `errLayer` and for one that
wraps `errCorrupt`. `download.go` wraps `errLayer` when the running
slot's layer has no readable sidecar, when its sidecar is damaged,
when the layer does not match its sidecar, and when the copy in the
inactive slot does not match. `versionCondition` in
`machine-operator/release.go` returns the reason `DigestMismatch` for
every `fetchRejected` state.

The comment on `versionCondition` and the comment on `DigestMismatch`
in `phase.go` say that the machine stays `Blocked` until the catalog
names different bytes. For a broken layer, a new catalog entry does
not help, because the fix is to repair or reinstall the machine, as
the condition message says. `Ensure` returns a `fetchRejected` state
until the request changes, so a repair of the layer also does not
clear the state until the operator restarts or the target changes.

## What would confirm it

A test of `versionCondition` with the snapshot that `fetcher.run`
writes for an `errLayer` error. On a lab guest, a damaged sidecar of
the running slot's layer, then a new target, and a read of the
`VersionConverged` condition.

## What a fix has to decide

The reason for a broken layer, and whether it is `Blocked` like
`DigestMismatch`. Whether a rejected layer is tried again after a
person repairs the machine, and what event starts that.
