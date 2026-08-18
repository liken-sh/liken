# The audio operator

Milestone 59. Completed. Each physical audio output publishes as its
own DRA device, so a pod claims one HDMI output's speakers or the
analog jack, and receives the PipeWire socket and the name of the sink
its streams must reach. It is the third instance of milestone
56's pattern: the operator claims the machine's audio controller
through an ordinary `liken.sh` claim, runs PipeWire, and publishes what
PipeWire holds under `audio.liken.sh`.

## The problem

liken publishes the audio controller as one device. The policy is in
`machine-operator/publishing.go`: `publishAudio` returns a single
device with subsystem `sound`, delivering the controller's whole
subtree, which is the ALSA control node, the hardware-dependent nodes,
one node for each PCM subdevice, and the jack input nodes beside them.
That is the right thing for the OS to publish, because the OS can read
the nodes and nothing else.

What the nodes do not say is which output is which. An Intel N95 with
two monitors publishes one audio device that delivers `pcmC0D3p`,
`pcmC0D7p`, `pcmC0D8p`, and an analog PCM, and nothing in that list
says which one plays into the kitchen monitor's speakers. A workload
that must play into one monitor's speakers has one way to get them
today, and it is wrong: claim the controller and open a PCM by
number. The claim is exclusive, so the pod takes every output on the
card to play through one. It must find the right device number
itself, and the number it finds on one machine names different
hardware on the next one. Nothing stops it from opening the wrong
output.

One fact governs the design: which PCM plays into which monitor is a
fact that only a running daemon reads. It comes from the ELD, the
EDID-Like Data block that the graphics driver writes into the audio
driver when a monitor is connected. liken's inventory walk does not
read it, and a walk that did would report a fact that changes whenever
somebody moves a cable.

## What the pod runs and what it holds

The pod runs PipeWire, with WirePlumber for session management.
PipeWire is to audio what Weston is to displays: the userspace daemon
that owns the hardware and holds the facts the OS cannot read.

PipeWire reads the ELD through the ALSA control interface. Its ALSA
card profile code finds the control named `ELD` for a PCM device
(`spa/plugins/alsa/acp/acp.c`, `pa_alsa_mixer_find_pcm(mixer_handle,
"ELD", device)`), parses it (`pa_alsa_get_hdmi_eld` in
`spa/plugins/alsa/acp/alsa-util.c`), and copies the monitor name onto
the port as `device.product.name`. It takes the channel count, the
speaker allocation, and the IEC958 codec list from the same block. It
subscribes to that control, so an ELD that changes calls
`hdmi_eld_changed`, and a monitor that arrives or leaves is an event,
not a poll.

PipeWire keeps only part of the block. Its `pa_hdmi_eld` struct holds
`monitor_name`, `speakers`, `iec958_codecs`, and `lpcm_channels`
(`spa/plugins/alsa/acp/alsa-util.h`). The manufacturer and product
codes are in the ELD and not in that struct, so the operator reads the
raw `ELD` control itself for them. The same bytes are readable as text
at `/proc/asound/card<N>/eld#<codec>.<pin>`, which
`sound/pci/hda/patch_hdmi.c` creates with `snprintf(name, sizeof(name),
"eld#%d.%d", codec->addr, index)` where `index` is the pin index. The
proc file is for a person to read. The control element is what the
operator reads, because `snd_hctl_elem_get_device` gives the PCM device
number the block belongs to, and the proc file's second number is a pin
index instead.

None of that card profile code runs in this pod. It belongs to the ACP
path, which only the ALSA monitor builds, and the monitor needs udev.
So the operator declares raw PCM sink nodes and reads the ELD control
itself. The section on finding the card with no udev states why, and
what it costs. The operator's own jack watcher is what makes a monitor
an event rather than a poll.

The privilege is none. The operator declares no `hostNetwork` and adds
no capability. Everything it does to the hardware, it does through the
ALSA nodes its claim delivers, plus the two hostPath mounts every DRA driver
takes, which milestone 56 lists.

## Finding the card with no udev

A liken machine runs no udevd, and WirePlumber's ALSA monitor
enumerates cards through libudev. The monitor asks udev for the sound
subsystem, gets nothing, builds no PipeWire device, and creates no
sink. PipeWire then holds a graph with no ALSA node in it on a machine
whose speakers work, every output the operator publishes has the
no-sink taint, and no pod can play. WirePlumber's own documentation
states the dependency: "The plugin then monitors UDev and creates
device and node objects for all the ALSA cards that are available on
the system." There is no non-udev path through that monitor.

