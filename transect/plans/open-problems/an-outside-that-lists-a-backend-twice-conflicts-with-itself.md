# An `Outside` that lists a backend twice conflicts with itself

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`Outsides::new` in `crates/transect-ground/src/outside/mod.rs` indexes
each address and driver of each `Outside` by backend. When it finds a
backend already in the index, it records a conflict condition on the
first `Outside` and on the current one. It does not check that the two
are the same `Outside`. `parse` in `crates/transect-ground/src/outside/parse.rs`
does not remove duplicates from `spec.addresses` or `spec.drivers`.

So an `Outside` that lists one address twice, or the same address in
two cases (the index lowercases addresses), gets two conflict
conditions that name it as both sides: "nas.example is listed by
storage and storage, and storage draws it because its name sorts
first".

## What would confirm it

A unit test of `Outsides::new` with one `Outside` whose `addresses` are
`[nas.example, NAS.example]`. The result has conflict conditions.

## What a fix has to decide

Whether a duplicate inside one `Outside` is silently accepted, or is
reported with its own condition that says the entry is listed twice.
