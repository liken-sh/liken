# audio-operator

A Kubernetes DRA driver that publishes each physical audio output as
its own device. A pod claims one monitor's speakers, or the analog
jack, and receives the PipeWire socket and the name of the sink its
streams must reach.

This is an instance of liken's device operator pattern (milestone 56).
liken publishes the facts about hardware that no other layer can
observe, and which PCM device plays into which monitor is not one of
them: it comes from the ELD block that the graphics driver writes into
the audio driver, so a running daemon is what makes it true, and it
changes whenever somebody moves a cable. That daemon does not belong
in the read-only root that every machine boots, where every machine
would carry it for the one machine that uses it. So the operator is an
ordinary workload. It claims the machine's audio controller through an
ordinary `liken.sh` claim, runs PipeWire and WirePlumber beside itself
in the same pod, and publishes what PipeWire holds under its own
driver name, `audio.liken.sh`. The system image carries no sound
server.

The operator uses no private interface into liken. The raw claim, the
ResourceSlices it writes, and the CDI files it leaves for the
container runtime are the public contracts that any DRA driver on any
Kubernetes cluster gets. A cluster that never deploys it behaves
exactly as it does now.

## What it publishes

One device for each playback PCM device on the claimed card: each
HDMI or DisplayPort output, and the analog jack. Membership does not
depend on a monitor being connected or on PipeWire holding a sink. A
device whose monitor is unplugged stays in the slice with two taints
on it, so a person can create a pod for it and the pod starts when
somebody plugs the monitor back in.

The device name is the ALSA card and PCM device number, `card0-pcm3`.
The number comes from the codec's pin order, which the driver
enumerates the same way at every boot on the same hardware and kernel.
It is not stable across machines, and this operator does not claim
that it survives a kernel change, because that was not measured. A
claim that must survive either one selects on the attributes instead.

The name is also published as the `output` attribute, and its two
halves as `card` and `pcm`, because a selector cannot read a device's
name. CEL is given `device.driver`, `device.attributes`,
`device.capacity`, and `device.allowMultipleAllocations`, and there is
no `device.name` among them, so an identity that lived only in the
name would be an identity nothing could ask for.

| Attribute | Type | What it is |
|---|---|---|
| `output` | string | the device's own name, `card0-pcm3` |
| `card` | int | the ALSA card number |
| `pcm` | int | the PCM device number on that card |
| `connectionType` | string | `hdmi`, `displayport`, or `analog` |
| `sinkName` | string | the PipeWire node name a consumer's streams target |
| `manufacturer` | string | the monitor's three-letter PNP manufacturer code, from the ELD |
| `product` | string | the monitor's product code, four lowercase hexadecimal digits |
| `monitorName` | string | the monitor's name, the same EDID descriptor the display operator publishes as the model name |
| `lpcmChannels` | int | the highest uncompressed channel count the monitor accepts |
| `speakers` | string | the speaker allocation, in the kernel's own names: `FL/FR` |
| `monitor.liken.sh/id` | string | the pairing identity, which the next section defines |

The monitor attributes are present on an HDMI or DisplayPort output
whose monitor answers, and absent otherwise. `sinkName` is present
while PipeWire holds a sink for the output, and it is absent when the
name is longer than the API's 64-character limit on a string
attribute, because a truncated name would name nothing.

