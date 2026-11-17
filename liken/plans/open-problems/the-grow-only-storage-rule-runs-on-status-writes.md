# The grow-only storage rule runs on status writes

Open bug. A machine booted from a stick manifest whose storage size is
larger than the in-cluster spec cannot write its status. The API
server refuses every status write, and the machine goes Lost with no
way out but a spec edit.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

Nine CEL rules on the root of the `Machine` schema
(`machine/manifests/machines-crd.yaml`, from line 157) compare
`spec.storage.<role>.size` against `status.boot.storage.<role>.size`. A
root rule runs on a status write as well as a spec write. A manifest
that arrived on a stick never met the API server, so it can boot a
machine whose declared size exceeds the in-cluster spec. Every status
write is then refused, and the machine goes Lost with no way out but a
spec edit.

## The fix

The rules cannot move to `spec.storage`, because a rule there cannot
read `self.status`. The fix keeps the rules at the root and adds a
transition guard that passes any write that leaves `spec.storage`
unchanged, as a status write does.

## What the lab can measure

The drill runs under QEMU, and needs no metal. A `Machine` whose stick
manifest declares a larger `podEphemeral` than the in-cluster spec:
the status write must succeed.
