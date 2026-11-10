# 91, Loudness from title to title

This plan, written on 2026-11-09, records an idea for later:
library-operator measures the loudness of each audio stream, and
media-operator uses the measurement to play each title at about the
same level, so a person does not turn the volume up for one film and
down for the next. The work touches library-operator, media-operator,
and equipment-operator. It is a root plan for the same reason as
[plan 90](completed/90-subtitle-tracks-by-role.md): a fact that
library-operator's probe records travels on the `Play` to
media-operator, which acts on it. When a design session settles the
contract, the detail of each part goes into that component's own
`plans/`.

Status on 2026-11-09: a stub, not designed. Nothing is built. The
measurement decodes every audio stream in the library from start to
end, so it needs a design session of its own before anyone builds it.

## What a person gains

Films and series are mixed to different levels. Today the only
correction is the remote's volume key, pressed again at the start of
each title. A measured level for each stream lets the player start
each title near one target. A person could also ask for a narrower
range at night, so that speech stays audible and explosions do not
wake a sleeping child.

## What `liken` has to build on

- library-operator's probe fact (`library-operator/probe.go`) writes a
  row for each audio stream to the `streams` table, with its codec,
  profile, channels, and layout. The probe reads only the container's
  header, under a bound of one minute for each file
  (`ffprobeTimeout`). A full decode does not fit in it as it is.
- library-operator's heavy facts, trickplay and appearances, run in
  worker Jobs of their own (`library-operator/factworkers.go`), so a
  decode of minutes for each title does not hold the walk.
- Plan 90 put a per-track fact on each item of the `Play`: the
  `subtitles` list of `media-operator/deploy/plays-crd.yaml` carries
  each subtitle track's event count from the probe to the command
  sidecar. A loudness for each audio track could travel the same way.
- media-operator does not change mpv's own level during a `Play`. A
  volume press becomes an ask on the `Receiver` or on the `Sink`
  objects ([plan 77](completed/77-the-media-bus-stops-at-media-operator.md)),
  and [media-operator plan 43](../media-operator/plans/43-the-level-follows-the-sound.md)
  proposes which of the two takes it.
- [Plan 76](completed/76-bitstream-passthrough.md) states that PipeWire cannot
  scale a bitstream, so mpv's level and the sink's level do nothing
  during passthrough. Only the receiver's own volume changes the
  level then.
- A Denon `Receiver` already declares the receiver's own range
  controls, `spec.denon.settings.audyssey.dynamicVolume` and
  `spec.denon.settings.audio.drc`.

## The evidence so far

FFmpeg's `ebur128` filter, run on five minutes from 30:00 of the first
audio stream of two films on 2026-11-09, for a review of the media
system, read Iron Man at -21.9 LUFS
with a loudness range of 19.2 LU, and Hannibal at -35.3 LUFS with a
range of 10.6 LU. That is one sample of each film, so it shows the
size of the spread and not a figure for a library. For reference,
EBU R 128 sets a target of -23 LUFS for broadcast, and Netflix's
delivery specification states -27 LKFS, gated on dialogue.

On a home library on 2026-11-09, the catalog held 12,232 audio
streams in 10,083 files.

## The compute cost

An integrated loudness needs the whole stream decoded. For the library
above that is up to 12,232 decodes of a feature's or an episode's
length, or fewer if only the stream that plays by default is measured.
Audio decodes on the CPU on every machine. Dolby TrueHD and DTS-HD
Master Audio tracks decode in software at up to eight channels, so
they cost more than a stereo track. The read is every byte of the
file, because the audio packets are spread through the container.

The appearances pass reads every byte of a feature too, and its
`ffmpeg` command could decode an audio stream in the same read. But it
drops the audio today (`-map 0:v:0 -an` in
`library-operator/appearances/src/frames.rs`), runs only where a
`Library` turns it on, and covers only titles whose cast has a
headshot. A measurement of a sample, as in the evidence above, would
cost a fraction of a full decode and give a less exact level.

## Licenses and terms

FFmpeg's `ebur128` and `loudnorm` filters are part of FFmpeg, which
the repository's `ffmpeg` base image already carries. `libebur128` is
MIT. No outside service or data is involved.

## What a design session must decide

- Whether the probe measures loudness, or a heavy fact of its own
  does, and whether it measures every audio stream or only some.
- Where the value is kept: the probe ledger, a ledger of its own, new
  `streams` columns, and the field on the `Play` item.
- The target, and whether it follows R 128 or a dialogue-gated
  measure.
- How media-operator applies an offset to decoded PCM: mpv's own
  level, an mpv audio filter, or the sink, given that plan 77 sends a
  person's volume press to the sink or the receiver.
- How it applies an offset to a bitstream, where only the receiver's
  volume acts: a `Receiver` volume ask at the start of a title, how
  that ask is undone, and how it meets plan 77's note that a Denon's
  scale is in dB while PipeWire's gain is linear.
- Whether a narrower night range comes from mpv's `loudnorm` or
  `dynaudnorm`, from the receiver's own controls, or from both, and
  who turns it on.
- What happens to a title with no measurement.

## What is not decided

The tool, the target, where each part runs, the fields on the `Play`
and the `streams` table, and how the offset reaches the receiver.
None of these is chosen here.
