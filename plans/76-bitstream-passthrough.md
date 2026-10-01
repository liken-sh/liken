# 76, Bitstream passthrough

Proposed on 2026-10-01. A film's Dolby or DTS track can go to an AV
receiver as the encoded bitstream instead of as decoded PCM, so the
receiver can decode it itself. That is the only way a receiver's height
speakers play Dolby Atmos or DTS:X. The work touches audio-operator,
media-operator, and equipment-operator.

## The problem

mpv decodes every audio track to PCM. For an Atmos track, ffmpeg's
TrueHD and E-AC-3 decoders output the 7.1 or 5.1 bed and drop the height
objects. Plan 12 in audio-operator made that bed reach the receiver with
its channels intact, but the heights are still lost.

PCM cannot carry them. A receiver tested on a home cluster, a 7-channel
model set up as 5.1.2, lists multichannel PCM over HDMI as 7.1 and drives
its height speakers only from Dolby Atmos or DTS:X, or from its own
upmixers. Writing the kernel's HDMI channel map to route PCM to the
front-height slots made those channels go silent instead.

## What was proved

On that receiver, by hand, on one HDMI sink:

1. PipeWire offers compressed formats only for an ALSA device whose name
   starts with `hdmi` or `iec958` (`alsa-pcm.c` in PipeWire 1.4). A sink
   declared as `hw:0,3` never offers them. Declaring the same output as
   `hdmi:0,0` with `iec958.codecs = [ PCM AC3 DTS EAC3 TrueHD DTS-HD ]`
   made the sink offer every one of those codecs. alsa-lib's card
   configuration maps `hdmi:0,0` to device 3 on that card, and the
   `hdmi` device also sets the HDMI non-audio flag a receiver uses to
   tell a bitstream from PCM.
2. With `audio-spdif=eac3,truehd,dts-hd,dts,ac3` set over mpv's IPC
   socket and the audio track reloaded, mpv sent an E-AC-3 Atmos track
   as `spdif-eac3` at 192 kHz, and PipeWire linked it unchanged to the
   sink in `iec958` format.
3. Playback then crawled: mpv's clock advanced 0.04 seconds in 3
   seconds. PipeWire's graph ran at 48 kHz, its allowed rates were
   `[ 48000 ]`, and a bitstream cannot be resampled. Forcing the graph
   to 192 kHz at runtime (`clock.force-rate`) stopped playback entirely.
   PipeWire logged nothing at its warning level. This is not solved.
4. After passthrough ended, WirePlumber left the sink's port
   configuration at two channels, so the next PCM stream folded to
   stereo until WirePlumber restarted.

## The design

### Step one: make passthrough play

Before anything is built, a hand test has to make one bitstream play at
the right speed. The likely cause of the stall is the graph rate:
PipeWire switches the graph's rate only to a rate in
`clock.allowed-rates`, and the context allowed 48 kHz alone. The test
sets `default.clock.allowed-rates = [ 44100 48000 88200 96000 176400
192000 ]` in the static PipeWire configuration, raises PipeWire's log
level, and plays an E-AC-3 track and then a TrueHD track. TrueHD needs
HDMI's high bitrate mode: eight channels at 192 kHz carrying one
bitstream. If either stalls, the log level shows where, and this plan
waits until it plays.

The same test checks finding 4: after a bitstream ends, does a PCM
stream link with the sink's full layout again? If not, the operator has
to restart WirePlumber after passthrough, with the restart mechanism
plan 12 built, or the behavior goes to WirePlumber upstream.

### audio-operator: sinks that accept bitstreams

For an HDMI or DisplayPort sink whose ELD lists compressed formats in
its short audio descriptors, the operator:

* declares the node with the ALSA `hdmi` device name for that output
  instead of `hw:`. The name's index is the output's position among the
  card's HDMI devices in alsa-lib's card configuration, and the operator
  checks the mapping instead of assuming it, because each driver's
  configuration numbers its devices differently.
* sets `iec958.codecs` to the codecs the ELD lists. The ELD already
  names them: the tested receiver listed AC-3, E-AC-3, DTS, DTS-HD, and
  TrueHD (MLP).
* reports them on the `Sink` as `status.passthrough`, so a person and
  media-operator can read what the sink accepts.

The PipeWire configuration allows the rates from step one on every
machine. A sink with no compressed formats in its ELD is declared as it
is now. Analog, USB, and Bluetooth sinks never pass a bitstream through.

### media-operator: when to pass through

A `Player` opts in with a passthrough setting, off by default, because
passthrough moves control of the sound from the cluster to the receiver.
When it is on, the playback pod reads the sink's accepted codecs, and
mpv gets `--audio-spdif` with the ones it should pass through. mpv
passes a track through only when its codec is in that list and decodes
it otherwise, so a TV that accepts only AC-3 still plays a TrueHD film,
decoded.

The display shows the format the receiver is getting, for example "Dolby
Atmos, bitstream", because the OSD's volume works differently while a
bitstream plays.

### Volume

PipeWire cannot scale a bitstream, so mpv's volume and the sink's volume
do nothing during passthrough. The receiver's own volume does. A
`Player` whose `Receiver` (equipment-operator) holds its session already
sends the Player's volume to the receiver over the session's volume
topic. Passthrough is allowed only for such a `Player`, so a remote's
volume buttons keep working. A `Player` with no `Receiver` keeps
decoding.

### The library

The catalog's `streams` table already holds each track's codec,
profile, channels, and layout. The profile names Atmos, as in "Dolby
TrueHD + Dolby Atmos". The media browser can show an Atmos or DTS:X mark
from it, and nothing in the library needs to change for passthrough
itself.

## Considered and set aside

* **Height positions in PCM.** The kernel can route a 5.1.2 layout to
  HDMI's front-height slots, but the tested receiver ignored them, and
  the kernel only accepts that channel map while the device is open and
  stopped.
* **Passthrough on by default.** It would move volume control to a
  receiver the cluster may not control, and a wrong codec list would
  make a film silent.
* **Decoding Atmos in the cluster.** No open decoder renders Atmos
  objects to speakers.
* **The ALSA device directly from mpv**, bypassing PipeWire. It would
  work for one player, but it takes the device away from PipeWire, so
  the sink's volume, its taps, and every other client lose it while a
  film plays.

## How it will be proved

On a home cluster with an Atmos-capable receiver:

* An E-AC-3 Atmos film and a TrueHD Atmos film play at normal speed.
  The receiver reports Dolby Atmos, and its height speakers play
  overhead sound.
* After the film ends, the speaker walk plays with the sink's full
  layout, with no restart by hand.
* The remote's volume buttons change the receiver's volume during
  passthrough.
* A `Player` with passthrough off, and a TV that accepts no compressed
  formats, play the same films as decoded PCM.
