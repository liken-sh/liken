# A refused bitstream plays nothing

Open problem. A `Player` with `passthrough: On` gives mpv the codecs
that its `Sink` lists ([plan 45](../completed/45-a-player-passes-bitstreams-through.md)).
When PipeWire then refuses the bitstream, mpv does not decode the
track. The film plays with no sound until someone stops it.

## The evidence

On 2026-11-10, on a laptop, with the PipeWire 1.6.9 of the
audio-operator image and the mpv 0.41 of the player image:

* A sink that listed AC-3 alone refused an E-AC-3 bitstream. mpv's
  log repeated `Stream in error state, trying to reload` and
  `Error during playback: No such file or directory, no target node
  available`. `audio-out-params/format` still read `spdif-eac3`, so
  that property does not show the fault.
* mpv decodes after a failed bitstream only when the audio output
  fails to open. Its PipeWire output opens before WirePlumber links
  the stream, so the refusal arrives later, as a stream error.
* While that loop ran, `set_property audio-spdif ac3` over mpv's IPC
  socket made mpv open a PCM stream, and the file played.

## When PipeWire refuses

* Another stream holds the sink when the film starts. A tap of the
  sink through `audio-api` is one.
* The `Sink` the operator read is not the sink this play got, or its
  formats changed after the operator read them. The receiver's ELD
  changes with its power on some receivers.

## A remedy

The command sidecar owns mpv's IPC socket. It could ask for mpv's log
messages at the error level, and on the stream error from
`ao/pipewire` while `audio-out-params/format` starts with `spdif`, set
`audio-spdif` to nothing, so the play continues decoded. One log line
would say that the bitstream was refused.

Undecided: whether the sidecar should try the bitstream again later
in the same play, and whether a refusal should also reach the `Play`
as a condition or an `Event`.
