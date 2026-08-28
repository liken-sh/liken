# 07, Sinks and sources

The operator publishes an output as a device in a `ResourceSlice`
and nothing else. A claim selects on the attributes, holds the
device alone, and every setting the output has is stored in the
memory of the pod that holds it. This plan adds two cluster-scoped
resources under `audio.liken.sh/v1alpha1`: a `Sink` for every
playback endpoint and a `Source` for every capture endpoint. Each
one has a `status` that reports what the hardware declares and what
the operator last observed, and a `spec` that declares the settings
the endpoint rests at. The device name changes at the same time,
from a card number the kernel assigned this boot to a name built
from the hardware's own identity, and the attributes take the
`sink` and `source` words.

The precedent is the display operator's
[plan 08](https://github.com/liken-sh/display-operator/blob/main/plans/completed/08-a-display-for-every-panel.md),
which gave every monitor a `Display`. This plan follows its shape
and cites it where the rule is the same.

## The problem

Three things cannot be done today, and each one is a house-sized
need rather than a player-sized one.

**Nothing but the holder can touch a sink.** A claim is exclusive.
While a player holds the television's speakers, no other pod can
mute them, and no rule can lower every speaker in the house at
night. The only channel to a sink's state is the PipeWire socket a
prepared claim delivers, and only the holder has it.

**A sink's state dies with its holder.** WirePlumber's persistent
storage is off in this pod, so every node starts at the default and
keeps whatever the last holder wrote. A player that set a Bluetooth
speaker to half volume and then restarted leaves the speaker there,
and the next holder inherits it. `volume.go` sets every delivered
sink to unity for the same reason, and unity is the one value the
operator can defend without a declaration to read.

**The card's own controls are invisible.** The USB DAC on `liken-1`
has a `PCM Playback Volume` in its own hardware, and a Realtek codec
has `Master Playback Volume`, `Headphone Playback Switch`, and
`Auto-Mute Mode`. Nothing in this pod reads or writes them. The
declared `adapter` nodes carry no ACP device, so PipeWire in this
pod never opens the mixer (`spa/plugins/alsa/alsa-pcm.c`, 1.4.2:
the PCM node opens the control device only for the pitch element
and `api.alsa.bind-ctls`). Every volume PipeWire applies here is a
software gain in `audioconvert`, and the hardware stays wherever
ALSA left it at boot.

A fourth fact makes the first three worse. The device name is
`card0-pcm3`, and `names.go` says plainly that the number is the
kernel's enumeration order and survives neither a machine change
nor a second card. A declaration keyed to that name would follow
the wrong hardware the first time a USB DAC registers ahead of the
onboard card.

Microphones are the same problem with a sharper edge. The operator
publishes playback PCMs only, so the capture half of the USB DAC on
`liken-1` is not in the cluster at all. When it is, the resting
state that matters most is `mute`, and the second actor is obvious:
a rule that closes every microphone in the building, and a person
who wants one `kubectl get sources` to show which ones are open.

## The design

### The cut

A pod that plays or records holds the device through a claim,
exactly as today, and keeps its own stream volume. Everything else
about the endpoint, what it declares and what it rests at, goes
through the `Sink` or the `Source`, and RBAC decides who may write
it. The claim carries the sound, and the resource carries the
settings. The claim stays exclusive, because
one holder for one piece of hardware is the clearest thing a claim
can mean, and the open problem on sharing is unchanged by this
plan.

### The endpoints this covers

An endpoint is one PipeWire node: a playback PCM, a capture PCM, or
a Bluetooth transport. Every endpoint gets the same object shape,
and the table says where each kind's fields come from.

| Endpoint | Kind | `volume` and `mute` | `controls` | `Connected` |
| --- | --- | --- | --- | --- |
| analog jack: headphones, line out, speakers | `Sink` | node `Props` | `Master`, `Headphone`, `Speaker` volumes and switches, `Auto-Mute Mode` | the jack control |
| analog input: microphone, line in | `Source` | node `Props` | `Capture Volume`, `Mic Boost Volume`, `Input Source` | the jack control |
| HDMI or DisplayPort | `Sink` | node `Props` | `IEC958 Playback Switch` | the ELD |
| USB card, either direction | `Sink` or `Source` | node `Props` | whatever the card's feature units declare | always |
| Bluetooth speaker or headphones | `Sink` | the device's `Route`, which is AVRCP volume when the peer supports it | none | the transport |
| Bluetooth headset microphone | `Source` | the device's `Route` | none | the transport |

Two rows need a word. An HDA card serves several input jacks
through one capture PCM and picks the live jack with `Input Source`,
so a `Source` is the capture PCM and the jack it listens to is a
control, not a second object. And an HDMI input exists only as a
capture card, which is a USB `Source` like any other.

The last row is the one this plan designs and does not build. Its
own section below says why.

### One resource per endpoint

The operator creates a cluster-scoped `Sink` for every playback
endpoint it publishes, and a `Source` for every capture endpoint,
named by the device name. The operator owns the object, creates it,
and writes all of `status`. Hardware belongs to a machine and not to
a tenant, so the resource is cluster-scoped, like a `Node` and like
a `Display`.

The two kinds have the same shape. They are two kinds and not one
kind with a direction field because `kubectl get sinks` and
`kubectl get sources` answer two different questions, and because a
`Role` can grant one without the other.

### The name

A device name is built from the hardware's own identity, in the
same three forms udev's `60-persistent-alsa.rules` encodes into
`/dev/snd/by-id` and `/dev/snd/by-path` (systemd v259). `liken`
runs no udev, so the operator reads the same sysfs attributes
itself, the way `liken` builds `path_id` for disks.

| Endpoint | Identity | Example |
| --- | --- | --- |
| onboard PCI card | node, PCI address, PCM id | `liken-1-pci-0000-00-1f-3-hdmi-0` |
| USB card with a serial | vendor, product, serial, PCM id | `usb-0573-1573-a34004801402-usb-audio` |
| USB card with no serial | node, USB port path, PCM id | `liken-1-usb-1-6-usb-audio` |
| Bluetooth speaker | address | `7c-66-ef-01-23-45`, unchanged |

The PCM id is the driver's own name for the PCM device (`HDMI 0`,
`USB Audio`), read with `SNDRV_CTL_IOCTL_PCM_INFO` on the control
device and lowercased with dashes. It is the same `id` that
`/proc/asound/card<N>/pcm<D>p/info` prints, and the ioctl is used
because the container runtime masks `/proc/asound` with an empty
tmpfs. It is the driver's
name for the endpoint and not its device number, and on HDA it is
stable per codec: the HDMI codec numbers its converters in order
and the device slots for each PCM type are a fixed table
(`sound/hda/common/codec.c`, v7.2, `get_empty_pcm_device`). The
sysfs source for each part: `/sys/class/sound/card<N>/device`
resolves to the PCI function or the USB interface, and the USB
device above it carries `idVendor`, `idProduct`, and `serial`.

