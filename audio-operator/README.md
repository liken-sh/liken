# audio-operator

A Kubernetes DRA driver that publishes each physical audio output as a
device. A pod claims one monitor's speakers, or the analog jack, and
receives the PipeWire socket and the name of the sink its streams must
reach.

This is an instance of liken's device operator pattern. liken publishes
the hardware facts no other layer can observe, and which PCM device
plays into which monitor is not one of them: it comes from the ELD
block the graphics driver writes into the audio driver, so a running
daemon makes it true, and it changes whenever somebody moves a cable.
That daemon does not belong in the read-only root every machine boots,
because only some machines play audio. So the operator is an ordinary
workload. It claims the audio controller through a `liken.sh` claim,
runs PipeWire and WirePlumber in the same pod, and publishes what
PipeWire holds under `audio.liken.sh`. The system image carries no
sound server.

The operator uses no private interface into liken. The raw claim, the
ResourceSlices it writes, and the CDI files it leaves for the runtime
are the public contracts any DRA driver gets. A cluster that never
deploys it behaves as it does now.

## What it publishes

One device for each playback PCM device on the claimed card: each HDMI
or DisplayPort output, and the analog jack. Membership does not depend
on a monitor being connected or on PipeWire holding a sink, so a device
whose monitor is unplugged stays in the slice with two taints and a pod
can wait for it.

The device name is the ALSA card and PCM number, `card0-pcm3`. The
number comes from the codec's pin order, which the driver enumerates
the same way at every boot on the same hardware and kernel; it is not
stable across machines, and a claim that must survive a kernel change
selects on the attributes instead. The name is also the `output`
attribute, with its halves as `card` and `pcm`, because CEL cannot read
a device's name.

| Attribute | Type | What it is |
|---|---|---|
| `output` | string | the device's name, `card0-pcm3` |
| `card` | int | the ALSA card number |
| `pcm` | int | the PCM device number on that card |
| `connectionType` | string | `hdmi`, `displayport`, or `analog` |
| `sinkName` | string | the PipeWire node name a consumer's streams target |
| `manufacturer` | string | the monitor's three-letter PNP manufacturer code, from the ELD |
| `product` | string | the monitor's product code, four lowercase hexadecimal digits |
| `monitorName` | string | the monitor's name, the same EDID descriptor the display operator publishes as the model |
| `lpcmChannels` | int | the highest uncompressed channel count the monitor accepts |
| `speakers` | string | the speaker allocation, in the kernel's names: `FL/FR` |
| `monitor.liken.sh/id` | string | the pairing identity, defined below |

