# A titles model with other columns waits for the library to grow

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