The operator does not need the monitor, because it already enumerates
the card without udev. `readOutputs` lists the playback PCM devices
from the nodes the claim delivers, and reads each one's ELD through
the ALSA control interface. So the operator writes the nodes down for
PipeWire instead of asking it to discover them. Before it starts the
daemons, it generates
`/etc/pipewire/pipewire.conf.d/60-liken-outputs.conf` with one
`context.objects` entry for each playback PCM device:

    context.objects = [
      { "factory": "adapter",
        "flags": [ "nofail" ],
        "args": {
          "factory.name": "api.alsa.pcm.sink",
          "api.alsa.path": "hw:0,3",
          "api.alsa.pcm.card": "0",
          "media.class": "Audio/Sink",
          "node.name": "liken.audio.card0-pcm3",
          "audio.channels": "2",
          "audio.position": "FL,FR",
          "liken.audio.card": "0",
          "liken.audio.pcm": "3"
        }
      }
    ]

The mechanism was verified against the PipeWire sources at 1.4.11 and
at master.

* **It is the supported path.** `pipewire-props(7)` states it: "In
  minimal PipeWire setups without a session manager, the device
  properties can be configured via context.objects in pipewire.conf(5)
  when creating the devices." The shipped `src/daemon/pipewire.conf.in`
  includes the same object as a commented example, with
  `api.alsa.pcm.source` in place of the sink.
* **The factory exists under that name.** `spa_alsa_sink_factory` in
  `spa/plugins/alsa/alsa-pcm-sink.c` registers
  `SPA_NAME_API_ALSA_PCM_SINK`, which is `api.alsa.pcm.sink`.
* **Nothing in the path uses udev.** `spa_alsa_init` in
  `spa/plugins/alsa/alsa-pcm.c` reads `api.alsa.path` and
  `api.alsa.pcm.card` and opens the device with `snd_pcm_open`.
* **The drop-in adds and does not replace.** `pipewire.conf(5)` says a
  dictionary section merges key by key and an array section is
  appended. `context.objects` is an array, so the daemon's own dummy
  and freewheel drivers stay.
* **The operator's own properties survive to `pw-dump`.**
  `module-adapter.c` hands the whole `args` dictionary to
  `pw_adapter_new`, and `impl-node.c` sets `info.props` to the node's
  whole property dictionary, so `liken.audio.card` and
  `liken.audio.pcm` arrive in the dump. That is what maps a node back
  to its output. A node the udev monitor built has `alsa.card` and
  `alsa.device` instead, and this graph has no such node in it.
* **`media.class` has to be written.** The ALSA plugin keeps a media
  class inside the SPA node, and `module-adapter.c` copies no default
  onto the PipeWire node, so a node declared without it is a node that
  nothing treats as a sink.

WirePlumber stays, with `monitor.alsa` and `monitor.alsa-midi`
disabled in the pod's profile. It still links each client's stream to
the sink named in `PIPEWIRE_NODE`, which is the whole of what this
operator needs from a session manager. `hardware.audio` stays required:
WirePlumber's component list gives it `wants` on the two monitors and
not `requires`, so disabling them leaves the profile satisfied.

**The alternative, rejected: teach a session manager to enumerate
without udev.** The ALSA card profile device, `api.alsa.acp.device`,
uses no libudev either, and neither does `api.alsa.pcm.device`. Both
were read and both are dead ends from a configuration file.
`src/modules/spa/spa-device.c` installs no listener for a SPA device's
child objects, and `struct pw_device_events` in `src/pipewire/device.h`
has only `info` and `param`, so a device declared in
`context.objects` produces a Device global with profiles and routes on
it and zero nodes. A session manager reads a device's children only by
loading the SPA plugin in its own process, which is what WirePlumber's
`monitors/alsa.lua` does with `api.alsa.enum.udev`. Taking that route
means patching WirePlumber to enumerate from an ioctl scan instead. It
is the only route to hardware mixer volume, profile switching, and
port availability from jack detection, and it costs a fork of the
session manager. Raw PCM sinks with software volume need no patch at
all, and the operator reads jacks and ELD itself already.

