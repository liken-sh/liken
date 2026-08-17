# Every sink is declared stereo

Open problem. The operator declares every sink node with
`audio.channels` set to `2` and `audio.position` set to `FL,FR`. A
receiver, a soundbar, or a television that accepts more than two
uncompressed channels still gets two.

## What the declaration says now

`sinkObject` in `nodes.go` writes both properties onto every object,
for every playback PCM device, HDMI and analog alike. The recorded
reason is the ELD. A monitor's ELD reports the highest uncompressed
channel count it accepts, and the operator already publishes that
number as the `lpcmChannels` attribute. The ELD is readable only while
the monitor is connected, and the drop-in is written once before the
daemons start. A layout taken from a cable would build one graph when
the monitor is plugged in at boot and a different graph when somebody
plugs it in later. Stereo is what every HDMI monitor and every analog
jack accepts, so it is the layout that is true at every start.

That reason says why the count does not come from the ELD. It does not
say why the two properties are there at all. Two is a number this
operator chose. It is not a number it read off the card.

## What PipeWire does when the two properties are absent

This is the question the rest of the document turns on. If a node
declared without a channel count takes the count from the hardware, then
the stereo constraint is self-imposed and removing it costs two deleted
lines.

The reading is of PipeWire master, which `meson.build` gives as version
`1.7.0`, and of the release tag `1.4.11`. It also covers WirePlumber
master, which `meson.build` gives as version `0.5.15`, the same as its
newest tag. All three were fetched from
`https://raw.githubusercontent.com/PipeWire/`. Line numbers below are
master's, and each one that was also checked at 1.4.11 says so.

**`audio.channels` has no default other than zero.** `alsa_set_param`
in `spa/plugins/alsa/alsa-pcm.c:167` is the only place that assigns
`state->default_channels`, and it assigns it from the
`SPA_KEY_AUDIO_CHANNELS` property, which is `audio.channels`. No other
line in the file writes that field. `pw_load_spa_handle` in
`src/pipewire/pipewire.c:268` allocates the SPA handle with
`calloc(1, sizeof(struct handle) + spa_handle_factory_get_size(factory, info))`,
and `impl_get_size` in `spa/plugins/alsa/alsa-pcm-sink.c:864` returns
`sizeof(struct state)`, so the whole `struct state` starts zeroed. With
the property absent, `default_channels` is 0.

**Zero means the hardware's range passes through.** `add_channels` in
`alsa-pcm.c:1589` reads the device with
`snd_pcm_hw_params_get_channels_min` and
`snd_pcm_hw_params_get_channels_max`, then narrows the result only when
the property was given:

    if (state->default_channels != 0 && !all) {
            if (min > state->default_channels ||
                max < state->default_channels)
                    spa_log_warn(state->log, "given audio.channels %d out of range:%d-%d",
                                    state->default_channels, min, max);
            else
                    min = max = state->default_channels;
    }

That is `alsa-pcm.c:1607` at master and `alsa-pcm.c:1583` at 1.4.11,
identical in both. With the property absent the branch does not run, and
`min` and `max` stay what the PCM device reported.

**The node then advertises a range whose default is the maximum.**
`state->props.use_chmap` is false unless `api.alsa.use-chmap` sets it
(`alsa-pcm.c:208`) or `api.alsa.open-ucm` sets it (`alsa-pcm.c:1007`).
This operator sets neither, so `add_channels` takes the else branch and
builds `SPA_FORMAT_AUDIO_channels` as a `SPA_CHOICE_Range` in the order
`max`, `min`, `max` (`alsa-pcm.c:1657` to `1660`). The first value of a
SPA choice is its default value, so the default is the device's
maximum.

**The adapter passes the range through untouched.** An adapter object
runs `do_auto_port_config` only when the `adapter.auto-port-config`
property is present, and otherwise comes up in port config mode `none`
(`spa/plugins/audioconvert/audioadapter.c:2281` to `2284`). This
operator sets no such property, so mode `none` is what its nodes get.
In that mode `impl_node_port_enum_params` gives `SPA_PARAM_EnumFormat`
no special handling and forwards the call to the follower, which is the
ALSA node (`audioadapter.c:1779` to `1785`). So the range the ALSA node
built is the range the adapter node publishes. Nothing in PipeWire
narrows it, and nothing in PipeWire sets `SPA_PARAM_PortConfig` either.

