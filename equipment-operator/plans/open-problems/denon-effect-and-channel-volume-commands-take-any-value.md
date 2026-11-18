# Denon effect and channel volume commands take any value

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Each command builder in `denon/protocol.go` returns an error for a
value outside the receiver's range, except two. `EffectCommand`
formats any integer as `PSEFF %02d`, so -5 becomes `PSEFF -5` and 150
becomes `PSEFF 150`. `ChannelVolumeCommand` checks only that the
channel name is not empty. It converts the trim with
`int((display + 50) * 2)`, which truncates toward zero, and passes the
result to `HalfStepDigits`, which does not handle a negative count.
The `Receiver` CRD gives `effect` and `channelVolumes` no `minimum`,
`maximum`, or `multipleOf`.

A declared trim that is not a whole half step does not match the
value the receiver reports. A trim of 0.3 sends `50` (0 dB), and
`parseChannelVolume` reads back 0.0. `Settings.Pending` in
`denon/settings_pending.go` compares the two as `float64` values, so
it returns the trim on every pass until the send budget of
`sendLimit`, three sends, ends.

## Evidence

- `EffectCommand` has no range check, while `DelayCommand`,
  `AudioDelayCommand`, and `LFECommand` beside it each have one.
- `ChannelVolumeCommand` and `HalfStepDigits` in `denon/level.go`.

## What would confirm it

A unit test of `ChannelVolumeCommand` with 0.3 and -60, and of
`EffectCommand` with -5 and 150. A test of `Pending` with a declared
trim of 0.3 and a reported trim of 0.0.

## What a fix has to decide

- Where the range is enforced: in the CRD schema, in the command
  builders, or both.
- The receiver's range for the effect level and for a channel trim,
  which the repository does not record.
- Whether a trim between half steps is refused or rounded.
