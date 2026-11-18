# A reference value longer than 200 characters matches nothing

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`relations` in `crates/transect-resources/src/knowledge/references.rs`
passes each field value through `capped(&value, VALUE_CHARS)` before it
looks the value up with `matches.named`. `VALUE_CHARS` is 200. `capped`
in `crates/transect-graph/src/untrusted.rs` cuts a longer value to 199
characters and adds "…", and it also joins runs of white space into one
space. The lookup then compares the changed text with the unchanged
values of the target field, so a long value finds no object.
`written_secret` in the same file builds a `Secret` name from the capped
value in the same way.

A Kubernetes object name can be up to 253 characters, so a reference
to an object whose name has 201 to 253 characters draws no edge.

## What would confirm it

A unit test of `relations` with kind knowledge whose reference matches
on `metadata.name`, and a target object whose name has 220 characters.
The relation has no target.

## What a fix has to decide

Whether the lookup uses the raw value and only the text on the page is
capped, and whether a value that `capped` changes for white space is
matched at all.
