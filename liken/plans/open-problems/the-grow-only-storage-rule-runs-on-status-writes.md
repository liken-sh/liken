# The grow-only storage rule runs on status writes

Open bug. A machine booted from a stick manifest whose storage size is
larger than the in-cluster spec cannot write its status. The API
server refuses every status write, and the machine goes Lost with no
way out but a spec edit. A unit test reproduced the refusal on
2026-11-19.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

Nine CEL rules on the root of the `Machine` schema
(`machine/manifests/machines-crd.yaml`, from line 133) compare
`spec.storage.<role>.size` against `status.boot.storage.<role>.size`. A
root rule runs on a status write as well as a spec write. A manifest
that arrived on a stick never met the API server, so it can boot a
machine whose declared size exceeds the in-cluster spec. Every status
write is then refused, and the machine goes Lost with no way out but a
spec edit.

## The reproduction

A test read the `Machine` CRD and ran an update through the CEL
validator of `k8s.io/apiextensions-apiserver`, as the API server does
for a write. The old object declared `podEphemeral` at `10Gi` and
recorded a boot at `10Gi`. The new object kept the spec and recorded a
boot at `20Gi`, as a status write from a machine that booted from a
larger stick manifest does. The test asserted that the write passes,
and it failed with:

```
the status write was refused: [[]: Invalid value: podEphemeral cannot be smaller than the size this machine booted with. A partition can only grow.], want it accepted because the spec did not change
```

The test ran in a scratch module, because the `liken` module does not
depend on `k8s.io/apiextensions-apiserver`. It is not in the tree,
because a failing test cannot land on `main`.

## The fix

This needs the maintainer's decision before a fix is built, because it
changes the validation of the `Machine` CRD.

**Whether the grow-only rules pass a write that leaves the storage
spec unchanged.** With the guard, a status write always passes the
rules, and only an edit of `spec.storage` is checked against the size
that the machine booted with. Without it, the rules stay as they are,
and a machine whose stick manifest declared more storage than the
cluster's spec can report no status until a person raises the spec.

The rules cannot move to `spec.storage`, because a rule there cannot
read `self.status`. The fix keeps the rules at the root and adds a
transition guard that passes any write that leaves `spec.storage`
unchanged, as a status write does.

## What the lab can measure

The drill runs under QEMU, and needs no metal. A `Machine` whose stick
manifest declares a larger `podEphemeral` than the in-cluster spec:
the status write must succeed.