The sink name is `liken.audio.` followed by the device name. This operator
declares every sink node itself, which the section on finding the card
explains, so it names them too. A name built from the ALSA address is
the same at every start on the same card, where the name a session
manager builds from a card profile is not.

    $ kubectl get resourceslice liken-1-audio.liken.sh -o yaml
    spec:
      driver: audio.liken.sh
      nodeName: liken-1
      devices:
        - name: card0-pcm3
          attributes:
            output: {string: card0-pcm3}
            card: {int: 0}
            pcm: {int: 3}
            connectionType: {string: hdmi}
            manufacturer: {string: GSM}
            product: {string: "5b09"}
            monitorName: {string: LG ULTRAWIDE}
            lpcmChannels: {int: 2}
            speakers: {string: FL/FR}
            sinkName: {string: liken.audio.card0-pcm3}
            monitor.liken.sh/id: {string: gsm-5b09-lg-ultrawide}
        - name: card0-pcm8
          attributes:
            output: {string: card0-pcm8}
            card: {int: 0}
            pcm: {int: 8}
            connectionType: {string: hdmi}
            manufacturer: {string: DEL}
            product: {string: "4071"}
            monitorName: {string: DELL U2415}
            lpcmChannels: {int: 2}
            speakers: {string: FL/FR}
            sinkName: {string: liken.audio.card0-pcm8}
            monitor.liken.sh/id: {string: del-4071-dell-u2415}

The ELD carries no serial number. The kernel prints every field of the
block, and there is no EDID serial among them, so an audio device says
which model of monitor it plays into and cannot say which unit.

## The pairing identity

`monitor.liken.sh/id` is the attribute that pairs a screen with that
screen's speakers. The display operator publishes the same name for
the same monitor, and a `matchAttribute` constraint compares the two
values, so they must be identical byte for byte. The derivation is
therefore part of the contract between the two repositories:

    <manufacturer>-<product>[-<name>]

* **`<manufacturer>`** is the three-letter PNP manufacturer identifier
  in lowercase. EDID packs it into two bytes, five bits for each
  letter, most significant letter first. The ELD block carries those
  two bytes in EDID order at offsets 16 and 17, and the kernel prints
  `manufacture_id` in the other byte order, because it reads them as
  a little-endian integer. `0x1e6d` is `GSM`, which is LG. A pair of
  bytes that decodes to something other than three letters publishes
  no pairing attribute at all.
* **`<product>`** is the product code as exactly four lowercase
  hexadecimal digits, from the little-endian value at offsets 18 and
  19.
* **`<name>`** is the monitor name in lowercase, trimmed, with each
  run of spaces replaced by one dash. EDID pads a descriptor with
  spaces to fill it, so the padding is trimmed before the spaces
  inside the name become dashes. It is appended only when the name is
  not empty.

An LG UltraWide gives `gsm-5b09-lg-ultrawide`. A panel with no name
descriptor gives `boe-095f`.

The name is optional because one operator can read a name that the
other cannot. If a missing name dropped the whole value, one operator
would publish a pairing attribute for a monitor and the other would
publish none, and a claim with a `matchAttribute` constraint across
the two would park forever with nothing in the cluster saying why.
Both repositories test the same vectors, so a change to either one
that broke the parity fails a test.

The domain is `monitor.liken.sh`, which neither driver owns, and that
is deliberate. The scheduler compares a fully qualified attribute name
across devices without regard to which driver published them, and an
attribute written without a domain is assumed to be in the publishing
driver's domain. So a bare `monitorName` from this operator and a bare
`monitorName` from the display operator are two different fully
qualified names, and they never match.

Two monitors of the same model produce the same value. The constraint
is then satisfied by either pairing, so a claim can get one screen
with the other screen's speakers. The fix needs a value tied to the
connector rather than to the model, and the ELD's `port_id` is the
candidate. Whether it corresponds to the DRM connector the display
operator names was not measured.

## Deploying it

    kubectl apply -k deploy/

Or reference `deploy/` from your own GitOps and patch it there. The
base assumes the namespace `liken-system` exists.

Nothing states which machine has the speakers. The operator's pod
claims the audio controller, only a machine with one publishes one,
and the scheduler puts the pod where the hardware is. To serve the
cards on several machines, raise `replicas` on the Deployment to the
number of machines: each replica's claim allocates a distinct card,
the scheduler spreads the pods to wherever the hardware is, and a
replica past that number parks Pending and costs nothing.

Two replicas on one machine is a different case, and this operator
does not serve it. Both would write the same ResourceSlice, because
the slice is named for the node and the driver, and each write would
replace the other's devices. A machine with two sound cards therefore
serves one of them, and the claim's selector decides which. See
[the claim takes any sound card](plans/open-problems/the-claim-takes-any-sound-card-and-a-node-serves-only-one.md).

