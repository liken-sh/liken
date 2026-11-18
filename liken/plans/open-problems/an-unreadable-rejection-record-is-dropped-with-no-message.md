# An unreadable rejection record is dropped with no message

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Each boot copies the stored rejection of a staged document into the
boot record, so the `Machine` status keeps showing why an earlier
document was refused. When the rejection file cannot be read or does
not parse, init discards the error. The status then shows no
rejection, and the console has no line that says why.

## The evidence

`ManifestStore.LoadRejection` (`machine/staging.go:166`) returns an
error for a read failure and for YAML that does not parse strictly.
Four callers in init assign its result with `_`:

- `loadManifestCandidates`, `init/manifests.go:110`, for the `Machine`
  manifest.
- `chooseCluster`, `init/cluster.go:71`, for the `Cluster` document.
- `settleSystemRelease`, `init/proving.go:65`, for the system release.
- `chooseRegistryCredentials`, `init/registries.go:83`, for the
  registry credentials.

The other reads next to each of these, such as `LoadStaged`, print
their errors.

## What would confirm it

A unit test for one caller that writes a rejection file with a field
that does not exist, runs the caller, and checks the boot record and
stderr.

## What a fix has to decide

Whether an unreadable rejection is only printed, or is also reported
in status, for example as a rejection whose reason says that the
record could not be read. The four callers should handle it the same
way.
