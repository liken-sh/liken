# A reference value longer than 200 characters matches nothing

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and unit tests reproduced it on 2026-11-19.

`relations` in `crates/transect-resources/src/knowledge/references.rs`
passes each field value through `capped(&value, VALUE_CHARS)` before it
looks the value up with `matches.named`. `VALUE_CHARS` is 200. `capped`
in `crates/transect-graph/src/untrusted.rs` cuts a longer value to 199
characters and adds "…", and it also joins runs of white space into one
space. The lookup then compares the changed text with the unchanged
values of the target field, so a long value finds no object.
`written_secret` in the same file builds a `Secret` name from the capped
value in the same way, and so does `referenced_object` for a reference
without `match`. `capped` also drops each character that `shown` does
not allow.

A Kubernetes object name can be up to 253 characters, so a reference
to an object whose name has 201 to 253 characters draws no edge.

## The reproduction

Each test used a name of 220 characters. In the first, a reference with
`match: status.address` named a `Receiver` whose `status.address` is
that name. It failed with:

```
a value of 220 characters refers to [Value], want the Receiver whose status.address equals it
```

In the second, a reference without `match` named a `Receiver` by that
name, and the relation named a `Receiver` whose name is the cut value
that ends in "…", not the object of the whole name. So the branch
without `match`, `referenced_object`, has the same defect, and draws
an edge to an object that does not exist.

The tests are not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should only the text that a page shows be cut at 200 characters, so
that a long name still finds its object, and should a value that
`capped` changes for white space still match?

Whether the lookup uses the raw value and only the text on the page is
capped, and whether a value that `capped` changes for white space is
matched at all.
