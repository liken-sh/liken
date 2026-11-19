# The storage role test checks five of nine roles

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`TestRoleAddressesEveryRoleAndNothingElse` (`machine/status_test.go:8`)
says that each role name reaches its own field of `StorageStatus`, but
it checks the fields of only five roles. A mistake in
`StorageStatus.Role` for `biosBoot`, `bootHome`, `systemA`, or
`systemB`, such as two names that return the same field, passes the
test.

## The evidence

The test calls `Role` for each name in `StorageRoleNames` and sets
`Backing` to `BackingPartition` through the pointer that `Role`
returns. It then checks `MachineState`, `MachineEphemeral`,
`ClusterState`, `PodStorage`, and `PodEphemeral`. `Role`
(`machine/status.go:471`) also returns `BIOSBoot`, `BootHome`,
`SystemA`, and `SystemB`, and the test does not read those fields.

## What would confirm it

Change `Role` so that `SystemBRole` returns `&s.SystemA`, and run the
test. If it passes, the gap is confirmed.

## What a fix has to decide

Whether the test checks every field by name, or checks that the
pointers that `Role` returns for the names in `StorageRoleNames` are
all different and all inside the struct, so that a new role is
covered without an edit to the test.
