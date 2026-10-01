# 41, The speaker walk

Built on 2026-09-30. `pattern://speakers` plays pink noise from one
speaker of a 7.1 layout at a time and names the speaker on the screen,
so a person can check that each speaker of a home theater is wired to
the channel it should play. It extends the test patterns of
[plan 40](40-test-patterns.md). The walk played on `liken-1`; the
results are at the end.

## The problem

A film that sounds wrong can have a speaker on the wrong channel, a
speaker that does not play, or a receiver that downmixes. A film does
not show which one. A receiver's own setup menu walks the speakers,
but it tests the receiver alone: the operator decodes the film with
mpv and sends PCM through PipeWire to the sink, and a setup menu never
passes through that path.

## The design

The walk is one 40-second file, `speakers-1280x720.mkv`, beside the
other patterns. Pink noise plays from each speaker for 5 seconds, in
the order a receiver walks a room: front left, center, front right,
side right, back right, back left, side left, and then the subwoofer.
Pink noise is what a receiver's own walk plays. The subwoofer's noise
stops at 120 Hz.

The picture names the speaker that should play, with its channel and
its place, such as "FC · 2 of 8". Each speaker is a chapter, so the
scrubber reads "Front left · 1 of 8" and a chapter skip moves to the
next speaker. The picture is 1280x720 at one frame a second, because
the file is for the ears.

The audio is AAC at 80 kb/s with the cutoff forced to 20 kHz. With
the default cutoff at 128 kb/s, the encoder divided the rate among all
eight channels, although seven are silent at any time, and cut every
band above 8 kHz: energy above 8 kHz measured -74 dBFS against -36 dBFS
in the source. With the cutoff forced, it measures -36.8 dBFS at
80 kb/s. The rate keeps the file under the 500 KB that the
repository's large-file check allows: 128 kb/s made 663 KB, 96 kb/s
527 KB, and 80 kb/s 470 KB. The noise measures about 1 dB above the
source at 80 kb/s, which is the encoder's own noise.

A pattern now carries its own frames, so `pattern://speakers` plays
its one frame on any screen, and `pattern://speakers/1920x1080` fails
with a message that lists `1280x720`.

The file is 7.1 and nothing else. A system with fewer speakers plays a
downmix, and the walk shows where each missing speaker's channel lands.

## Considered and set aside

* **Opus.** The walk's audio in Opus at 192 kb/s was 289 KB, against
  470 KB for the whole file in AAC. The workstation's ffmpeg printed "Error
  parsing Opus packet header" on every decode of a 7.1 Opus file, and
  of a stereo one, while it decoded the audio correctly. A playback
  log that reports an error on every walk would mislead the person who
  reads it.
* **A sine tone.** A single tone is uneven in a room, and a small
  speaker plays a low tone poorly.
* **A 5.1 walk.** A 5.1 system hears the 7.1 walk as a downmix, which
  is itself a test of the chain.
* **Ten minutes, like the picture patterns.** 40 seconds is enough to
  walk a room once, and a longer walk multiplies the audio, which is
  most of the file.

## How it is proved

On a workstation, the file decoded back to eight channels carries noise
on one channel in each 5-second window, at about -25.7 dBFS, and the
subwoofer at -30.7 dBFS. The other seven channels are silent in each
window. The file is 470 KB, and `make` writes it in under half a
second, holding 92 MB.

On 2026-09-30, on `liken-1`, `pattern://speakers` played on
`lab-portable`, whose sink is the panel's two speakers. A 50-second tap
of the sink through `audio-api` measured each window's middle three
seconds:

| Speaker | Left | Right |
|---|---|---|
| Front left | -25.7 dBFS | silent |
| Center | -28.8 dBFS | -28.8 dBFS |
| Front right | silent | -26.0 dBFS |
| Side right | silent | -29.0 dBFS |
| Back right | silent | -29.1 dBFS |
| Back left | -28.8 dBFS | silent |
| Side left | -28.7 dBFS | silent |
| Subwoofer | -39.8 dBFS | -39.8 dBFS |

The front speakers reach their own side at the file's level, the center
reaches both sides 3 dB down, each surround reaches its own side 3 dB
down, and the subwoofer reaches both sides about 9 dB below its level
in the file. No speaker reached the wrong side. The tap shows the
downmix at the sink, and does not show whether mpv or PipeWire made it.
A run on a 7.1 sink is still owed.
