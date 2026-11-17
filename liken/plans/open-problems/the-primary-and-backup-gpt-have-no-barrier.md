# The primary and backup GPT have no barrier between them

Open bug. A power cut while `liken` writes a partition table can leave
a disk with no readable GPT, because both copies of the table can be
torn at the same moment.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

`WriteTableInPlace` (`disks/gpt.go:463`) writes all five chunks of both
tables and calls `Sync` once at the end. A power cut or a drive that
reorders writes can leave the primary entries half-written while the
backup is already overwritten, and then `ReadGPT` reports neither copy
readable. The single `Sync` is in `writeTableBytes`
(`disks/gpt.go:509`), so `WriteTable` has the same gap.

## The fix

The first fix proposed was one `Sync` after the primary table and one
after the backup, so that at every moment one complete table exists.

That order does not protect a relocation. When the disk grew, the new
backup goes to the new end of the disk, where `ReadGPT` looks for it,
so a torn primary write after it leaves no readable copy. The safe
order is the backup first, a `Sync`, then the primary and a `Sync`.
