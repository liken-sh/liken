# Denon effect and channel volume commands take any value

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

A table test calls `EffectCommand` with -5 and 150, and
`ChannelVolumeCommand` with -60 and 0.3, and wants an error from each.
Each case failed and named the line that the builder returned:

```
the builder returned "PSEFF -5", want an error
the builder returned "PSEFF 150", want an error
the builder returned "CVFL 0-10", want an error
the builder returned "CVFL 50", want an error
```

The test is not in the tree, because a failing test cannot land on
`main`.

The test did not cover `Pending` with a trim of 0.3 and a reported
0.0. The resend until `sendLimit` follows from the `float64`
comparison in `Settings.Pending`, read in the code.

## What a fix has to decide

The operator sends an effect level or a channel trim outside what the
receiver accepts, and sends a trim between half steps, such as 0.3
dB, again until the send budget ends.

- What range does the receiver accept for the effect level and for a
  channel trim? The repository does not record it.
- Is the range enforced in the `Receiver` CRD, in the command
  builders, or in both?
- Is a trim between half steps refused, or rounded to the nearest
  half step?
