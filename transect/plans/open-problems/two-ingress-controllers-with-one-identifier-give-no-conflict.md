# Two `IngressController`s with one identifier give no conflict

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

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

## The reproduction

A test gave one `IngressClass` the controller `example.io/proxy`, and
two `IngressController`s that declare it, one for the image of the
workload `router` and one for the image of `mail`. It built `Serving`
with the two in one order and then in the other, and wanted one answer
for the class. It failed, because the answer follows the order of the
resources:

```
the class is served by Some(Declared(Object { group: "apps", kind: "Deployment", namespace: Some("edge"), name: "mail" })) when the proxy IngressController comes first and by Some(Declared(Object { group: "apps", kind: "Deployment", namespace: Some("edge"), name: "router" })) when it comes second, want one answer because the order of the resources means nothing
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When two `IngressController`s declare one identifier, should one win
by its name, or should both count, as when one controller runs under
two images, and on which resource does the `Conflict` condition go?

Which one wins, by name as for `Outside` and `Intent`, or both, as
when one controller runs under two images. Then where the `Conflict`
condition is recorded.
