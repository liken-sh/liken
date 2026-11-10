# 45, A `Player` passes bitstreams through

Plan 45. Built on 2026-11-10. It is media-operator's part of
[root plan 76](../../../plans/completed/76-bitstream-passthrough.md).
The drill on a receiver has not run, and the end of this plan lists
what it must show.

A `Player` that states `passthrough: On` sends a film's Dolby and DTS
tracks to its receiver encoded, in the formats that its `Sink` lists.

## What decides the design

[audio-operator's plan 15](../../../audio-operator/plans/completed/15-sinks-that-pass-bitstreams-through.md)
records the measurements. Three of them decide this plan:

* mpv sends a track as a bitstream only when `--audio-spdif` names its
  codec, and decodes every other track.
* mpv does not decode a track that PipeWire refuses as a bitstream.
  Its stream ends with `no target node available`, mpv opens it again,
  and the film plays with no sound. So mpv must get only the codecs
  that the running sink accepts.
* PipeWire cannot scale a bitstream, so the sink's level and mute do
  nothing while one plays.

## The design

`spec.passthrough` on the `Player` is `On` or `Off`, and an absent
field is `Off`. It is off by default because it moves the control of
the level from the cluster to the receiver.

When the operator creates a playback pod, `passthroughFor`
(`passthrough.go`) decides the codecs. It answers codecs only when all
of these are true:

* The `Player` states `On`.
* The `Player` has one entry in `spec.sinks`, because one mpv stream
  goes to one sink.
* A `Receiver` sets the unit's level. `levelReceiver` makes that
  decision for `volumeDevices` too, so the volume keys reach the same
  receiver that decodes the track.
* `status.sinks` names a `Sink`, and that `Sink` lists one or more of
  `AC3`, `EAC3`, `DTS`, `DTS-HD`, and `TrueHD` in `status.passthrough`.

The codecs reach mpv as `--audio-spdif` in `MEDIA_PLAYER_OPTIONS`,
with the preference options. In every other case mpv gets no such
option and decodes, as before. Each play start on a `Player` that
states `On` writes one log line, which names the codecs and the sink,
or the reason the play decodes.

The operator reads the `Sink` that the last allocated playback claim
resolved, because it creates the pod before the scheduler allocates
this play's claim. So a `Player`'s first play decodes. A `Player`
whose sink selection matches more than one device can also get the
formats of the device it played through last.

## Not built

* The display does not show the format the receiver gets. Root plan
  76 proposed a line such as "Dolby Atmos, bitstream".
* The media browser shows no mark for a title with an Atmos or DTS:X
  track.
* A refused bitstream still plays with no sound.
  [The open problem](../open-problems/a-refused-bitstream-plays-nothing.md)
  has the evidence and a remedy.

## Not proved yet

On a `Player` with a receiver that decodes Dolby Atmos:

* An E-AC-3 film and a TrueHD film with Atmos play, the receiver
  reports Dolby Atmos, and the height speakers play.
* The volume keys change the receiver's level during the film.
* A change of the audio track during a film, to a track mpv decodes
  and back, keeps the sound.
* With `passthrough: Off`, and on a television that lists no formats,
  the same films play decoded.
