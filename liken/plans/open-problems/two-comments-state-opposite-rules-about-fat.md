# Two comments state opposite rules about FAT

Open bug. A comment in `init/durable.go` says that one directory fsync
makes every rename on a FAT filesystem durable, and the lab showed
that it does not. Code that follows the comment writes to FAT with no
device flush, so a power cut can lose the write.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

The comment on `syncDirectory` in `init/durable.go` (lines 76 to 81)
says a FAT directory fsync flushes the whole block device, so one call
covers every rename. `init/slotloader.go` says the opposite, and the
lab showed that the second one is correct. `installGRUB` in
`init/install.go` follows the first one at its `grub.cfg` write and
calls only `syncDirectory` (`init/install.go:255`). The wrong comment
says one directory fsync is enough, so code that follows it keeps
adding new call sites without a flush.

The install sequence calls `unix.Sync()` after `installGRUB` returns
and before it reports success (`init/install.go:182`), so the installed
`grub.cfg` reaches the disk. Only the local comment and call are wrong.

## The fix

Delete the wrong comment, and make every FAT writer call
`unix.Sync()`. The open problem
[the BIOS arm has no device flush](the-bios-arm-has-no-device-flush.md)
holds the FAT writes of the boot chain that have no flush today.