**Every playback PCM device is declared, including an HDMI PCM with no
monitor on it.** PipeWire reads `context.objects` once, while it loads
its configuration, so the declared set is fixed for the life of the
daemon. A set that followed the cables would need a PipeWire restart
every time somebody moved one, and a restart ends every consumer's
session. The PCM devices a card has are fixed when its driver binds, so
a set that follows the card never moves. The node creation does not
depend on a monitor either: `impl_init` in `alsa-pcm-sink.c` ends at
`spa_alsa_init`, and `snd_pcm_open` runs later, on the `ParamBegin`
command, so the node exists in the graph whether the cable is in or
not.

The taints stay honest under that choice. An HDMI output with no
monitor now has a sink node, so the no-sink taint no longer fires for
it, and the operator taints it on the ELD instead, which is the fact
that says whether a monitor is there. The no-sink taint keeps its own
meaning: PipeWire holds no node for this PCM device, which is the one
output that `nofail` let fail while the rest of the card came up.

**A PCM device that appears or leaves is one restart.** Every reconcile
pass generates the document again and compares it against the one
PipeWire started with. A difference means the running graph cannot
serve the card any more, so the operator stops and the kubelet's
restart declares the new set. It does not taint on the way out, because
the restart takes the socket from every consumer for a few seconds and
a `NoExecute` taint would evict all of them for that gap. This costs
one restart for a card that arrived or left, and it never fires for a
monitor somebody plugged in.

## The controller claim

The operator acquires the audio controller through an ordinary
exclusive claim against liken's driver, which is milestone 56's
arbitration rule.

The exclusivity was a decision this milestone forced. liken published
the controller as shareable at first, because ALSA gives each PCM
subdevice to one opener and refuses a second open with `EBUSY`, so
two direct claims could play through different outputs of one card.
The sharing lost on two counts. In practice one sound server owns the
card and mixes every stream through it, so the shared case never
happens. And two claims sharing a card have no contract over which
claim gets which output, so the `EBUSY` arrived at play time, on
whichever pod opened second, instead of in the scheduler where a
person can see a claim wait. `publishAudio` in
`machine-operator/publishing.go` publishes the controller exclusively
now, the way the display device and the Bluetooth adapter publish,
and the operator's claim is the whole of the arbitration.

The delivery sets stay disjoint with no rule needed. liken delivers the
card's nodes, and the operator delivers no node at all, which the
delivery section states.

## One device for each output

The operator publishes its own ResourceSlices under `audio.liken.sh`,
with one device for each physical output that PipeWire drives: each
HDMI or DisplayPort PCM, and the analog jack.

The device name is the output's ALSA card and PCM device number, such
as `card0-pcm3`. The number comes from the codec's pin order, which the
driver enumerates the same way at every boot on the same hardware and
kernel. It is not stable across machines, and this document does not
claim it is stable across a kernel change, because that was not
verified. A claim that must survive either one selects on the
attributes instead.

The attributes come from the ELD and from PipeWire's node:

* the connection type, which is HDMI, DisplayPort, or analog,
* the monitor's manufacturer code and product code, for an HDMI output,
* the monitor name, which is the same EDID descriptor the display
  operator publishes as the model name,
* the maximum LPCM channel count and the speaker allocation,
* the PipeWire node name of the sink, which is what a consumer's
  environment names.

The ELD holds no serial number. The kernel prints the whole block at
`snd_hdmi_print_eld_info` in `sound/pci/hda/hda_eld.c`, and the fields
are `monitor_present`, `eld_valid`, `codec_pin_nid`, `codec_dev_id`,
`codec_cvt_nid`, `monitor_name`, `connection_type`, `eld_version`,
`edid_version`, `manufacture_id`, `product_id`, `port_id`,
`support_hdcp`, `support_ai`, `audio_sync_delay`, `speakers`,
`sad_count`, and the audio descriptors. There is no EDID serial among
them. So an audio device can say which model of monitor it plays into,
and it cannot say which unit. The display operator publishes the
serial, because it reads the whole EDID. The next section has to answer
that difference.

## Pairing a screen with its speakers

A pod that plays a video on the kitchen monitor needs that monitor and
that monitor's speakers. One ResourceClaim does that, with one request
against `display.liken.sh`, one request against `audio.liken.sh`, and a
`matchAttribute` constraint across the two.

The Kubernetes behavior was verified against the v1.36 sources.

* A claim holds a list of requests, and each request names its own
  DeviceClass. `DeviceClaim.Requests` is a list of `DeviceRequest`, and
  `DeviceClassName` is required on each one
  (`staging/src/k8s.io/api/resource/v1/types.go`, release-1.36). So one
  claim can hold a request against each driver.
