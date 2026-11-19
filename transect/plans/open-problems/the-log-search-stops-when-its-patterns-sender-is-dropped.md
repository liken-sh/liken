# The log search stops when its patterns sender is dropped

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

`watch` in `crates/transect-observe/src/logs/search.rs` runs a round of
log searches, then waits on its `EVERY` timer or on
`resources.changed()`. When `resources.changed()` returns an error, the
task returns, and no later round runs, not even on the timer.

The receiver returns an error when its sender is dropped. With no
resources directory, `main` in `crates/transect/src/main.rs` reads the
resources from a `watch::channel` whose sender it drops at once, and
`Field::fixed` in `crates/transect-resources/src/resources/field.rs`
drops its sender the same way. So in that mode the search runs one
round and then stops for the life of the process.

`poll` in `crates/transect-observe/src/alerts/poll.rs` handles the same
case differently: its `select!` matches only `Ok(())` from
`resources.changed()`, so the tick keeps the loop running.

## The reproduction

A test started `watch` in a paused Tokio runtime with no log source and
patterns from `Field::fixed`, and counted the rounds over six times
`EVERY`. It wanted three rounds, and failed with:

```
1 rounds of search ran in 1800s, want 3 because the timer starts a round every 300s
```

The same test passed with the `select!` of `alerts/poll.rs`, which
matches only `Ok(())`.

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

With no resources directory, should the log search keep running on
its timer, or stop?

Whether a closed resources channel means "the patterns never change"
and the loop keeps its timer, as in `alerts/poll.rs`, or means
shutdown. If it can mean either, the two cases need different signals.