Two DeviceClasses come with the base. `audio-controller` is the raw
device the operator claims from liken:

    device.driver == "liken.sh" &&
    has(device.attributes["liken.sh"].subsystem) &&
    device.attributes["liken.sh"].subsystem == "sound"

`audio-output` is what a consumer claims:

    device.driver == "audio.liken.sh"

To uninstall it for good, delete the workload and then delete the
slice by name:

    kubectl delete -k deploy/
    kubectl delete resourceslice liken-1-audio.liken.sh

The operator never deletes its own slice, which the section on
disconnects and restarts explains, so the slice outlives the pod. A
node that leaves the cluster takes its slice with it, because the Node
owns it.

## Claiming an output

Name one output by its device name:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-speakers
      namespace: media
    spec:
      devices:
        requests:
          - name: output
            exactly:
              deviceClassName: audio-output
              selectors:
                - cel:
                    expression: |
                      device.attributes["audio.liken.sh"].output == "card0-pcm3"
              tolerations:
                # A monitor that somebody unplugs for a moment is not
                # a loss. This number belongs to the workload, not to
                # the operator: it says how long this pod may hold an
                # output that cannot play before the pod ends.
                - key: audio.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

Name it by its monitor instead, and the claim survives a cable that
moves between the card's outputs. The pairing attribute is present
only on an HDMI or DisplayPort output whose monitor answers, so the
selector guards it first: a selector that reads a missing attribute
fails the whole allocation.

    selectors:
      - cel:
          expression: |
            has(device.attributes["monitor.liken.sh"].id) &&
            device.attributes["monitor.liken.sh"].id == "gsm-5b09-lg-ultrawide"

The attribute map is two levels deep and splits on the domain, so the
pairing attribute, whose full name is `monitor.liken.sh/id`, reads as
the key `id` under the domain `monitor.liken.sh`. The `matchAttribute`
field in the next section is not CEL, and it takes the full name.

Do not tolerate `audio.liken.sh/no-monitor` or
`audio.liken.sh/no-sink`. Those two say why an output cannot play, and
`audio.liken.sh/disconnected` says what happens to the pod that holds
it, which the section on disconnects and restarts sets out.

Choose `tolerationSeconds` with one more transient in mind, which is
this operator's own. WirePlumber drops a card's sinks and builds them
again when it switches the card's profile, so a reconcile pass that
lands inside that window sees no sink and taints every output for a
moment. A number in the tens of seconds absorbs both that and a person
who unplugs a cable to move it.

Then the pod names the claim, and the container that plays the audio
names the pod's entry:

    apiVersion: v1
    kind: Pod
    metadata:
      name: player
      namespace: media
    spec:
      resourceClaims:
        - name: output
          resourceClaimName: kitchen-speakers
      containers:
        - name: player
          image: ...
          resources:
            claims:
              - name: output

Leave out the `selectors` block to claim any output on the card.

## Claiming a screen and that screen's speakers

A pod that plays a video on the kitchen monitor needs that monitor and
that monitor's speakers. One claim holds both: one request against
`display.liken.sh`, one request against `audio.liken.sh`, and a
constraint that requires one value of `monitor.liken.sh/id` across the
two.

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-screen
      namespace: media
    spec:
      devices:
        requests:
          - name: screen
            exactly:
              deviceClassName: display-output
              tolerations:
                - key: display.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30
          - name: speakers
            exactly:
              deviceClassName: audio-output
              tolerations:
                - key: audio.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30
        constraints:
          # Every device in question must carry this attribute and the
          # same value for it. The scheduler compares the two drivers'
          # devices without regard to which driver published them, so
          # the screen and the speakers come from one monitor.
          - requests: [screen, speakers]
            matchAttribute: monitor.liken.sh/id

The container receives both deliveries: the screen's, whatever the
display operator's own delivery is, and this operator's socket and
sink name.

## What a consumer receives

A mount and two environment variables. No device node.

