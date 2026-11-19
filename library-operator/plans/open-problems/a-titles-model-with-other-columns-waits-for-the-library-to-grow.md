# A titles model with other columns waits for the library to grow

Open bug. A unit test reproduced it on 2026-11-19.

The titles model of a `Library` is fitted on the columns that the
`titles` tool of that release works out from a reading, and the model
file names them. A later release of the tool can work out other
columns. `titles place` then refuses the model, the placing `Job`
records `failed` with the tool's words, and the Library's placements
stay as the last good run wrote them.

Nothing starts a fit for that. The operator starts a fit only when the
count of videos with a reading and a provider's mark has grown by half
since the last fit (`titlesFitDue` in `titlesfit.go`). So a library
that does not grow keeps a model that the tool refuses, and its
placements go stale, until a person removes the model from the volume
or the status.

[Plan 96](../completed/96-end-titles-from-the-picture.md#each-cluster-fits-its-own-model)
decided that a change of the columns starts a fit. It is not built.
The operator does not read the model file, so it has no copy of the
model's columns to compare with the tool's. One way is for the fit
`Job` to report the columns' identity in its result, and for the
placing `Job` to report that `place` refused the model for its columns,
as a reason of its own that the trigger of the fit reads.

## The reproduction

A test gave a `Library` with the titles fact on a status whose model
was fitted on 120 marked videos, and whose last placing run `failed`
with the reason that `titles place` gives for a model of other
columns. The Library's report counted the same 120 videos. The test
ran the pass that starts a fit and wanted one fit `Job`. It failed
with:

```
after a placing run that refused the model for its columns, jobs = [], want one fit because plan 96 says a change of the columns starts a fit
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

How should the operator learn that the titles tool refuses the
Library's model for its columns: from a result reason of its own that
the placing `Job` reports, or from an identity of the columns that the
fit `Job` reports and the operator compares with the tool's?