**WirePlumber is what settles on a number, and it takes the largest.**
`si_audio_adapter_find_format` in WirePlumber's
`modules/module-si-audio-adapter.c:152` enumerates the node's
`EnumFormat` with a null filter (`:159`), calls `wp_spa_pod_fixate` on
each entry (`:189`), and keeps an entry only when it carries more
channels than the entry already kept (`:209`):

    if (self->raw_format.channels < raw_format.channels) {
      self->raw_format = raw_format;

Fixating a `SPA_CHOICE_Range` keeps its default value, which
`add_channels` put at the maximum. So a node declared without
`audio.channels` settles at the highest channel count the PCM device
reports.

**Omitting the count alone can leave the positions empty.** In the same
else branch, `add_channels` emits `SPA_FORMAT_AUDIO_position` only when
`min` and `max` are equal (`alsa-pcm.c:1662` to `1681`). It takes the
positions from `default_pos`, which is what `audio.position` fills, when
that map has as many entries as the channel count, and otherwise from
the built-in `default_map` table at `alsa-pcm.c:1440`, which covers 1
through 8 channels and gives `FL, FR, RL, RR, FC, LFE, SL, SR` at eight.
A device whose `min` and `max` differ reports a channel count with no
positions at all. The declared `audio.position` does not repair that,
because the position is emitted only on the equal branch and only when
its length matches the count that branch settled on. WirePlumber handles
that case rather than rejecting it: when the format carries no
`SPA_FORMAT_AUDIO_position` array, it sets `SPA_AUDIO_FLAG_UNPOSITIONED`
on the format (`module-si-audio-adapter.c:200` to `202`).

This is what nobody has run: the operator's own drop-in with both
properties removed, on a real card, with `pw-dump` showing what the node
settled on. The source says what the code does. It does not say what
`snd_pcm_hw_params_get_channels_max` returns for an HDA HDMI PCM device
with nothing plugged in, and that number is what the whole path turns
on.

The documentation names no number for either property. In
`doc/dox/config/pipewire-props.7.md` at tag 1.6.8, the ALSA node section
gives `audio.channels` as "The number of audio channels to open the
device with. Defaults depends on the profile of the device." (line 957),
and `audio.position` as "The audio position of the channels in the
device. This is auto detected based on the profile." (line 966). Both
sentences describe the card profile path, which builds sinks from a
card's profiles and ports. This pod does not run that path. The ALSA
monitor is off, so the profile code never loads, and the mechanism above
is the only one in play.

## Candidate one: omit both properties

The change is to delete two lines from `sinkObject`. The node then
carries whatever channel count the PCM device reports, and the count
follows the card rather than the cable, which is the same rule the
declared node set already follows.

What it costs: a channel count with no channel positions, on any device
whose reported minimum and maximum differ. WirePlumber marks that format
`SPA_AUDIO_FLAG_UNPOSITIONED` rather than rejecting it, so the node is
usable, and an unpositioned eight-channel sink is a sink whose channels
name no speakers. What a client does with one, and what WirePlumber's
own linking policy does with one, is not recorded anywhere in this
repository and nobody has measured it.

`api.alsa.use-chmap` is the paired candidate. With it set to `true`,
`add_channels` takes the chmap branch at `alsa-pcm.c:1618`, queries the
driver with `snd_pcm_query_chmaps`, and emits both the count and the
positions from the driver's own map. Whether the HDA driver answers
`snd_pcm_query_chmaps` for an HDMI PCM device, and what it answers when
no monitor is connected, is unverified.

## Candidate two: declare the ELD's count

The operator reads the ELD before it starts the daemons, so it can write
the count the monitor reported at that moment. `lpcmChannels` is already
that number.

This reintroduces exactly the failure the current comment names. A
monitor plugged in after the daemons start leaves a node declared for
the wrong layout. A monitor swapped for a different model does the same.
PipeWire reads `context.objects` once, while it loads its configuration,
so the declared set is fixed for the life of the daemon and no later
read corrects it.

The operator already pays that price once, for a different fact. When
the card's playback PCM devices change, `reconcile` in `main.go:286`
compares the document it would write now against the one it wrote at
start, and returns an error so the kubelet restarts the pod:

    the card's playback PCM devices have changed since PipeWire started,
    so the new set has no nodes; restarting to declare them

That restart is bounded, because a card's PCM devices are fixed when its
driver binds. A channel count taken from the ELD is not bounded the same
way, because it changes with a cable. So the open question is whether a
changed channel count should cost the same restart, and a restart takes
the socket away from every consumer on the machine.

Two shapes exist for that. One restarts on any ELD channel-count change,
which makes a plugged cable a pod restart. The other declares the ELD's
count at start and never restarts for it, which leaves a node declared
for a monitor that is no longer there. Neither has been chosen.

## What a consumer gets today

It is downmixed. It is not blocked. On 2026-08-17 an mpv playing an
8-channel E-AC-3 track into one of these stereo sinks reported

    AO: [pipewire] 48000Hz 7.1 8ch floatp

and played correctly. PipeWire's audioconvert channelmix took the eight
channels down to two. `pipewire-props(7)` documents the channelmix
settings that control it, including `channelmix.mix-lfe`,
`channelmix.upmix`, `channelmix.lfe-cutoff`, and `channelmix.fc-cutoff`.

So this is a fidelity question and not a functionality question, and
that is what sets its urgency. No workload fails because of it. A
workload that plays 5.1 content into a 5.1 receiver gets a stereo fold
of it.

## What no hardware here can settle

There is no multichannel audio device on any machine in this fleet. A
test needs one device that reports more than two LPCM channels in its
ELD: an AV receiver, a soundbar with an HDMI input, or a television that
accepts 5.1. Three measurements need that device.

* **Whether a node declared without `audio.channels` settles above two.**
  Remove the two properties, restart the pod, and read the node's
  `Format` and its port list out of `pw-dump`. The source says it takes
  the device's maximum. Nothing on this hardware has shown that it does.
* **Whether the ELD's count matches what the device accepts.** The
  operator publishes `lpcmChannels` from the highest LPCM short audio
  descriptor in the block. Whether that number matches what
  `snd_pcm_hw_params_get_channels_max` reports for the same PCM device
  is unmeasured, and the two come from different places: one from the
  monitor over the DDC line, the other from the driver.
* **Whether a layout past stereo survives the HDMI path.** Play a
  channel identification track and confirm each channel arrives at the
  speaker its position names. The HDA driver, the ELD's speaker
  allocation, and the receiver's own decoding all sit between the node
  and the speaker, and none of them has been exercised here.

## Passthrough is a separate question

Compressed formats over HDMI, such as E-AC-3, DTS, or TrueHD, do not
travel the multichannel LPCM path. They travel the IEC958 path, and the
current declaration forecloses it.

`enum_iec958_formats` in `alsa-pcm.c:1902` returns 0 and offers no
compressed format unless `state->is_iec958` or `state->is_hdmi` is set.
Those two are set at `alsa-pcm.c:1051` and `alsa-pcm.c:1052`, at 1.4.11
`alsa-pcm.c:1038` and `alsa-pcm.c:1039`:

    state->is_iec958 = spa_strstartswith(state->props.device, "iec958");
    state->is_hdmi = spa_strstartswith(state->props.device, "hdmi");

`state->props.device` is whatever `api.alsa.path` carried. This operator
writes `hw:0,3`, which starts with neither string, so both flags stay
false on every node it declares. Setting `iec958.codecs` on the object
changes nothing while that is true, because the codec list is read only
after the flag test.

So the current design does foreclose passthrough, and it forecloses it
through the path form rather than through the missing codec list. What
an ALSA path of the form `hdmi:CARD=0,DEV=3` opens, whether it resolves
without the ALSA configuration a card profile normally supplies, and
whether it reaches the same PCM device the claim delivers, is all
untried. That is its own question and it does not belong inside this
one.