| | |
|---|---|
| mount | `/var/run/audio.liken.sh`, read-only, the directory that holds PipeWire's socket |
| `PIPEWIRE_REMOTE` | `/var/run/audio.liken.sh/pipewire-0` |
| `PIPEWIRE_NODE` | the allocated output's sink name |

A PipeWire client resolves `PIPEWIRE_REMOTE` first, and a value that
starts with a slash is used as an absolute path with no runtime
directory consulted. `PIPEWIRE_NODE` sets `target.object` on every
stream the client creates, and `target.object` takes a node name.

Both variables are read by clients that use PipeWire's own stream API.
A client that plays through the PulseAudio protocol or the ALSA
compatibility plugin selects its sink another way, and this document
does not state which, because that was not measured.

The mount is read-only. Connecting to a Unix socket needs write
permission on the socket itself and not on the file system that
carries it, and a consumer has no reason to create anything in that
directory.

**One container holds at most one output.** `PIPEWIRE_REMOTE` and
`PIPEWIRE_NODE` each hold one value, so two claims on one container
write the same two variables and one of them wins with no error. A pod
that plays into two outputs runs two containers, and each container
names its own claim in its own `resources.claims`.

The consumer therefore holds no audio device node, and the operator
holds them all. liken's own claim on the controller delivers the
card's whole subtree, which is the control node, the PCM nodes, and
the input nodes of its jacks, and this operator republishes none of
them, so the two drivers never deliver the same `/dev` path.

## Finding the card with no udev

**This operator declares PipeWire's sink nodes. WirePlumber's ALSA
monitor is off.**

The monitor enumerates cards through libudev. A liken machine runs no
udevd, so udev answers with nothing, the monitor builds no device, and
PipeWire ends up holding a graph with no ALSA node in it on a machine
whose speakers work. Every output would then publish with the no-sink
taint and no pod could play.

The operator needs no udev to know the hardware. It already enumerates
the card's playback PCM devices from the nodes its claim delivers, and
reads each one's ELD through the ALSA control interface. So before it
starts the daemons it writes what it found into
`/etc/pipewire/pipewire.conf.d/60-liken-outputs.conf`, one
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
          "node.description": "liken audio output, ALSA card 0 device 3",
          "audio.channels": "2",
          "audio.position": "FL,FR",
          "liken.audio.card": "0",
          "liken.audio.pcm": "3"
        }
      }
    ]

This is PipeWire's own answer for a setup with no session manager
finding hardware, which `pipewire-props(7)` states and
`src/daemon/pipewire.conf.in` carries as a commented example. A
`context.objects` entry is appended to the daemon's own, because
PipeWire merges a dictionary section key by key and appends an array
section. `liken.audio.card` and `liken.audio.pcm` are this operator's
own properties, and they are what map a node in `pw-dump` back to the
output the slice publishes: the adapter module hands the whole `args`
dictionary to the node, and a node's info block carries its whole
property list, so a key PipeWire does not recognize still arrives in
the dump.

The monitor is off on every host and not only on a liken machine, so
this operator builds one graph everywhere from one enumeration.
WirePlumber stays for the policy: it links each client's stream to the
sink the client names, which is the whole of what this operator needs
from a session manager.

**Every playback PCM device is declared, whether a monitor is on it or
not.** PipeWire reads these declarations once, while it loads its
configuration, so the set is fixed for the life of the daemon. A set
that followed the cables would need a PipeWire restart every time
somebody moved one, and a restart ends every consumer's session. The
PCM devices a card has are fixed when its driver binds, so a set that
follows the card never moves.

The taints stay honest under that. An HDMI output with no monitor has a
sink node now, so it is the ELD and not the missing node that taints
it, and the ELD is the fact that says whether a monitor is there. That
output carries `disconnected` and `no-monitor`, and it carries no
`no-sink`, because it has a sink and the slice publishes the name of
it. The `no-sink` taint keeps its own meaning: PipeWire holds no node
for this PCM device. That is what `nofail` on each object leaves
possible. One output that cannot be created must not stop the daemon,
because every other output on the card would lose its sink with it.