The monitor attributes are present on an HDMI or DisplayPort output
whose monitor answers, and absent otherwise. `sinkName` is present
while PipeWire holds a sink, and absent when the name passes the API's
64-character limit. The sink name is `liken.audio.` plus the device
name: this operator declares every sink node itself (see
[Finding the card with no udev](#finding-the-card-with-no-udev)), so
the name is the same at every start on the same card.

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

The ELD carries no serial number, so an audio device says which model
of monitor it plays into and cannot say which unit. See
[the ELD carries no serial number](plans/open-problems/the-eld-carries-no-serial-number.md).

## The pairing identity

`monitor.liken.sh/id` pairs a screen with that screen's speakers. The
display operator publishes the same value for the same monitor, and a
`matchAttribute` constraint compares the two, so they must be identical
byte for byte. The derivation is a contract between the two
repositories:

    <manufacturer>-<product>[-<name>]

* **`<manufacturer>`** is the three-letter PNP id in lowercase, decoded
  from the two bytes at ELD offsets 16 and 17. `0x1e6d` is `GSM`, which
  is LG. Bytes that decode to something other than three letters
  publish no pairing attribute.
* **`<product>`** is the product code as four lowercase hexadecimal
  digits, from the little-endian value at offsets 18 and 19.
* **`<name>`** is the monitor name in lowercase, trimmed, with each run
  of spaces turned to one dash. It is appended only when the name is
  present.

An LG UltraWide gives `gsm-5b09-lg-ultrawide`; a panel with no name
gives `boe-095f`. The name is optional because one operator can read a
name the other cannot: if a missing name dropped the whole value, one
driver would publish the attribute and the other none, and the
constraint would park forever with nothing saying why. Both
repositories test the same vectors, so a change that breaks parity
fails a test.

The domain `monitor.liken.sh` belongs to neither driver, and that is
deliberate. The scheduler assumes an attribute with no domain is in the
publishing driver's domain, so a bare `monitorName` from each operator
would be two different names that never match.

Two monitors of one model produce one value, so a constraint is
satisfied by either pairing. See
[the ELD carries no serial number](plans/open-problems/the-eld-carries-no-serial-number.md).

## Deploying it

    kubectl apply -k deploy/

Or reference `deploy/` from your own GitOps. The base assumes the
namespace `liken-system` exists.

Nothing states which machine has the speakers. The pod claims the audio
controller, only a machine with one publishes one, and the scheduler
places the pod there. To serve cards on several machines, raise
`replicas` to the number of machines; a replica past that parks
Pending. A machine with two sound cards serves only one, because the
slice is named for the node and the driver. See
[the claim takes any sound card](plans/open-problems/the-claim-takes-any-sound-card-and-a-node-serves-only-one.md).

The base ships two DeviceClasses. `audio-controller` is the raw device
the operator claims from liken
(`device.attributes["liken.sh"].subsystem == "sound"`); `audio-output`
is what a consumer claims (`device.driver == "audio.liken.sh"`).

To uninstall, delete the workload and then the slice, which the
operator never deletes on its own:

    kubectl delete -k deploy/
    kubectl delete resourceslice liken-1-audio.liken.sh

## Claiming an output

Select an output by its device name, or by its monitor, which survives
a cable moving between the card's outputs. The monitor selector guards
the attribute first, because a selector that reads a missing attribute
fails the whole allocation:

    # by output
    device.attributes["audio.liken.sh"].output == "card0-pcm3"

    # by monitor, cable-independent
    has(device.attributes["monitor.liken.sh"].id) &&
    device.attributes["monitor.liken.sh"].id == "gsm-5b09-lg-ultrawide"

The attribute map splits on the domain, so `monitor.liken.sh/id` reads
in CEL as the key `id` under the domain `monitor.liken.sh`. The
`matchAttribute` field below is not CEL and takes the full name.

Tolerate `audio.liken.sh/disconnected` and nothing else; leave
`audio.liken.sh/no-monitor` and `audio.liken.sh/no-sink` untolerated.
Choose `tolerationSeconds` with one more transient in mind: WirePlumber
drops and rebuilds a card's sinks when it switches profile, so a
reconcile inside that window taints every output for a moment. A number
in the tens of seconds absorbs that and a person moving a cable.

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
                - key: audio.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

Leave out the selector to claim any output on the card.

## Claiming a screen and that screen's speakers

A pod that plays a video on the kitchen monitor needs that monitor and
its speakers. One claim holds both: a request against
`display.liken.sh`, a request against `audio.liken.sh`, and a
`matchAttribute` constraint that requires one value of
`monitor.liken.sh/id` across the two, so the screen and the speakers
come from one monitor.

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
          - requests: [screen, speakers]
            matchAttribute: monitor.liken.sh/id

The container receives both deliveries: the display operator's, and
this operator's socket and sink name.

## What a consumer receives

A mount and two environment variables. No device node.

| | |
|---|---|
| mount | `/var/run/audio.liken.sh`, read-only, the directory that holds PipeWire's socket |
| `PIPEWIRE_REMOTE` | `/var/run/audio.liken.sh/pipewire-0` |
| `PIPEWIRE_NODE` | the allocated output's sink name |

A PipeWire client resolves `PIPEWIRE_REMOTE` first, and an absolute
path is used as-is. `PIPEWIRE_NODE` sets `target.object` on every
stream. Both are read by clients that use PipeWire's stream API; a
client on the PulseAudio protocol or the ALSA compatibility plugin
selects its sink another way, which this document does not state. The
mount is read-only, because connecting to a Unix socket needs write
permission on the socket, not on the directory.

**One container holds at most one output.** `PIPEWIRE_REMOTE` and
`PIPEWIRE_NODE` each hold one value, so two claims on one container
overwrite, and the last wins. A pod that plays into two outputs runs
two containers. liken's claim on the controller delivers the card's
whole subtree, and this operator republishes none of it, so the two
drivers never deliver the same `/dev` path.

**A consumer's image must carry PipeWire's client configuration.**
`libpipewire` does not open a client context without
`/usr/share/pipewire/client.conf`. Debian ships that file in
`pipewire-bin`, the daemon package, not in the library package
`libpipewire-0.3-0`. An image that installs the library alone fails
before it reaches the socket, with `can't load config client.conf: No
such file or directory`.

## Finding the card with no udev

**This operator declares PipeWire's sink nodes, and WirePlumber's ALSA
monitor is off.** The monitor enumerates cards through libudev, and a
liken machine runs no udevd, so the monitor would build nothing and
PipeWire would hold a graph with no ALSA node on a machine whose
speakers work.

The operator needs no udev to know the hardware. It enumerates the
card's playback PCM devices from the nodes its claim delivers, reads
each one's ELD through the ALSA control interface, and writes what it
found into `/etc/pipewire/pipewire.conf.d/60-liken-outputs.conf` before
the daemons start, one `context.objects` entry for each PCM device:

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
finding hardware, which `pipewire-props(7)` documents. The
`liken.audio.card` and `liken.audio.pcm` properties map a node in
`pw-dump` back to the output the slice publishes. WirePlumber stays for
the policy: it links each client's stream to the sink named in
`PIPEWIRE_NODE`.

**Every PCM device is declared, monitor or not.** PipeWire reads the
declarations once, so the set is fixed for the daemon's life. A set
that followed the cables would need a restart every time somebody moved
one; a card's PCM devices are fixed when its driver binds, so a set
that follows the card never moves. `nofail` on each object keeps one
output that cannot be created from stopping the daemon and taking every
other output's sink with it.

**What this gives up.** The card-profile path needs the ALSA monitor,
so there is no hardware mixer volume, no profile switching, and no port
availability. Volume is PipeWire's software volume, every output is
stereo, and jack detection is the operator's, read from the card's
input nodes. A monitor that accepts more than two channels still gets
two. See
[every sink is declared stereo](plans/open-problems/every-sink-is-declared-stereo.md).

## The privilege it takes

None. The pod declares no `hostNetwork`, adds no capability, and drops
`ALL`. Everything it touches it touches through the device nodes its
claim delivers: the control and PCM nodes PipeWire opens like any file,
and the card's `/dev/input/event*` jacks, where a monitor arriving or
leaving is a switch event. PipeWire asks RTKit for real-time priority,
finds none, and runs without it, so not even `SYS_NICE` is here.

The pod runs a D-Bus system bus of its own, because WirePlumber's
device reservation and PipeWire's RTKit lookup speak it, and no process
outside the pod reaches it. WirePlumber runs the stateless
`main-embedded` profile with every hardware monitor off: the ALSA
monitor because it finds nothing without udev, and the Bluetooth and
camera monitors because this operator's domain is the sound card its
claim allocated. `config/50-audio-operator.conf` states that rather
than resting on what happens to be reachable.

Beside those, the pod takes the two hostPath mounts every DRA driver
takes, the kubelet plugin registry and `/var/run/cdi`, its own plugin
socket directory, and `/var/run/audio.liken.sh`, because a consumer's
mount comes from the host.

## Disconnects and restarts

**An output that cannot play is tainted, never deleted.** The device
stays in the slice with two taints or three:

| Key | Effect | When it appears | Who tolerates it |
|---|---|---|---|
| `audio.liken.sh/disconnected` | `NoExecute` | the output cannot play | the consumer, with its own `tolerationSeconds` |
| `audio.liken.sh/no-monitor` | `NoSchedule` | no monitor answers on this HDMI output | nobody |
| `audio.liken.sh/no-sink` | `NoSchedule` | PipeWire holds no node for this PCM device | nobody |

The `NoExecute` taint says the output cannot serve a stream now; it
ends the holder after the claim's `tolerationSeconds`, so the consumer
tolerates it to survive a short drop. A tolerated `NoExecute` taint
still permits allocation, so one of the untolerated `NoSchedule` taints
is always beside it to hold the pod `Unschedulable` until the output
can really play. The two reasons carry separate keys because they have
separate repairs: `no-monitor` reads the ELD and clears when the cable
returns, and `no-sink` clears only on a restart. An output that
publishes a `sinkName` never carries `no-sink`.

Deleting the device instead would strand the claim, because the kubelet
retries `NodePrepareResources` against a device in no slice with no
bound. A device leaves the slice only when the card does.

**A running pod's device set never changes.** CRI carries CDI devices
at container creation only, so the pod is one session and the taint is
what ends it.

**An operator restart ends every client's audio.** The socket belongs
to the PipeWire in this pod, so a restart takes it away; a client that
reconnects finds the new one, and a client that does not has to
restart. This is the same trade the display operator makes.

**A PCM device that appears or leaves restarts the pod.** PipeWire
builds its nodes from the document written before it starts, so a PCM
device that was not there then has no node. Every reconcile regenerates
the document and compares; a difference stops the operator, and the
kubelet's restart declares the new set. The operator does not taint on
its way out, because the gap is a few seconds a consumer survives by
reconnecting. This never fires for a monitor plugged in, because a
card's PCM devices are fixed when its driver binds.

**The slice survives the restart.** The operator never deletes it, and
the Node owns it, so the garbage collector removes it when the machine
leaves the cluster and the new pod republishes over it.

**PipeWire, WirePlumber, and the operator exit together.** The operator
starts both daemons as children and waits on them; either one exiting
ends the container nonzero, and the kubelet restarts the set.

## Not here yet

* **Shared sinks.** PipeWire mixes streams, so one sink could serve
  several consumers, which a monitor cannot. This version is exclusive,
  for the one-owner clarity the other operators have. See
  [a sink can be shared](plans/open-problems/a-sink-can-be-shared-and-this-one-is-not.md).
* **The analog jack.** Every HDA controller has an analog output, and
  most machines have nothing plugged in. This version publishes it
  unconditionally, so an empty jack allocates to a claim and produces
  no sound. See
  [the analog jack publishes whether or not anything is plugged in](plans/open-problems/the-analog-jack-publishes-whether-or-not-anything-is-plugged-in.md).
* **More than stereo.** Every node asks for two channels at `FL,FR`, so
  a monitor that accepts more gets two. See
  [every sink is declared stereo](plans/open-problems/every-sink-is-declared-stereo.md).
* **The identical-monitor tiebreak.** Two monitors of one model share
  one `monitor.liken.sh/id`. Whether the ELD's `port_id` distinguishes
  them awaits a machine with two identical monitors.
* **The drill.** No drill against a real card and two monitors has run
  yet. The plans state what one must show.
* **Metrics.** The operator prints to stderr and reports state through
  the taints. There is no metrics endpoint.

## Building it

    go build ./...
    go test ./...
    docker build -t audio-operator .

The Kubernetes libraries and the Go version are pinned to what liken
builds against, because the two drivers serve the same kubelet on the
same node.

## License

MIT. See [LICENSE](LICENSE).
