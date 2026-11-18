# `BootNext` for the proven slot reads as a pending trial

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`status.firmware.bootNext` marks a `BootNext` that points at the
proven slot with the note "pinned at the proven slot". The note is
missing when `BootOrder` does not start with the proven slot's entry.
That is the firmware for which `assertBootNext` writes `BootNext`, so
on that firmware the status can show the proven slot's `BootNext` as
if a trial were waiting.

## The evidence

`firmwareFacts` (`init/efi.go:211`) adds the note only when `BootNext`
equals `BootOrder[0]` (`init/efi.go:228`). Its comment says that any
other value is a trial boot that has not happened yet.

`repairBootEntries` (`init/bootentries.go:181`) writes `BootOrder`
through `assertBootOrder` and then writes the proven slot's entry to
`BootNext` through `assertBootNext` (`init/bootentries.go:205`). When
the `BootOrder` write fails, or the firmware does not keep it,
`assertBootOrder` prints a line and returns, and `assertBootNext`
still runs. `BootOrder[0]` is then another entry, and `BootNext` is the
proven slot's entry with no note. The comment on `assertBootNext`
(`init/bootentries.go:265`) names firmware that returns to its old
order at reset and still honors `BootNext`.

## What would confirm it

A unit test with `fakeFirmwareVars` in which `BootOrder` starts with
an entry other than the proven slot's, and `BootNext` is the proven
slot's entry. Then read `firmwareFacts`. On a machine, a firmware that
refuses `BootOrder` writes shows the same.

## What a fix has to decide

What `firmwareFacts` compares `BootNext` with. The proven slot's entry
is in the store, not in the firmware variables, so the fix needs the
proven record or the entry number that `repairBootEntries` found.
