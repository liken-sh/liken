# Two `IngressController`s with one identifier give no conflict

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`Serving::build` in `crates/transect-network/src/entry/serving.rs`
collects the declared controllers into a `HashMap` keyed by
`controller.controller`, the identifier that an `IngressClass` names.
When two `IngressController` resources declare the same identifier, the
later one in the input replaces the earlier one, and nothing reports
it. The workloads of the earlier one are no longer found as the
controller of that class.

Other resources that two documents can set twice report it: `Outside`
in `crates/transect-ground/src/outside/mod.rs` and `Intent` in
`crates/transect-resources/src/intent/object_intent.rs` each record a
`Conflict` condition on both documents.

## What would confirm it

A unit test of `Serving::build` with two `IngressController`s that
declare one identifier and different images. `of(Some(identifier))`
returns the workloads of only one of them, and no condition is
recorded.

## What a fix has to decide

Which one wins, by name as for `Outside` and `Intent`, or both, as
when one controller runs under two images. Then where the `Conflict`
condition is recorded.