A card that serves both directions through one PCM, such as the USB
DAC on `liken-1`, states the same id for both, and two devices of
one pool cannot share a name. So a capture endpoint's name ends in
`-capture`, and a playback endpoint's name carries no suffix:
`usb-0573-1573-a34004801402-usb-audio` plays and
`usb-0573-1573-a34004801402-usb-audio-capture` records.

The onboard and serial-less forms carry the node name because a
PCI address repeats on every machine of the same model, and the
resource is cluster-scoped. The serial form carries no node because
the dongle keeps its identity when it moves, and `status.node`
reports where it is now. A serial-less dongle moved to another port
becomes a new object, and its declared spec stays on the old one.
That is the trade udev made, and this plan takes it.

One limit to state in the schema description. On an Intel HDMI
codec, a pin binds to the first free PCM slot when a monitor
appears (`sound/hda/codecs/hdmi/hdmi.c`, v7.2,
`hdmi_attach_hda_pcm`). So `hdmi-0` names the card's first HDMI
slot and not a physical port, and on a card with two monitors the
slot each one lands in can change between plug events. The monitor
each slot feeds is what `status.monitor` reports, and a machine
with one HDMI monitor never sees the difference.

The name is a DNS label, so it is at most 63 characters. A node name
long enough to break that is refused at publish time with the
reason in the operator's log, because a silently shortened name is
a name nobody can predict.

