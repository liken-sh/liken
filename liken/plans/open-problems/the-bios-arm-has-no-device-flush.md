# The BIOS arm has no device flush

Open bug. On a BIOS machine, a power cut after init arms a trial can
lose the arm write, and the next boot then reports a release that ran
and fell back when the release never ran. A power cut after the
`grub.cfg` heal can leave a boot home with a `.partial` file and no
`grub.cfg`, and GRUB then stops at a rescue prompt.

The claim was checked against the code on 2026-11-19: no `unix.Sync()`
follows the writes of `armTrial` and `assertProven`
(`init/grubactuator.go:47` and `69`) or the write of `grub.cfg`
(`init/grubactuator.go:163`). No unit test can show a write that a
power cut loses. The QEMU drill in "What the lab can measure" can.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17. The fix rests on milestone 94's rule: when
the code cannot confirm a state, it must refuse or report, and never
act. So a trial whose arm write may not have reached the disk does not
count as armed.

## The evidence

`armTrial` and `assertProven` in `init/grubactuator.go` write
`try_slot` and `default_slot` through `updateGRUBEnv`
(`init/grubenv.go:103`) and `writeFileDurably` (`init/durable.go`),
which fsyncs the temp file and renames it. Nothing flushes the block
device after the rename. On FAT, the rename's directory entry is in
buffers attached to the block device, and an fsync of the directory
does not reach them. `init/slotloader.go` explains this at length, and
the lab found it on the first promotion drill of the built part of
plan 33. The GRUB arm takes the same path with no flush. A power cut
after the arm can lose the arm write while the attempted marker
remains, and the next boot reads that as a release that ran and fell
back. The `grub.cfg` heal in the same file (`repairBootChain`, which
writes `grub.cfg` through `writeFileDurably` at
`init/grubactuator.go:163`) has the same gap, and a boot home with a
`.partial` file and no `grub.cfg` drops GRUB to a rescue prompt.

The reboot path does flush. `rebootMachine` (`init/reboot.go`) arms
the trial, shuts the plane down, remounts each disk read-only, and
calls `unix.Sync()` (`init/reboot.go:156`) before the reboot syscall.
The gaps are the promotion's write of `default_slot`, which stays
unflushed until the kernel writes it back, and the `grub.cfg` heal.

**The readback after that write reads the page cache.** `assertProven`
re-reads what it just wrote, and its comment says that it checks the
value read back, as `efiActuator` does with `BootOrder`. The UEFI
readback goes through efivarfs to the firmware. This one comes back
from the page cache. The readback is useful only after the flush
below.

## The fix

The fix is `unix.Sync()` after each of those writes, which is what the
installer, the loader writer, and the report already do
(`init/install.go:157`, `init/slotloader.go:99`, `init/report.go:389`).

## What the lab can measure

The drill runs under QEMU, and needs no metal. A BIOS guest with a
release staged: cut the power between the arm and the reboot, at the
point `make -C dev-cluster` can stop a guest. The next boot must find
no attempted marker for a trial that never ran, and must not report a
false rejection.
