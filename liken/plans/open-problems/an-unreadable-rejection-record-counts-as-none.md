# An unreadable rejection record counts as none

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When machine-operator cannot read the record of a document that init
rejected at boot, it treats the document as not rejected. It can then
stage the rejected document again and reboot into it.

## The evidence

`ManifestStore.LoadRejection` in `machine/staging.go` returns an error
when the file cannot be read or does not decode with
`yaml.UnmarshalStrict`. Each caller in machine-operator discards the
error: `reconcile` in `reconcile.go` for the `Machine` spec, and
`cluster.go`, `registries.go`, and `release.go` for their documents.
The rejection is then `nil`. `decideConvergence` in `converge.go`
blocks a spec only when the rejection is not `nil` and its hash equals
the spec's hash, so with `nil` it stages the spec for the next boot.

## What would confirm it

A test that writes a rejection record that does not decode, with the
hash of the declared spec, and runs a pass. The test checks whether
the spec is staged and whether the condition reports
`RejectedLastBoot`.

## What a fix has to decide

What a pass does when the record cannot be read: report the condition
as `Unknown` and stage nothing, or treat the record as present. The
same decision applies to each document that has a rejection record.