* A constraint names the requests it applies to and requires that all
  devices in question have the same value for one attribute. The
  field's doc states "MatchAttribute requires that all devices in
  question have this attribute and that its type and value are the same
  across those devices", and "Must include the domain qualifier". Its
  type is `FullyQualifiedName`.
* The scheduler compares the attribute across devices without regard to
  which driver published them.
  `structured/internal/stable/allocator_stable.go` builds one
  `matchAttributeConstraint` for the constraint and calls `add` for
  every candidate device in every named request. `lookupAttribute`
  looks the fully qualified name up in the device's attribute map
  first, and falls back to the bare identifier only when the name's
  domain equals the device's own driver name.

The consequence for the two operators is a naming rule. A device
attribute written without a domain is assumed to be in the publishing
driver's domain, so the display operator's bare `monitorName` and the
audio operator's bare `monitorName` are two different fully qualified
names and never match. Both operators publish the pairing attribute
under one shared domain that neither driver owns, which the
`QualifiedName` documentation expects: names defined by a third party
must include the domain prefix. The proposal is `monitor.liken.sh/id`,
built from the manufacturer code, the product code, and the monitor
name, because those are the three facts the ELD and the EDID both
hold.

One limit comes with it. Two monitors of the same model produce the
same `monitor.liken.sh/id`, so the constraint is satisfied by either
pairing, and a claim can get one screen with the other screen's
speakers. The fix needs a value tied to the connector rather than to
the model. The ELD's `port_id` is the candidate, and whether it
corresponds to the DRM connector the display operator names was not
verified. The drill measures it on a machine with two identical
monitors.

## The delivery is a socket, not a device node

The delivery has the shape milestone 57 uses, not the shape milestone
58 uses. A consumer receives no `/dev/snd` node. What it needs is the
PipeWire socket and the name of the sink its streams must reach, and
CDI delivers both as a mount and environment variables.

The client mechanisms were verified against the PipeWire sources.

* **The socket.** A client resolves `PIPEWIRE_REMOTE` first, then the
  `remote.name` context property, then the default `pipewire-0`
  (`src/modules/module-protocol-native/local-socket.c`, `get_remote`).
  A name that starts with `/` is used as an absolute path, and the
  runtime directory is not consulted (`try_connect_name`). Otherwise
  the socket is looked for in `PIPEWIRE_RUNTIME_DIR`, then
  `XDG_RUNTIME_DIR`, then `USERPROFILE`, and last in `/run/pipewire`.
  So the CDI spec mounts the socket directory and sets one of the two,
  and an absolute `PIPEWIRE_REMOTE` is the simplest of the two forms.
* **The target sink.** `PIPEWIRE_NODE` sets `target.object` on every
  stream, in `src/pipewire/stream.c`:
  `pw_properties_set(stream->properties, PW_KEY_TARGET_OBJECT, str)`.
  The client documentation gives the example
  `PIPEWIRE_NODE=alsa_output.pci-0000_00_1b.0.analog-stereo aplay ...`
  (`doc/dox/config/pipewire-client.conf.5.md`). `target.object` takes a
  `node.name` or an `object.serial`
  (`doc/dox/config/pipewire-props.7.md`). The general form is
  `PIPEWIRE_PROPS`, which updates the whole property set of a stream or
  a filter (`src/pipewire/stream.c`, `src/pipewire/filter.c`), and
  `PIPEWIRE_NODE` is the one variable this delivery needs.

Both variables are read by clients that use PipeWire's own stream API.
A client that plays through the PulseAudio protocol or the ALSA
compatibility plugin selects its sink another way, and this document
does not state which, because that was not verified.

The consumer therefore holds no audio device node, and the operator
holds them all. That is the same trade the display operator makes: the
session is a connection to a daemon, and the daemon restarting ends it.

## A monitor that leaves

A monitor that somebody unplugs leaves its HDMI output with a sink it
cannot play through, and that is milestone 56's loss case with nothing
new in it. The sink node stays, because the operator declares it from
the card's PCM devices and not from the cables. The device stays
published, the operator applies a `NoExecute` taint to it, the
taint-eviction controller ends the consumer pod, and
`tolerationSeconds` on the claim sets how long an output may be silent
first. A monitor coming back clears the taint.

The loss signal is the ELD control, the same one that publishes the
identity. The block goes invalid when the monitor leaves, and the
control change is an event the operator already listens to. The jack
input nodes that liken delivers with the card report the same fact from
the kernel's side, and the ALSA control names them per PCM, in the form
`HDMI/DP,pcm=3 Jack` (`spa/plugins/alsa/acp/alsa-mixer.c`).

