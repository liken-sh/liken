# An `Outside` that lists a backend twice conflicts with itself

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

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

## The reproduction

A test built `Outsides` from one `Outside` named `storage` whose
`addresses` are `nas.example` and `NAS.example`. It wanted no
condition, and failed with:

```
the conditions are ["nas.example is listed by storage and storage, and storage draws it because its name sorts first", "nas.example is listed by storage and storage, and storage draws it because its name sorts first"], want none because one Outside cannot conflict with itself
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should an `Outside` that lists one address twice be accepted with no
condition, or get a condition of its own that says the entry is
listed twice?

Whether a duplicate inside one `Outside` is silently accepted, or is
reported with its own condition that says the entry is listed twice.
