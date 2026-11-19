# An unreadable rejection record counts as none

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

A unit test ran `convergeRegistryCredentials` against a fake API server
whose credentials `Secret` differs from what the machine booted with,
to learn the hash of the declared document. Then it wrote a
`rejection.yaml` with that hash and a field that `Rejection` does not
have, so `yaml.UnmarshalStrict` refuses it, and ran the same pass
again. It asserted that the pass stages nothing, and failed with:

```
with a rejection record that does not decode, the pass stages the document (False/RestartRequested), want nothing staged
```

With the same record and no extra field, the pass staged nothing.
The test is not in the tree, because a failing test cannot land on
`main`.

## What would confirm it

The unit test above confirms it for the credentials document. The
`Machine` spec, the `Cluster` document, and the system release discard
the error in the same way.

## What a fix has to decide

When the operator cannot read the record of an earlier rejection,
what does the pass do?

- Report the condition as `Unknown`, and stage nothing until the
  record reads.
- Treat the record as present, and report `RejectedLastBoot`, so the
  document is not tried again until a person changes it.

The same decision applies to each document that has a rejection
record, and to init's reads of the same files in
[an-unreadable-rejection-record-is-dropped-with-no-message.md](an-unreadable-rejection-record-is-dropped-with-no-message.md).