What PipeWire does with the sink node when a port goes unavailable
depends on the profile WirePlumber then selects. The operator must not
depend on the node vanishing, so it treats the ELD as the fact and the
node as a consequence. The drill measures which one happens.

## What a drill must show

The drill runs on `liken-1`, an Intel N95 with two HDMI monitors, which
is also where milestone 57's drill runs.

* **The Kustomization deploys it and deleting it removes it.** Before
  the apply, the machine publishes what it publishes today and no
  `audio.liken.sh` slice exists. Apply the published base, and the
  operator's pod starts on the machine with the audio controller, with
  no node selector written by hand. Delete it, and the published
  devices return to what they were.
* **The declared nodes exist and play.** `pw-dump` lists one
  `Audio/Sink` node for each playback PCM device, each one with
  `liken.audio.card` and `liken.audio.pcm`, on a machine with no udevd.
  A stream that names one of them by `PIPEWIRE_NODE` reaches the
  speakers. This is the whole of what the static declaration buys, and
  nothing off the hardware proves it.
* **An HDMI PCM with no monitor.** The node exists in `pw-dump`.
  Record what `snd_pcm_open` on `hw:N,D` does when nothing is plugged
  in: whether the node enumerates formats, and what it reports if a
  stream reaches it. The kernel's answer is driver-specific and it was
  not verified.
* **The declared format.** The nodes ask for two channels at `FL,FR`
  and name no rate or sample format. Record whether each output
  negotiates one, and record what a monitor that accepts more than two
  channels does, because the ELD that reports the channel count is
  readable only while the cable is in and the declaration is written
  once at start.
* **Each output publishes once.** One device for each HDMI PCM and one
  for the analog jack, each with its connection type, and each HDMI
  device with its monitor's manufacturer code, product code, and
  name.
* **A claim by name.** A pod that names one output plays into that
  monitor's speakers and no other.
* **A claim by attribute.** A pod that selects on the monitor name gets
  that monitor's output. A second pod that asks for the same one parks
  as Unschedulable while the first one runs.
* **The paired claim.** One claim with a `display.liken.sh` request, an
  `audio.liken.sh` request, and a `matchAttribute` on
  `monitor.liken.sh/id` allocates a screen and that screen's speakers.
  Moving the cables between the two monitors and repeating it must
  still pair them correctly.
* **The identical-monitor case.** With two monitors of the same model
  and the same `monitor.liken.sh/id`, record whether the constraint
  pairs a screen with the other screen's speakers, and record whether
  the ELD's `port_id` distinguishes the two outputs. The result sets
  whether the pairing attribute needs a connector-level value.
* **Unplug and replug.** With a `tolerationSeconds` of thirty, a five
  second unplug must not end the consumer pod, and an unplug past
  thirty seconds must end it. Replugging clears the taint and the
  consumer starts again, with no human step.
* **An operator restart while a claim is live.** Deleting the operator
  pod restarts PipeWire, so the consumer loses its socket and its
  streams. The drill records what the consumer does and what the
  operator does when it comes back, which is the same question
  milestone 57's drill asks about Weston.

## Open questions

These were the questions this milestone could not answer. Each one
below records what happened to it.

* **Siblings or one operator.** Answered: they stay siblings, and the
  drill supports it. Each operator ships and runs on its own, the
  pairing constraint works across the two drivers, and the display
  operator's compositor restarts without touching audio. The cost this
  milestone named is the one that arrived: a shared attribute domain,
  held together by parity test vectors in both repositories. Merging
  them would still remove the constraint. It would also put Weston and
  PipeWire in one restart domain, and a compositor restart already
  ends every session on every screen, so audio would join a restart
  domain it is currently outside.
* **Sharing semantics.** Still open, and it belongs to the operator
  now:
  [A sink can be shared and this one is not](https://github.com/liken-sh/audio-operator/blob/main/plans/open-problems/a-sink-can-be-shared-and-this-one-is-not.md).
  The shipped release is exclusive, as proposed.
* **The analog jack on a machine that uses none.** Still open, and it
  belongs to the operator now:
  [The analog jack publishes whether or not anything is plugged in](https://github.com/liken-sh/audio-operator/blob/main/plans/open-problems/the-analog-jack-publishes-whether-or-not-anything-is-plugged-in.md).