The rename is a breaking change for a claim that names a device. An
allocation records the device name, and the DRA plugin resolves the
name back to the endpoint at prepare time, so a claim allocated
under the old name fails to prepare after the rename. The manual
says so, and a deployment recreates its claims once.

### The attributes

The `output` attribute becomes `sink` on a `Sink` and `source` on a
`Source`, each carrying the device name. A `DeviceClass` selects a
direction by the presence of the attribute. The manual gives the
YAML for two consumer classes, `audio-sink` in place of
`audio-output` and `audio-source` beside it, and the operator ships
neither, by the rule that a consumer's vocabulary belongs to the
cluster owner. The other attributes keep their names. `card` and
`pcm` stay, as the numbers a consumer needs to open the node's
`api.alsa.path`, and the slice description says they are this
boot's numbers.

### What status reports

- `node`, `card` (`{id, driver, longname}`), and `pcm`
  (`{device, id}`), so a reader can place the endpoint. `card.id` is
  the kernel's short id (`PCH`, `HID`) with the `_1` suffix the
  kernel appends on a clash.
- `location`: the PCI address or the USB path the name is built
  from, in the kernel's spelling.
- `connectionType`: `hdmi`, `displayport`, `analog`, `usb`, or
  `bluetooth`.
- `monitor`, on an HDMI or DisplayPort sink: the `Display` name the
  ELD identifies, with `manufacturer`, `product`, and `name`, the
  same values the pairing attribute is built from. A slot with no
  monitor reports none.
- `bluetooth`, on a speaker: `address`, `name`, `codec`, and
  `codecs`, the values the slice carries today.
- `nodeName`: the PipeWire node a consumer's streams target, absent
  while PipeWire holds none.
- `capabilities`: the controls the card declares for this endpoint,
  keyed by the kernel's own control name. Each entry carries the
  type (`boolean`, `integer`, `enumerated`), the range and step for
  an integer, the dB range from TLV when the control has one, and
  the values for an enumerated control. An HDMI PCM declares only
  `IEC958 Playback Switch` and `IEC958 Playback Default`; there is
  no volume element on an HDMI PCM (`hdmi.c`, v7.2,
  `snd_hda_hdmi_generic_build_controls`). Only `IEC958 Playback
  Switch` becomes a capability; the `Default` beside it is a
  status-bits block.
- `format`: the rate, channel count, and channel positions the node
  runs at, from its `Format` param.
- `observed`: the last value the operator read for `volume`,
  `mute`, `codec`, and each control in `capabilities`.
- `claim`: the namespace and name of the claim that holds the
  endpoint now, from the prepared claims the plugin already tracks.
  It is the answer to "who has the kitchen speaker", and it is empty
  between holders.
- Conditions. `Connected` reports that the endpoint can play or
  record now: a monitor on an HDMI slot, a plug in an analog jack, a
  connected speaker, always true on USB. `Ready` reports that
  PipeWire holds the node. The two map onto the `no-monitor` and
  `no-sink` taints, and the taints stay, because the scheduler reads
  taints and a person reads conditions.

### How controls attach to an endpoint

ALSA mixer controls belong to a card, and a card can publish several
endpoints. The rule that assigns them:

