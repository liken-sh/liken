# A broken deployment layer reports DigestMismatch

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

A unit test served a release from a fake release server, gave the
fetcher a running slot with no deployment layer, waited for the fetch
to settle, and passed its snapshot to `versionCondition`. It asserted
that the reason is not `DigestMismatch`, and failed with:

```
with no layer in the running slot, VersionConverged is False/DigestMismatch (Release 0.2.0 cannot be downloaded to slot B: the running slot's deployment layer has no readable sidecar (open /tmp/TestABrokenLayerIsNotReportedAsADigestMismatch2389742403/002/deployment.cpio.sha256: no such file or directory), so repair or reinstall this machine: this machine's deployment layer is unusable), want a reason other than DigestMismatch
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What would confirm it

The unit test above confirms the reason. On a lab guest, a damaged
sidecar of the running slot's layer, then a new target, and a read of
the `VersionConverged` condition, would confirm it on a machine.

## What a fix has to decide

When a machine's own deployment layer is damaged, which reason does
the machine show in place of the one that says the catalog is wrong?
And is that machine `Blocked`, as it is for `DigestMismatch`?

After a person repairs the machine, does the operator try the layer
again by itself, or only after the operator restarts or the target
changes, as it does now? If it tries again by itself, the fix must
name the event that starts the new try.
