# `BootNext` for the proven slot reads as a pending trial

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

`status.firmware.bootNext` marks a `BootNext` that points at the
proven slot with the note "pinned at the proven slot". The note is
missing when `BootOrder` does not start with the proven slot's entry.
That is the firmware for which `assertBootNext` writes `BootNext`, so
on that firmware the status can show the proven slot's `BootNext` as
if a trial were waiting.

## The evidence

`firmwareFacts` (`init/efi.go:213`) adds the note only when `BootNext`
equals `BootOrder[0]` (`init/efi.go:230`). Its comment says that any
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

The unit test gave a fake efivarfs with an entry for slot A
(`Boot0002`) and for slot B (`Boot0003`), a `BootOrder` of slot B and
then slot A, and a `BootNext` of slot A. It wrote a proven release
record for slot A, called `firmwareFacts`, and asserted that `BootNext`
has the note. It failed with:

```
BootNext is "Boot0002 (liken slot A)", want "Boot0002 (liken slot A, pinned at the proven slot)", because slot A is the proven slot
```

`firmwareFacts` does not read the store, so the proven record in the
test had no effect. The test is not in the tree, because a failing
test cannot land on `main`. On a machine, a firmware that refuses
`BootOrder` writes shows the same.

## What a fix has to decide

How should the firmware status learn which slot is the proven one?
The proven slot's entry is in the store, not in the firmware
variables.

- **Read the proven record** from the store, and compare `BootNext`
  with that slot's entry.
- **Pass the entry number** that `repairBootEntries` found to
  `firmwareFacts`.