- A control whose name carries `Playback` goes to the card's
  analog and USB sinks, and so does a control that names no
  direction, such as `Auto-Mute Mode`, because it is the card's. A
  control whose name carries `Capture`, and `Input Source` and `Mic
  Boost`, go to its sources.
- `IEC958 Playback Switch` goes to HDMI sinks by ordinal: the
  element with index 0 to the first HDMI slot, index 1 to the
  second. It is the only control an HDMI slot lists. The
  `Default` element beside it is a status-bits block, not a value a
  spec can state.
- Only the mixer interface is read. The `Playback Channel Map` a
  card declares on the PCM interface carries the word and is not a
  control a person sets.
- A card with two analog playback endpoints publishes the same
  `Playback` controls on both. The operator writes a control only
  when a spec states it, so the duplication costs nothing until two
  specs disagree, and the schema description says which one wins:
  the object whose write came last, because the hardware has one
  register.

A jack control (`Headphone Jack`, `HDMI/DP,pcm=3 Jack`) is not a
capability. It is read-only and feeds the `Connected` condition.

### What spec declares

Every field is optional, and the operator writes a declared field
back when the endpoint diverges from it, by the same
write-on-divergence rule the `Display` follows. An empty spec writes
nothing, per the parameters-only rule.

| Field | Where it lands | When it applies |
| --- | --- | --- |
| `volume`, 0 to 100 | the gain PipeWire applies: `channelVolumes` on the node for an ALSA endpoint, the `Route` on the device for a Bluetooth speaker | at once, under a claim or not |
| `mute` | the same two places | at once |
| `controls`, keyed like `capabilities` | the card's own registers, through `SNDRV_CTL_IOCTL_ELEM_WRITE` | at once |
| `format` (`rate`, `channels`, `positions`) | the node's `Props.params`, the runtime path for `audio.rate` and its siblings | at the node's next start |
| `codec`, on a speaker | `bluetoothAudioCodec` on the device, the write `codecs.go` makes today | when no claim holds the speaker |

`volume` is an integer percent of unity and applies the same gain to
every channel. Per-channel levels are not declared, because no
consumer has asked for them.

The three timing rows are three facts from the sources. A `Props`
write of `channelVolumes` or `mute` on an `adapter` node applies at
once (`spa/plugins/audioconvert/audioconvert.c`, 1.4.2). The same
keys written as `Props.params` reach `alsa_set_param`, the function
init uses, but the adapter refuses a `Format` change while started
and negotiates on the next `Start`, so a format change waits for the
node to suspend (`audioadapter.c`, 1.4.2). A codec switch removes
the node, releases the transport, and asks BlueZ for a new one
(`bluez5-device.c`, 1.4.2, `set_profile`), so it always interrupts
playback, and the resting codec waits for the claim to end, the way
`Display.spec.mode` waits for the claim that holds the screen. A
claim's own `codec` parameter wins while the claim holds the
speaker.

Two Bluetooth facts settle where `volume` goes on a speaker. A
`channelVolumes` write on the speaker's node changes the software
gain only; the A2DP follower accepts no volume key
(`spa/plugins/bluez5/media-sink.c`, 1.4.2). A write to the device's
`Route` calls `spa_bt_transport_set_volume`, which writes
`Volume` on `org.bluez.MediaTransport1`, and that is AVRCP absolute
volume on the speaker (`bluez5-device.c` and `bluez5-dbus.c`,
1.4.2). So the operator writes the `Route`, and the speaker's own
number is what moves. A speaker with no absolute volume gets the
software gain instead, and the `Route` reports no `volumeStep`.
`volume.go`'s unity write moves to the same path and becomes the
default the operator applies to a sink whose spec declares no
`volume`.

### Events, not polls

`observed` follows two event sources, and the operator reads no
timer.

