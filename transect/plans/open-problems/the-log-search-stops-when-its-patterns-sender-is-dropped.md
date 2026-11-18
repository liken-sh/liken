# The log search stops when its patterns sender is dropped

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`watch` in `crates/transect-observe/src/logs/search.rs` runs a round of
log searches, then waits on its `EVERY` timer or on
`resources.changed()`. When `resources.changed()` returns an error, the
task returns, and no later round runs, not even on the timer.

The receiver returns an error when its sender is dropped. `Field::fixed`
in `crates/transect-resources/src/resources/field.rs` drops the sender
at once, and its doc says that `transect` uses it when it runs with no
resources directory. So in that mode the search runs one round and then
stops for the life of the process.

`poll` in `crates/transect-observe/src/alerts/poll.rs` handles the same
case differently: its `select!` matches only `Ok(())` from
`resources.changed()`, so the tick keeps the loop running.

## What would confirm it

A test in a `synctest`-style paused-time runtime that starts `watch`
with `Field::fixed` patterns and a fake store, advances the clock past
`EVERY` twice, and counts the rounds. One round runs.

## What a fix has to decide

Whether a closed resources channel means "the patterns never change"
and the loop keeps its timer, as in `alerts/poll.rs`, or means
shutdown. If it can mean either, the two cases need different signals.