**What this gives up.** The card profile path, which is what builds
sinks from a card's profiles and ports, needs the ALSA monitor. So
there is no hardware mixer volume, no profile switching, and no port
availability from the card's own jack detection. Volume is PipeWire's
software volume, the channel layout is stereo on every output, and jack
detection is the operator's own, read from the card's input nodes. A
monitor that accepts more than two channels still gets two.

## The privilege it takes

None. The pod declares no `hostNetwork`, adds no capability, and drops
`ALL`. Everything it touches it touches through the device nodes its
claim delivers.

* **The card.** The control node and the PCM nodes arrive with the
  claim, and PipeWire opens them like any other file.
* **The jacks.** The card's `/dev/input/event*` nodes arrive with the
  same claim, and a monitor that arrives or leaves is a switch event
  on one of them.
* **Real-time priority.** PipeWire asks RTKit for one, finds no RTKit
  in this pod, and runs without one, so not even `SYS_NICE` is here.

The pod runs one more process beside the two daemons: a D-Bus system
bus of its own, because WirePlumber's device reservation speaks that
bus and PipeWire's RTKit lookup falls back to it. Nothing outside the
pod reaches it.

WirePlumber runs the `main-embedded` profile, which is systemwide and
keeps no state across restarts, with every hardware monitor turned off.
The ALSA monitor is off because it finds nothing without udev, which
the section on finding the card explains. The Bluetooth and camera
monitors are off because this operator has no claim on what they would
find: its domain is the sound card its raw claim allocated, so a
Bluetooth audio device or a camera must not arrive in this graph. The
image states all of that in `config/50-audio-operator.conf` instead of
resting on what happens to be reachable from the pod.

Beside those, the pod takes the two hostPath mounts every DRA driver
takes: the kubelet's plugin registry directory, so the kubelet finds
the driver, and `/var/run/cdi`, so prepared claims land where the
container runtime reads them. Its own plugin socket directory,
`/var/lib/kubelet/plugins/audio.liken.sh`, is the third, and
`/var/run/audio.liken.sh` is the fourth, because a consumer's mount
comes from the host and not from this pod.

## Disconnects and restarts

**An output that cannot play is tainted, never deleted.** The device
stays in the slice, and it carries two taints or three:

| Key | Effect | When it appears | Who tolerates it |
|---|---|---|---|
| `audio.liken.sh/disconnected` | `NoExecute` | the output cannot play | the consumer, with its own `tolerationSeconds` |
| `audio.liken.sh/no-monitor` | `NoSchedule` | no monitor answers on this HDMI output | nobody |
| `audio.liken.sh/no-sink` | `NoSchedule` | PipeWire holds no node for this PCM device | nobody |

The `NoExecute` taint says the output cannot serve a stream right now.
It goes on for either reason, and it ends the pod that holds the claim
once the claim's `tolerationSeconds` runs out. A monitor that drops for
two seconds and comes back must not do that, so the consumer tolerates
it.

A tolerated `NoExecute` taint still permits allocation, though, so a
consumer that tolerated only that one would be scheduled onto an output
that cannot play, wait out its toleration, get evicted, and be
scheduled again. One of the two untolerated `NoSchedule` taints is
always there beside it, and that is what holds the pod Unschedulable
instead, until the output can really play.

The reasons carry separate keys because they are separate facts, and
each one has its own repair. `no-monitor` reads the ELD, so it clears
when somebody plugs the cable back in. `no-sink` says PipeWire could
not create the node, and only a restart creates it. An output that
publishes a `sinkName` never carries `no-sink`, because one branch in
the operator writes the name or the taint.

Deleting the device instead of tainting it would strand the next
consumer: the allocation still names the device, `NodePrepareResources`
retries against a device that is in no slice, and nothing bounds that
retry. A device leaves the slice only when the card does.

