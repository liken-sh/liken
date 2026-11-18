# The Denon first read can end before the receiver replies

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`denon.Client.readLoop` in `denon/client.go` starts `firstReadTimer`
when the connection opens, before any line arrives. Each recognized
line resets the timer, and when it fires, `recordFirstReadDone` sets
`firstReadDone`. A receiver that accepts the connection and sends its
first reply more than `firstReadQuiet`, one second, after the connect
gets `firstReadDone` true while the client holds no reported state.

The root package applies power, settings, and zones only when
`FirstReadDone` is true and `Reachable` is `True` (`reconcile.go`,
`setPower` and the setters after it). The first recognized line sets
`Reachable` to `True` (`fold`). So when that late first line arrives,
a pass can compare the declared settings with a state that holds one
line of the burst and default values for the rest, and send settings
that the receiver already has.

## Evidence

- `readLoop` calls `time.AfterFunc(firstReadQuiet,
  d.recordFirstReadDone)` before its first `ReadString`.
- The comment on `FirstReadDone` says the root package waits for it
  "so it does not compare the declaration with default values".

## What would confirm it

A test with the fake receiver in `denon/fakeserver_test.go` that
delays its replies to the connect queries by more than
`firstReadQuiet`, and checks `FirstReadDone` before the first reply.

## What a fix has to decide

- Whether the timer starts at the first recognized line instead of at
  the connect.
- What a receiver that never replies to the queries gets: no first
  read at all, or one after `silenceLimit` ends the connection.
