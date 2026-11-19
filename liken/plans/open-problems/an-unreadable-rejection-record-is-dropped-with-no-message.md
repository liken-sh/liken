# An unreadable rejection record is dropped with no message

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

Each boot copies the stored rejection of a staged document into the
boot record, so the `Machine` status keeps showing why an earlier
document was refused. When the rejection file cannot be read or does
not parse, init discards the error. The status then shows no
rejection, and the console has no line that says why.

## The evidence

`ManifestStore.LoadRejection` (`machine/staging.go:153`) returns an
error for a read failure and for YAML that does not parse strictly.
Four callers in init assign its result with `_`:

- `loadManifestCandidates`, `init/manifests.go:111`, for the `Machine`
  manifest.
- `chooseCluster`, `init/cluster.go:71`, for the `Cluster` document.
- `settleSystemRelease`, `init/proving.go:65`, for the system release.
- `chooseRegistryCredentials`, `init/registries.go:83`, for the
  registry credentials.

The other reads next to each of these, such as `LoadStaged`, print
their errors.

A unit test wrote a credentials `rejection.yaml` with a field that
`Rejection` does not have, read init's console stream from a file, and
called `chooseRegistryCredentials`. It asserted that the boot record
holds a rejection or that the console has a line about the record, and
failed with:

```
with a rejection record that does not decode, the boot record has no rejection and the console has "", want a line about the record
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What would confirm it

The unit test above confirms it for the credentials document.

## What a fix has to decide

When init cannot read the record of an earlier rejection, does it only
print the error on the console, or does it also show it in status, for
example as a rejection whose reason says that the record could not be
read? The four callers should handle it the same way, and the operator
reads the same files, as
[an-unreadable-rejection-record-counts-as-none.md](an-unreadable-rejection-record-counts-as-none.md)
records.