**A running pod's device set never changes.** CRI carries CDI devices
only at container creation, CDI has no re-apply operation, and NRI's
post-create updates reach cgroup settings and not device nodes. The
pod is one session. The taint is what ends the session so the
scheduler can start the next one.

**An operator restart ends every client's audio.** The socket belongs
to the PipeWire in this pod, so a restart takes it away and every
client's connection with it. The prepared CDI mounts survive on the
host, because the mount is a host path, but the socket behind them
does not exist again until the new pod's PipeWire creates it. A client
that reconnects finds it. A client that does not has to restart, and
nothing in this operator restarts it. That is the same trade the
display operator makes: the session is a connection to a daemon, and
the daemon restarting ends it.

**A PCM device that appears or leaves restarts the pod.** PipeWire
builds its nodes from a document the operator writes before the daemons
start, so a PCM device that was not there then has no node and cannot
get one under this PipeWire. Every reconcile pass generates the
document again and compares. A difference stops the operator, and the
kubelet's restart declares the new set. The operator does not taint on
its way out of that one: the restart takes the socket from every
consumer for a few seconds, and a `NoExecute` taint would evict all of
them for a gap they survive by reconnecting. This costs one restart for
a card that arrived or left, and it never fires for a monitor somebody
plugged in, because a card's PCM devices are fixed when its driver
binds.

**The published slice survives the restart.** The operator never
deletes it, not on shutdown and not when it cannot read the card. The
Node owns it, so the garbage collector removes it when the machine
leaves the cluster, and the new pod republishes over it.

**PipeWire, WirePlumber, and the operator live and die together.**
PipeWire holds the card, and this operator holds the card's exclusive
claim, so an operator that outlived its sound server would publish
outputs that no pod can play through and keep the hardware from a pod
that could. The operator starts both daemons as its own children and
waits on them. Either one exiting ends the container with a nonzero
status, and the kubelet restarts the set.

## Not here yet

* **Shared sinks.** PipeWire mixes streams, so one sink can serve
  several consumers, which a monitor cannot. DRA can express it with
  `allowMultipleAllocations`. This version is exclusive anyway, for
  the one-owner clarity the other operators have, and because a claim
  on a shared sink gives a workload no say over what else plays
  through it. A second DeviceClass over the same devices, marked
  shared, is the obvious extension.
* **The analog jack on a machine that uses none.** Every HDA
  controller has an analog output, and most machines have nothing
  plugged into theirs. This version publishes it unconditionally, and
  it publishes it untainted whenever PipeWire holds a sink for it,
  because an analog jack with nothing in it is a working output as far
  as the card is concerned. So an empty jack allocates to a claim, and
  the pod that holds it plays into nothing and reports no error. The
  jack's own input node says whether something is plugged in, so a
  later version could taint it, or publish it only when it is
  occupied, at the cost of a device that appears and disappears with a
  cable.
* **More than stereo.** Every declared node asks for two channels at
  `FL,FR`, so a monitor or a receiver that accepts more gets two. The
  ELD reports the count, and the operator publishes it as
  `lpcmChannels`, but the ELD is readable only while the cable is in
  and the declaration is written once at start. A layout taken from the
  ELD would give one graph when the cable is in at boot and another
  when somebody plugs it in later. A version that wanted both would
  have to restart PipeWire on a channel-count change, the way it
  restarts for a new PCM device.
* **The identical-monitor tiebreak.** Two monitors of the same model
  share one `monitor.liken.sh/id`, which the pairing section
  describes. Whether the ELD's `port_id` distinguishes them, and
  whether it corresponds to the DRM connector the display operator
  names, awaits a machine with two identical monitors.
* **The drill.** Milestone 59 states what a drill against a real card
  and two real monitors must show. None of it has run.
* **Metrics.** The operator prints what it does to stderr and reports
  device state through the taints. It exposes no metrics endpoint.

## Building it

    go build ./...
    go test ./...
    docker build -t audio-operator .

The Kubernetes libraries and the Go version are pinned to what liken
builds against, because the two drivers serve the same kubelet on the
same node.

## License

MIT. See [LICENSE](LICENSE).
