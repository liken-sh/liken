# A pod page open longer than `KEEP` loses its log lines

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

`Tails` in `crates/transect-observe/src/logs/mod.rs` caches the log
tails of each pod that a page reads. `start` reads a pod's tails, and
before it inserts them it removes every cached read older than `KEEP`
(600 s). A page load calls `refresh`, which reads again only when the
cached read is older than `FRESH`. A render that a change pushes to an
open page calls `cached` through `crates/transect/src/web/render.rs`,
and `cached` does not read.

So when a pod page stays open for more than 600 s with no reload, and
another pod's page then reads, that read removes the first pod's
tails. The next push to the first page renders it with no lines.

## The reproduction

The cache records each read with a `std::time::Instant`, which a
paused Tokio clock does not move, so the test set the time of the
read of pod `a` back past `KEEP` by hand after the read. Then it read
pod `b`, and wanted the cache to still hold the lines of `a`. It
failed with:

```
the cache has no lines for pod a after a read of pod b, want the lines the open page of a shows
```

The test used the existing fixture of the log tests, whose log source
and API server listen on a local socket and never answer.

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should an open pod page keep its pod's read in the cache, or should a
render that a change pushes read the pod's logs again when the read is
gone?

Whether an open page keeps its pod's read alive, for example by a
subscription count, or a push render calls `refresh` when the read is
gone. `KEEP` also limits how long a read counts toward health, so the
two uses of the cache may need separate limits.
