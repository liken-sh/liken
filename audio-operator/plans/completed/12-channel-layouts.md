# 12, Channel layouts

Plan 12. Built on 2026-09-30. No drill has run on hardware yet: the
evidence below comes from hand tests before the build, and the drill
that is still owed is in the last section.

Every sink the operator declares names its channel positions, so a
multichannel stream reaches a multichannel sink with its channels
intact instead of folded into the front pair. The positions come from
`spec.layout` on the `Sink` when a person states them, from the
monitor's ELD for HDMI and DisplayPort, and from the device's own
channel map for USB. A sink with no source of positions stays as it is
today.

## The problem

The operator declares each ALSA sink in PipeWire's `context.objects`
with `api.alsa.path` and no channel positions, because the ALSA monitor
and its card profiles are off on a `liken` machine. The node then
reports eight channels whose positions are unknown. WirePlumber 0.5
marks such a sink unpositioned when it first reads the node's formats,
and configures every stream linked to it as two channels, `FL` and
`FR`. So every multichannel stream on every `liken` sink is downmixed
to stereo before it reaches the sink.

On a home cluster, an HDMI sink feeding a 7.1 receiver showed it. mpv
opened a 7.1 stream, WirePlumber linked only `FL` and `FR`, and a tap of
the sink through `audio-api` during the speaker walk of media-operator's
`pattern://speakers` carried sound on channels 0 and 1 alone: the
center at -3 dB on both sides, each surround on its own side at -3 dB.
A stereo panel on the testbed measured the same numbers to the tenth of
a decibel.

The cause is in WirePlumber 0.5.8's own source.
`si_audio_adapter_find_format` reads the node's `EnumFormat` once, when
the session item is configured, and sets `SPA_AUDIO_FLAG_UNPOSITIONED`
when the format has no position array. `configure_adapter` copies the
sink's format onto a stream only when neither side is unpositioned, and
otherwise builds the default format, which is two channels. Nothing
reads the format again while the node lives, and no WirePlumber setting
assigns positions to a node that WirePlumber did not create.

## What was proved

On the same sink, by hand:

* Writing `audio.position` and `audio.channels` to the node's `Props`
  gave `EnumFormat` the positions. After a restart of the WirePlumber
  container, a 7.1 stream linked eight ports by name, the receiver
  reported multichannel PCM, and the walk played the front speakers,
  the center, and the subwoofer each on its own.
* With `audio.channels` and `audio.position` in the declaration itself,
  `EnumFormat` carried them from PipeWire's start, and streams linked
  eight channels with no other step.

So the fix is the one PipeWire documents for an ALSA node: declare
`audio.channels` and `audio.position`.

## The design

### Where the positions come from

The declare init container chooses each sink's layout before PipeWire
starts, from the first source that gives one:

