# A firmware that drops BootOrder leaves a trial without a fallback

Open problem. The proving lifecycle arms a release trial with
`BootNext` and relies on `BootOrder` as the fallback when the trial
dies. A firmware exists that accepts a `BootOrder` write, reads it
back correctly, and restores its old order across a reset. On that
firmware the fallback is not real, and a trial that panics before
`init` runs can loop without any liken code ever executing to stop
it.

## The evidence

The lab's test machine showed the behavior on 2026-08-26. `init`
asserted `BootOrder` with slot B first on the way down, the write
read back correctly, and the machine came up on slot A. The next
boot's assert printed `BootOrder now leads with Boot0003`, a line
that only prints on a change, which proves the firmware had not held
the previous write. `BootNext` survived every reset on the same
machine that day.

The shutdown path now pins `BootNext` at the proven slot, so an
ordinary reboot boots the proven slot even on this firmware. That
covers the standing preference and not the trial.

## The gap

`armProvingBoot` verifies the fallback before arming: it asserts the
proven slot and reads `BootOrder` back (`fallbackInPlace`). On this
firmware the readback passes, because the firmware lies only across
a reset. The trial then consumes `BootNext`, so the one variable this
firmware honors is spent on the trial itself. If the trial's kernel
panics, `panic=10` resets the machine, and the firmware boots
whatever order it restored. When that restored order leads with the
trial slot, the loop is panic, reset, trial, panic. The proving
watchdog cannot help, because it runs inside `init`, and `init` never
runs.

## The proposed detection

The firmware cannot be trusted to report this defect, but it can be
caught lying. Record the `BootOrder` that `assertBootOrder` last
wrote under `machineState`. At the next boot, compare the firmware's
actual order against the record. A mismatch proves this firmware
does not hold `BootOrder` across a reset. On such a machine,
`armProvingBoot` refuses to arm a trial and reports why, so a release
upgrade waits for a person instead of gambling a machine that has no
fallback. One honest reboot after the fix lands is enough to classify
the firmware, and a firmware that starts holding its writes clears
the verdict the same way.
