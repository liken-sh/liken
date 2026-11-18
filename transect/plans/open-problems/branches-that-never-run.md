# Branches that never run

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it. Each
item below is code that no input reaches, so a test cannot cover it and
a reader takes it for behavior.

- **The `via` guard of the ways page.** `WaysPage::build` in
  `crates/transect-access/src/access/ways/page.rs` replaces `via` with
  an empty list for a `HostPort` or `MachineNetwork` row. `addresses.rs`
  in the same directory already builds every row of those kinds, the
  `pod_ways` rows, with `via: Vec::new()`. The test
  `names_the_workload_that_listens_only_where_it_reaches` builds every
  case with an empty `via`, so it passes with or without the guard and
  does not test what its name says.
- **The exit of the image label task.** `watch` in
  `crates/transect-supply/src/images/task.rs` returns when
  `changes.changed()` returns an error. `changes` is subscribed to
  `changed`, and the task holds the `changed` sender for its whole life
  (it calls `changed.send_replace`), so the receiver cannot return an
  error while the task runs.

## What would confirm it

For the guard, the same test with a non-empty `via` in the `HostPort`
and `MachineNetwork` cases, run with the guard removed: it still passes
only if no row of those kinds has a `via`. For the task, reading the
code is enough.

## What a fix has to decide

Whether the guard stays as a statement of the rule, with a test case
that gives it a non-empty `via`, or is deleted. Whether the task's exit
is deleted or replaced by a real shutdown signal.