1. **`spec.layout` on the `Sink`.** A person states the positions in
   PCM slot order, in PipeWire's channel names: `[FL, FR, RL, RR, FC,
   LFE, SL, SR]`. This is the only source for an analog output, and it
   overrides the sources below when a device reports its layout wrong.
2. **The ELD, for HDMI and DisplayPort.** The monitor's speaker
   allocation, capped by its largest LPCM channel count, selects one of
   PipeWire's own HDMI layouts:

   | The monitor advertises | `audio.channels` | `audio.position` |
   |---|---|---|
   | 8-channel LPCM and `FL/FR`, `LFE`, `FC`, `RL/RR`, `RLC/RRC` | 8 | `FL,FR,RL,RR,FC,LFE,SL,SR` |
   | 6-channel LPCM and `FL/FR`, `LFE`, `FC`, `RL/RR` | 6 | `FL,FR,RL,RR,FC,LFE` |
   | anything else | 2 | `FL,FR` |

   The strings are ACP's `hdmi-surround71` and `hdmi-surround`
   channel maps, the ones every Linux desktop uses for HDMI, and they
   match the kernel's default routing. PipeWire never writes the
   kernel's channel map, so the kernel chooses the HDMI channel
   allocation from the channel count and the ELD and routes the PCM
   slots in its standard order: for eight channels, slots 3 and 4 reach
   CEA `RLC/RRC` and slots 7 and 8 reach CEA `RL/RR`
   (`hdmi_std_setup_channel_mapping` in `sound/hda/core/hdmi_chmap.c`).
   A stereo television that accepts 8-channel LPCM gets `FL,FR`,
   because it has two speakers, and PipeWire folds the center into them
   instead of sending dialog to a slot the set ignores.
3. **The channel map, for USB.** A USB Audio Class device describes its
   channels in its descriptors, and the kernel turns them into the PCM's
   channel map. The node sets `api.alsa.use-chmap = true` and PipeWire
   reads that map itself. The option stays off for HDMI, because there
   the map lists positions in an order that differs from the kernel's
   routing.
4. **Nothing.** A built-in analog output reports no speakers, and the
   node is declared as it is today. Its streams stay stereo until a
   person states `spec.layout`.

An HDMI output whose ELD is absent at the declare step, because the
monitor is off or unplugged, has no layout from step 2 and is declared
as in step 4.

### When the layout changes

PipeWire reads the declaration once, and WirePlumber reads a node's
formats once, so a new layout needs a new PipeWire. The operator
already compares the declaration it would write now with the one
PipeWire started with. The comparison grows a second kind of
difference:

* A **layout difference**: `spec.layout` changed, or an HDMI output now
  has a valid ELD whose layout differs from the declared one. An ELD
  that becomes absent is not a difference, so a television that turns
  off changes nothing.
* The PCM set difference that the operator reports today is unchanged.

On a layout difference, the operator writes the new declaration over
the old one and restarts the PipeWire container once no sink of the
card has a link: never in the middle of a film. WirePlumber restarts
with it and reads every node again. The pod is not replaced, so the
operator, its watches, and its claims keep running. The `Sink` carries
a `LayoutApplied` condition that is `False` with the reason
`AwaitingIdle` while a change waits, and each restart writes one log
line and one `Event` that name the old layout, the new one, and the
source.

The restart uses the mechanism of [plan 06](06-restarting-wireplumber-when-the-bus-dies.md):
PipeWire is a native sidecar with `restartPolicy: Always`, and the
operator asks the kubelet for the restart through the container's
liveness probe. The declare container stays the writer at pod start,
and the operator becomes the writer for a layout change, so the
drop-in volume is mounted writable in the operator container.

### What the `Sink` reports

`status.layout` holds the declared positions, and
`status.layoutSource` holds `Spec`, `ELD`, `ChannelMap`, or `None`.
`kubectl get sinks` shows both. The `Sink` reference documents
`spec.layout` with the layouts above as examples, and says what the
operator does not do:

* It does not write the kernel's channel map, so a height position in
  `spec.layout` names a slot that the kernel routes by its standard
  allocation. The kernel can route front heights (CEA allocation 0x2f),
  but only when something writes the PCM's channel map while the device
  is open and stopped.
* It does not pass a bitstream through. A receiver that renders height
  speakers from Dolby Atmos or DTS:X needs passthrough, which is a
  separate design.

## Considered and set aside

* **Set the layout on the running node.** It worked by hand: write
  `Props` and restart WirePlumber. A declared layout reaches the same
  state with one mechanism, and a node created with its positions has
  no window in which a stream links as stereo.
* **`api.alsa.use-chmap` for every sink.** For HDMI, PipeWire takes the
  channel map's positions in the order the map lists them (`FL FR LFE FC
  RL RR RLC RRC` for 8 channels), while the kernel routes the slots in
  its standard order, so the center and the subwoofer would reach the
  wrong speakers. The kernel's HDMI map also names the rear pairs `RLC`
  and `RRC`, which no stream position matches.
* **One 7.1 layout for every HDMI sink.** Most televisions accept
  8-channel LPCM and play only the front pair, so the center channel,
  which carries the dialog, would be lost.
* **The layout in the declaration only at pod start.** A monitor that
  is off when the pod starts would leave its sink stereo until someone
  deleted the pod.
* **Height layouts by writing the kernel's channel map.** The kernel
  accepts the write only while the PCM is open and stopped, and resets
  it on close, so the operator would have to race PipeWire at every
  open. A receiver tested on a home cluster ignored height positions in
  LPCM anyway: its manual lists multichannel PCM as 7.1, and its
  Multi Ch In and Direct modes left the height speakers silent.

## How it was built

Three details of the build differ from the design above.

* The restart waits until no link touches any node the operator
  publishes on the machine, not only the sinks of one card. One
  PipeWire serves every card and every Bluetooth speaker in the pod,
  so its restart ends the streams on all of them.
* The liveness probe reads a fact on disk: the drop-in is newer than
  PipeWire's socket, which PipeWire creates at each start. The
  operator writes the drop-in and holds no other record, so an
  operator that restarts between the write and the restart changes
  nothing.
* A rewrite for a layout change keeps the set of PCM devices the
  running declaration holds. A PCM device that appeared since the pod
  started still waits for a replacement pod, as before.

## How it will be proved

On the testbed and on a home cluster:

* On a stereo panel, `status.layout` is `FL,FR` from the ELD, and the
  walk plays as before.
* On a 7.1 receiver, `status.layout` is `FL,FR,RL,RR,FC,LFE,SL,SR` from
  the ELD, a tap of the sink during the walk carries each speaker on
  its own channel, and the speakers play in the walk's order. A room
  with surround-back speakers confirms that the side and back pairs
  reach the speakers their names say.
* `spec.layout` on an analog output makes a 5.1 stream reach six
  channels, and removing it returns the sink to `None`.
* A receiver turned off and on again with the same layout restarts
  nothing. A new layout while a film plays waits for the film to end.
