# 15, Sinks that pass bitstreams through

Plan 15. Built on 2026-11-10. It is audio-operator's part of
[root plan 76](../../../plans/completed/76-bitstream-passthrough.md).
The proof so far ran on a laptop with no monitor on its HDMI output.
The drill on a receiver has not run, and the end of this plan lists
what it must show.

A sink whose monitor decodes Dolby or DTS itself offers those formats
to PipeWire's clients, so a player can send a film's track to the
receiver as the encoded bitstream. The `Sink` reports the formats in
`status.passthrough`.

## What the root plan left open

Root plan 76 recorded two faults from a hand test on PipeWire 1.4, and
stopped until the first one was solved.

1. A bitstream linked to the sink and then played at about one
   hundredth of its speed.
2. After a bitstream ended, WirePlumber gave the next PCM stream two
   channels.

## What was measured

On 2026-11-10, on a laptop with an HDA-Intel card and no monitor on its
HDMI outputs, the published audio-operator image ran PipeWire 1.6.9 and
WirePlumber 0.5.18 in containers, and the published player image ran
mpv 0.41. The sink was declared by hand as `hdmi:0,0` with
`iec958.codecs`. Each test file held 60 seconds of one format, and mpv
played 10 seconds of it.

| Track | What PipeWire opened | Wall time for 10 s |
|---|---|---|
| E-AC-3 | 2 channels at 192 kHz | 10.5 s |
| TrueHD | 8 channels at 192 kHz | 10.5 s |
| DTS | 2 channels at 48 kHz | 10.4 s |
| AC-3 | 2 channels at 48 kHz | 10.3 s |

So fault 1 does not occur on PipeWire 1.6.9. The graph ran at 192 kHz
with the stock `clock.allowed-rates`, and no setting of the rates was
needed. The test did not find which change between 1.4 and 1.6 removed
the fault.

During a bitstream, the card's `IEC958 Playback Default` control read
`AES0=0x02`, which is the bit that marks the stream as not PCM, and
`AES3=0x0e` for 192 kHz. During PCM it read `AES0=0x04`.

Fault 2 occurred every time. A 7.1 stream on a new graph linked eight
ports, and the same stream after one bitstream linked two, `FL` and
`FR`. The cause is in WirePlumber 0.5.18. A sink in passthrough mode
has no port format. When the next link is not a passthrough link,
`configure_and_link_adapters` in `module-si-standard-link.c` finds no
format on the sink or on the new stream, and asks the sink for its
default, which `build_adapter_default_format` in
`module-si-audio-adapter.c` builds with two channels. The format that
WirePlumber configured from the sink's own channels at its start is
gone.

Three more facts decide the design:

* mpv does not decode a track that PipeWire refuses as a bitstream.
  With a sink that listed AC-3 alone and a TrueHD track, mpv's stream
  ended with `no target node available`, mpv opened it again, and the
  loop went on with no sound. So a player must send only the formats
  the running sink lists.
* A bitstream needs the sink to itself. WirePlumber's
  `linking/prepare-link.lua` refused a bitstream while a PCM stream
  was linked to the sink, and refused a PCM stream while a bitstream
  was linked. The stream that was there first played on in both
  cases.
* alsa-lib's name for the device matters. PipeWire's ALSA node offers
  encoded formats only when the device name starts with `hdmi` or
  `iec958` (`enum_iec958_formats` in `spa/plugins/alsa/alsa-pcm.c`),
  because it opens the device again with the channel status bits as
  arguments. alsa-lib's `cards/HDA-Intel.conf` defines `hdmi:<card>,0`
  as PCM device 3 and control index 0, `hdmi:<card>,1` as PCM device 7
  and control index 1, and so on to index 10 for PCM device 16.

## The design

### The formats come from the ELD

`parseELD` keeps the audio coding type of every short audio descriptor
that is not LPCM. `bitstream.go` names the five types a sink passes
through, in PipeWire's names: `AC3`, `DTS`, `EAC3`, `DTS-HD`, and
`TrueHD`. The slice publishes them as the device attribute
`encodedFormats`, so a claim can select a receiver by them, and so
that the hold on a monitor that stops answering
([plan 14](14-a-short-hotplug-drop-changes-nothing.md)) rebuilds them
from the slice after an operator restart.

### The formats travel with the layout

A sink's formats reach PipeWire only through the declaration, as its
channel positions do. So `channelLayout` holds them, and the machinery
of [plan 12](12-channel-layouts.md) applies a change of them: one
comparison finds it, the declaration is written again when no stream
plays, `LayoutApplied` reports the wait, and one `LayoutChanged`
`Event` names the old and new formats. A monitor that does not answer
keeps the declared formats, so a receiver that turns off changes
nothing.

A sink with formats is declared with `api.alsa.path` set to the `hdmi`
device of its PCM device and with `iec958.codecs`. Every other sink is
declared as before. Only an HDMI or DisplayPort output of an
`HDA-Intel` card whose PCM device is in alsa-lib's table gets formats.
A card of another driver has another table, which the operator does
not hold, so its outputs send PCM alone.

### The `Sink` reports what the declaration holds

`status.passthrough` lists the formats of the declaration that
PipeWire runs, and not the formats of the ELD. While a change waits
for a film to end, the status still names what the running sink
accepts, which is what a player must send.

### A hook gives the sink its channels back

`config/restore-device-ports.lua` is a hook in WirePlumber's
`select-target` chain, between `linking/prepare-link` and
`linking/link-target`. Before a passthrough link, it keeps the port
format the sink has. Before any other link to a sink that is still in
passthrough mode, it sets that format again and waits for the ports.
The profile requires the hook, so a WirePlumber that cannot load it
does not start.

With the hook, in the image built from this tree, one mpv played a
7.1 PCM file, E-AC-3, PCM, TrueHD, DTS, AC-3, and PCM. Every PCM file
linked eight ports and every bitstream linked one.

## Considered and set aside

* **Restart WirePlumber after a bitstream.** Plan 76 proposed it. It
  ends every link on the machine, and the operator would have to learn
  when a bitstream ended. The hook corrects the one sink at the moment
  the next stream links.
* **The rates of step one in plan 76.** The plan proposed
  `default.clock.allowed-rates` up to 192 kHz. PipeWire 1.6.9 ran the
  bitstream at 192 kHz without it, so the configuration does not
  change.
* **A second node for bitstreams on the same PCM device.** Two nodes
  cannot hold one device open, and the operator publishes one node
  for each PCM device.
* **The operator's own ALSA definition in place of alsa-lib's table.**
  It would serve cards of other drivers. It is more to own, and every
  `liken` machine tested so far has HDA-Intel HDMI outputs.

## Not proved yet

On a receiver that decodes Dolby Atmos:

* The receiver reports the format, and its height speakers play.
* The bitstream opens while the receiver is on, and a film that
  starts while the receiver wakes behaves as the root plan says.
* A pause of more than a few seconds, after which WirePlumber
  suspends the sink and closes the device, resumes with sound.
* The `hdmi` device name changes nothing for PCM on the outputs that
  played before.
