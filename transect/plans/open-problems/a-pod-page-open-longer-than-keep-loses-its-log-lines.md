# A pod page open longer than `KEEP` loses its log lines

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

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

## What would confirm it

A test in a paused-time runtime: refresh pod `a`, advance the clock
past `KEEP`, refresh pod `b`, then call `cached` for `a`. It returns
`None`.

## What a fix has to decide

Whether an open page keeps its pod's read alive, for example by a
subscription count, or a push render calls `refresh` when the read is
gone. `KEEP` also limits how long a read counts toward health, so the
two uses of the cache may need separate limits.
