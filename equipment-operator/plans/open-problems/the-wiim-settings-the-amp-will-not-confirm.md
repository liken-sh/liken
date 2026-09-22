# The WiiM settings the amp will not confirm

The equalizer, the output mode, and the subwoofer have documented
setters, but the house's amps refuse the reads that would confirm them.
`EQGetStat` answers `{"status":"Failed"}` on every one, and the output
mode the device reports is `hardware: 7`, which is not the enum the
setter documents. A declared setting with no readable value would be
re-sent on every reconcile and never settle, so those families are not
modeled. `wiim/AGENTS.md` holds the commands and the sources.

Each family lands the way `device` and `audio.balance` did: a family
type, the command builders, a `ConfirmedBy` line, the CRD block, and a
test. The blocker is a model whose reads answer, not the code.

Two smaller questions sit beside it:

* The three alarm slots (`setAlarmClock`) are documented and readable,
  but an alarm is a timer and not a slow-moving setting, so it may
  belong on the commands topic instead of the settings topic.
* `reboot` is documented, works on a Mini, and fails on an Amp, so the
  command set is not fixed across the line.