The card's control device delivers a 72-byte `snd_ctl_event` on
`read()` after `SNDRV_CTL_IOCTL_SUBSCRIBE_EVENTS`, for every control
write from any process, every jack change, every ELD change, and a
knob turned on a USB DAC, whose mixer keeps an interrupt endpoint
open for that purpose (`sound/core/control.c` and
`sound/usb/mixer.c`, v7.2). `jacks.go` already polls an input node
the same way, with a non-blocking `os.NewFile`; the control fd joins
that loop, and the `MASK_VALUE` bit names the control that changed.

PipeWire's graph delivers `pw-dump -m`, which prints one JSON array
per batch of changes, each element the whole changed object in the
same shape a plain `pw-dump` prints, and `{ "id": N, "info": null }`
for a removal (`src/tools/pw-dump.c`, 1.4.2). A node's `Props`
change and a Bluetooth device's `Route` change both arrive there. A
speaker whose own volume moved reports it through BlueZ, PipeWire
debounces it for 200 ms, and both the `Route` and the node's
`Props` update. The reconcile pass that reads the graph today keeps
running on every event, and the `-m` stream is what wakes it in
place of the per-pass `pw-dump` run.

A write the operator did not make and the spec does not cover
lands in `observed` and nowhere else. A write the spec does cover
is divergence, and the operator writes the declaration back.

One gap in the graph's events was found on the first drill. A
`Props` write on a suspended `adapter` node is applied and kept,
and it is the level the node runs at when it starts, but PipeWire
1.4.2 never announces it: the adapter compares a parameter's flags
and not its serial (`audioadapter.c`, `convert_node_info`), so
`pw-dump` and the monitor stream serve a stale copy while the node
is suspended. So an idle endpoint reports the level the operator
last wrote as `observed`, and the graph takes over once the node
runs. A `PortConfig` write would flush the cache, and it is set
aside because it re-derives the format and turned a stereo HDMI
node into eight ports on the drill.

The same drill found that the container runtime masks
`/proc/asound` with an empty tmpfs, so the PCM id is read with
`SNDRV_CTL_IOCTL_PCM_INFO` on the control device, which answers the
same id the proc file prints.

### No override layer

`Display.spec.override` exists because the idle sidecar needs a
temporary state above the resting one, with a capture and a
restore. No audio consumer needs that today, so this plan adds no
override and the schema leaves room for one.

### Sources

The declare pass walks capture PCMs beside playback PCMs and
declares an `adapter` node with `factory.name = api.alsa.pcm.source`
for each. The source node accepts the same `Props` as a sink
(`spa/plugins/alsa/alsa-pcm-source.c`, 1.4.2), so `volume`, `mute`,
and `format` work the same way. A prepared claim on a `Source`
delivers the same socket and the same two variables, with
`PIPEWIRE_NODE` naming the source node, which `target.object` honors
for a capture stream as it does for playback.

### Bluetooth sources

A Bluetooth microphone is a headset microphone, and a headset's
microphone comes over HFP or HSP, not A2DP. The pod registers the
`a2dp_source` role alone and `bluez5.hfphsp-backend = "none"`, for
the reason `bluez.go` states: the headset profiles carry voice over
an SCO socket, and an SCO socket opens in the host's network
namespace, which this pod does not have. The Bluetooth operator's
pod does run on the host network, and that is what lets it own the
radio.

So a Bluetooth `Source` needs three changes, and this plan names
them without building them:

- The audio pod, or the PipeWire container alone, on the host
  network.
- `hfp_ag` added to `bluez5.roles`, which makes this machine the
  audio gateway and a headset the hands-free unit, and
  `bluez5.hfphsp-backend = "native"`, PipeWire's own backend that
  needs no oFono.
- A rule for the profile switch. Classic Bluetooth cannot run A2DP
  and HFP on one headset at the same time, so the moment a claim
  opens the microphone, the same headset's `Sink` drops to HFP
  quality, 16 kHz mSBC on most headsets, and returns when the claim
