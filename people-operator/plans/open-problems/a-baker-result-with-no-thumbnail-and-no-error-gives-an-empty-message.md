# A baker result with no thumbnail and no error gives an empty message

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`(*bakers).result` in `people-operator/bakers.go` decodes the JSON line
that a finished baker pod wrote to its log into a `bakeResult`. It
treats the line as a failure when `Error` is set or `Thumbnail` is
empty:

```go
if line.Error != "" || line.Thumbnail == "" {
    return reading{}, &sourceError{reason: bakeReason(line.Reason), err: errors.New(line.Error)}
}
```

When the line decodes with neither field set, for example `{}` or a
line from a baker of another version, `line.Error` is empty and the
error has an empty message. `composeStatus` in `status.go` appends the
error's text to the `AvatarReady` message, so the message ends in
"status.thumbnail shows the initials: " or "status.thumbnail keeps the
last good picture: " with nothing after it. The reason is `BakeFailed`,
because `bakeReason` turns an empty reason into `BakeFailed`. A person
who reads the condition learns that the bake failed and not why.

`bakeFile` in `bake.go` sets `Error` or `Thumbnail` on every path, so
the current baker does not write such a line. The case needs a log
that the current `bake` command did not write.

## What would confirm it

A test in `bakers_test.go` whose fake API server returns `{}` as the
baker pod's log, followed by a read of the `Person`'s `AvatarReady`
message.

## What a fix has to decide

The text that `result` gives when the line holds no error, such as
"the baker pod %s/%s wrote no thumbnail and no error", and whether the
operator treats a line it cannot use as a bug in the operator, as
`runBake`'s exit status 2 does for bad arguments.
