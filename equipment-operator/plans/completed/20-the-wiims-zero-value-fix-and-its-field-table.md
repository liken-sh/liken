# 20, The WiiM's zero-value fix and its field table

Proposed on 2026-11-10, from the phases of
[plan 16](16-the-wiims-settings.md) that were not built. Plan 14
([14-one-settings-model-for-every-receiver.md](../14-one-settings-model-for-every-receiver.md))
holds the common settings model, the tiers, and `settingExceptions`
that this plan uses.

Status on 2026-11-17: closed. Phase 1, the zero-value fix, is built
(b247ebd7). The client records each read command whose last answer
held no value (`unread`, `wiim/client.go`), and `Client.Settings()`
reports no value for the balance, the status light, or the touch
buttons when its read is one of them. A request that fails changes no
record, and a read that answers with a value later reports the field
again. A declared field that the model cannot read back is then sent
once. The controller test sent three times before the change and sends
once after it. On the same day the client ran against a WiiM Amp on
`Linkplay.5.2.830149`, which answers all three reads, and it reported
all three fields. No unit that lacks a read was at hand, so the
sent-once path is proven only against the fake amp. Two proofs under
"How it was proved" have not run: the balance on the Amp across an
operator restart, and the proof of plan 16's phase 2. Phases 2 and 3,
the WiiM's rows in plan 14's engine, are not built.
[Plan 21](../21-the-wiims-field-table.md) holds them, with the field
tables, the notes on each row, the drills, and the corrections that
the rows rest on.

## What is built

Plan 16's phase 2 is built (78f136a5, with 769cb7d4, a787959f,
12fe4791, 7a3d639d, and 19a47568): the client reads the WiiM's settings
families after its subscriptions are live, after a write to a family,
on each new subscription, and at the 30-minute backstop
`settingsResync` (`wiim/client.go:58`), and no longer on the 10-second
poll. Plan 16 records how. This plan took the rest of plan 16: the
zero-value fix, the WiiM's rows in plan 14's engine, and the tier of
each row that a drill must settle. It built the zero-value fix, and
plan 21 holds the rest.

## The problem

A WiiM `Receiver` declares two families today: `device` (`name`,
`led`, `buttons`) and `audio.balance` (`wiim/settings.go:18-34`). Two
faults keep the WiiM from the tier rules that plan 14 states.

**A field the amp cannot read back is sent three times, not once.**
`Pending` treats a nil observed value as a field the device does not
report, and sends it once for each change of the spec (`isPending`,
`wiim/settings.go:108-118`). The controller passes it the value of
`Client.Settings()` (`reconcile.go:496`). That function builds every
field from the stored snapshot with a pointer builder
(`wiim/settings.go:151-163`), so no field is ever nil. A read that the
amp does not answer leaves the snapshot's zero value in place:
`Client.read` (`wiim/client.go:656-672`) returns before the parser runs
when `isMiss` (`wiim/client.go:704-707`) matches the body: `unknown
command`, `Failed`, or empty. So on a model with no `LED_SWITCH_GET`,
the snapshot holds `led: false` (`readControls`, `wiim/client.go:1003`,
and `wiim/state.go:294-295`), and a declared `led: true` differs from
it. The operator sends the command, compares again, and sends it again
until the send budget stops it at `sendLimit`, 3
(`send_budget.go:29`). The field is then named in `SettingsConfirmed`
as unconfirmed, while the amp may hold the declared value. The test
of the sent-once path passes because it builds a nil observed value by
hand (`wiim/settings_pending_test.go:34-48`); the client never does.

**The common families have no WiiM rows.** Plan 14 moves `name` and
`balance` to `spec.settings`. The WiiM also answers `eq`, `subwoofer`,
`inputs`, and `system.autoStandby` in the common vocabulary, and has
brand fields that no other receiver shares.

## Phase 1: the zero-value fix

A field whose read did not answer is reported as absent. Then a
declared field that the amp cannot read back is sent once, as the CRD
already tells the reader (`deploy/receivers-crd.yaml:274`).

The code path:

1. `Client.read` (`wiim/client.go:656`) records, for each read
   command, whether the last answer was a value. A body that `isMiss`
   matches (`wiim/client.go:704`) records "no value". A transport
   error records nothing and keeps the earlier answer, because one
   missed request is not the model's answer (the same reason as
   `pollFailures`, `wiim/client.go:60-64`). Today `read` records in
   `answeredOnce` that a command answered at all, including with a
   body that `isMiss` matches, and that is not the same record.
2. `Client.Settings()` (`wiim/settings.go:151-163`) returns nil for a
   field whose read command has no value: `getChannelBalance` for
   `Balance` (`balanceCommand`, read in `readBalance`,
   `wiim/client.go:894`), `LED_SWITCH_GET` for `LED` (`ledCommand`),
   and `Button_Enable_GET` for `Buttons` (`buttonsCommand`), both read
   in `readControls` (`wiim/client.go:1003`). The read commands are
   constants in `wiim/protocol.go:24-48`. `Name` comes from
   `getStatusEx`, which a poll cannot miss (`readStatusEx`,
   `wiim/client.go:711`).
3. `Pending` and `ConfirmedBy` (`wiim/settings.go:63-118`) and
   `setWiimSettings` (`reconcile.go:490`) already handle a nil
   field. They do not change.

The test: `startFakeWiim` (`wiim_settings_test.go:36`) gains a
variant that answers `unknown command` to `LED_SWITCH_GET`. A declared
`led: true` is sent once, `SettingsConfirmed` does not name it, and a
restart against the same `status.settledSettings` sends nothing.

Phase 1 does not show the reader which fields went to the sent-once
path. Plan 14's `settingExceptions` does that, with the state
`SentOnce`. Phase 1 makes the operator do what the CRD states today.
Plan 14's tier rule is stricter for `led` and `buttons`, whose setters
are undocumented: on a unit that does not answer their reads, phase 2
lists them as Unsupported and sends nothing. Plan 21 holds that phase.

## Phases

The numbers in parentheses are plan 16's phase numbers.

1. The zero-value fix (1). Built on 2026-11-17 (b247ebd7).
2. After plan 14 (3): the WiiM field table in plan 14's engine. Not
   built; [plan 21](../21-the-wiims-field-table.md) holds it.
3. After the drills (4): each row marked "Needs" at the tier its drill
   shows. Not built; plan 21 holds it.

## How it was proved

On a home cluster with a WiiM Amp:

* With `LED_SWITCH_GET` answering `unknown command` in the fake,
  a declared `led` is sent once. The controller test of b247ebd7
  proves this. On the Amp, a declared `balance` is sent, read back,
  and not sent again after an operator restart. This has not run.
* The operator's log shows no settings read between the backstop
  intervals, and a change of the balance in the WiiM Home app shows in
  `status.settings.audio.balance` within 30 minutes. This is the proof
  of plan 16's phase 2, which has not run.