ends.
  WirePlumber's `bluetooth.autoswitch-to-headset-profile` does that
  switch on a desktop, and the pod turns it off. The operator would
  do it on the claim instead, and the `Sink`'s `status.format` would
  say so while it lasts.

None of the three changes the `Source` object. The shape is the
same, `volume` and `mute` land on the device's `Route` as they do
for a speaker, and `controls` is empty because a transport declares
none. LE Audio removes the profile-switch limit, because a BAP
headset carries both directions at once, and PipeWire 1.4 has
`bap_sink` and `bap_source` roles for it. Whether the lab's radios
and kernel support it is not verified in this plan.

### What retires

- `card0-pcm3` names and `outputFromDeviceName` retire, replaced by
  the identity derivation above and its inverse. The pairing
  identity `monitor.liken.sh/id` is unchanged.
- The `output` attribute retires in favor of `sink` and `source`.
- The per-pass `pw-dump` run retires in favor of the `-m` stream.
- The unity write in `volume.go` becomes the default for an
  undeclared `volume`, on the `Route` for a speaker.

## What was considered and set aside

**One kind with a direction field.** Set aside above. Two kinds
read better and grant separately.

**Profiles and ports, as PipeWire's ACP device offers them.** Set
aside because this pod has no ACP device. `nodes.go` declares one
`adapter` per PCM so that no udev is needed, and a declared node has
no `Profile` or `Route`. The card's own controls, read through the
control interface the operator already opens, are the honest
surface for what the hardware can do.

**A `Card` resource that holds the mixer controls.** Set aside
because a person declares state on the thing they hear, and that is
an endpoint. The controls-attach rule above puts a card's controls
on its endpoints instead, and names the one case where two objects
share a register.

**PipeWire as the mixer reader, through `api.alsa.bind-ctls`.** The
PCM node can bind named controls as `api.alsa.bind-ctl.<name>` props
and subscribe to their changes, which would fold the mixer into the
graph read. Set aside because the control names are known only
after the operator enumerates the card, which is after the node is
declared, and because whether a bound control accepts a write
through `Props` is not verified. The control interface is one ioctl
away and the operator already speaks it.

**Naming an HDMI sink by the monitor it feeds.** Set aside because
the monitor moves and the slot does not. `status.monitor` links the
two, and the pairing attribute already lets a claim ask for a
monitor's speakers by the monitor.

**An override layer now.** Set aside above, until a writer needs
one.

**Per-channel volumes in spec.** Set aside until a consumer asks.

## How the work is proved

On `liken-1` and `stick-1`, with the USB DAC on `liken-1`:

- `kubectl get sinks` lists every HDMI slot on both machines, the
  DAC by its serial, and the LG's speakers by address, and each
  `status.capabilities` matches what `amixer` would list for the
  card.
- `kubectl get sources` lists the DAC's capture endpoint, and a pod
  that claims it records through `PIPEWIRE_NODE`.
- Declaring `spec.volume: 50` on the LG's `Sink` moves the volume
  the speaker itself displays, and the change reaches
  `status.observed` from the `Route` without a poll.
- Turning the DAC's own knob changes `status.observed.controls` in
  under a second, and declaring `spec.controls` for `PCM Playback
  Volume` writes it back on the next turn.
- Declaring `spec.mute: true` on an HDMI sink while a player holds
  it silences the television, and the player keeps playing.
- Declaring `spec.codec` on a held speaker changes nothing until
  the claim ends, and switches then.
- Rebooting both machines keeps every name, and moving the DAC to
  `stick-1` keeps its `Sink` with `status.node` changed.
- Deleting the operator pod and letting it return leaves every
  declared value in place, because the declaration is on the
  object and not in the pod.

The analog controls on `stick-1`'s Realtek codec are proved when
the `liken` image ships the codec's driver module. Today the card
reports `Realtek ID 269`, the generic driver's name, and publishes
no analog PCM.
