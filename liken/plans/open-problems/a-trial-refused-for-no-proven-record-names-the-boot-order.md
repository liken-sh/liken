# A trial refused for no proven record names the boot order

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

When the machine has no proven release record, or the record does not
parse, the reboot path does not arm a staged trial, and the console
line says that the default boot order does not read back with the
proven slot first. The boot order was not checked, so the line names
the wrong cause.

## The evidence

`armProvingBoot` (`init/proving.go:132`) prints that line when
`fallbackInPlace` returns false (`init/proving.go:173`).
`fallbackInPlace` (`init/proving.go:216`) returns false at once when
`provenRecord` returns nil, before it calls `assertProven` or
`fallbackLeads`. `provenRecord` (`init/proving.go:347`) returns nil
with no line for a missing record, and with a line for a record that
cannot be read or parsed.

The unit test wrote a staged release record for slot B and put that
release on the slot, wrote no proven record, and gave a fake efivarfs
with a boot entry for each slot. It called `armProvingBoot` for a boot
from slot A and asserted that the console line does not name the boot
order. It failed with:

```
the console holds "liken: system: the default boot order does not read back with the proven slot first, so the reboot does not arm the trial\n", want a reason that names the missing proven release record
```

The test is not in the tree, because a failing test cannot land on
`main`. The refusal itself is tested and passes: with no proven
record, the reboot writes no attempted marker and no `BootNext`.

The case is rare, because machine-operator writes the running release
as the first proven record on a machine that has none
(`machine-operator/release.go:252`).

## What a fix has to decide

When a machine has no record of a release that booted and was
promoted, should the reboot refuse the upgrade trial, or try it?

- **Refuse, and give the reason.** This is what the reboot does now.
  `armProvingBoot` checks for the record before `fallbackInPlace` and
  prints that the proven release record is missing.
- **Try the upgrade.** `fallbackInPlace` then needs a slot to put
  first when no record names one, such as the slot that runs now.
