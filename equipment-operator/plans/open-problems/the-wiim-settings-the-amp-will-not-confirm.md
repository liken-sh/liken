# WiiM settings that the amp does not report back

The equalizer, the output mode, and the subwoofer have documented
setters, but the house's amps refuse the reads that would confirm them.
`EQGetStat` returns `{"status":"Failed"}` on every one, and the output
mode the device reports is `hardware: 7`, which is not the enum the
setter documents. The operator would re-send a declared setting with
no readable value on every reconcile, and the setting would never
match. So the operator does not model those families.
`wiim/AGENTS.md` holds the commands and the sources.

Each family would be added the same way as `device` and
`audio.balance`: a family type, the command builders, a `ConfirmedBy`
line, the CRD block, and a test. The only blocker is the lack of a
model whose reads return values.

Two smaller questions are open:

* The three alarm slots (`setAlarmClock`) are documented and readable,
  but an alarm is a timer and not a slow-moving setting, so it may
  belong on the commands topic instead of the settings topic.
* `reboot` is documented, works on a Mini, and fails on an Amp, so the
  set of commands differs across the product line.
