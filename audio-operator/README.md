# audio-operator

`audio-operator` is a Kubernetes
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver for the audio inputs and outputs of a
[`liken`](https://github.com/liken-sh/liken) machine. It publishes
each monitor's speakers, the analog jack, the playback and capture
sides of a USB card, and each paired Bluetooth speaker as a device
that a pod can claim. The operator's pod runs PipeWire and
WirePlumber, so the system image has no sound server. A pod that
claims an output gets the PipeWire socket and the name of the
PipeWire node that its streams must play to.

Each output or input is also a `Sink` or a `Source` resource under
`audio.liken.sh`. Its `status` shows what the card declares and what
the operator last read from it. Its `spec` sets the volume, the mute,
and the card's own controls. These settings apply whether or not a
pod is playing, so a rule that mutes the kitchen at night needs no
claim, and a player pod that has the speaker keeps control of its own
stream volume.

A workload gets its audio output from its manifest. When one claim
asks both `display-operator` and this operator for the same monitor,
a video's sound plays on the monitor that shows its picture. A player
pod can send music to the amplifier on the analog jack. The claim
names the output by its monitor or by its jack, the scheduler finds
the machine, and the container gets the socket. You need no SSH, no
configuration on the host, and no privileged pod.

A paired Bluetooth speaker is another output, on a machine that also
runs the [`bluetooth-operator`](https://liken.sh/bluetooth/). That
operator publishes each radio's media bus as a device. The claim of
the `audio-operator` pod allocates the bus, and WirePlumber uses it to register the A2DP
endpoint with `bluetoothd`. Each paired speaker then appears as an
`audio.liken.sh` device named by its MAC address, and a workload
claims it the same way it claims an HDMI output.

The operator is one of `liken`'s
[extension operators for claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/).
These are optional workloads that you install like any other
manifest, and a cluster runs without them. The operator needs the
sound card, which `liken`'s own DRA driver publishes as a raw device.
This operator claims the card through an ordinary `liken.sh` claim,
and publishes one `audio.liken.sh` device for each playback PCM device
and each capture PCM device of the card. It uses no private interface
into `liken`. The claim, the `ResourceSlice`s, and the Container
Device Interface (CDI) files are the public interfaces that any DRA
driver gets.

The operator uses `bluetooth-operator` through the same public
interfaces. Its class selects on `sound.liken.sh/supportsSound` and
names no driver, so one claim allocates both the sound card that
`liken` publishes and the media bus that `bluetooth-operator`
publishes. The operators exchange nothing outside those claims.

## The manual

**[liken.sh/audio](https://liken.sh/audio/)** is the manual. It also
serves the deployment manifests as raw YAML, so you can do the whole
install from there:

* [Install the operator](docs/content/docs/guides/install.md)
* [Play sound to an output](docs/content/docs/guides/claim.md)
* [Pair sound with its screen](docs/content/docs/guides/pair.md):
  one claim for a monitor's screen and that monitor's speakers
* [Set endpoint volume and controls](docs/content/docs/guides/rest.md):
  the level, the mute, and the card's own controls, without a claim
* [Devices](docs/content/docs/reference/devices.md): the device
  classes, the attributes, the taints, and what a claim gives a
  container
* [Sinks](docs/content/docs/reference/sinks.md) and
  [Sources](docs/content/docs/reference/sources.md): the resources
  that show each endpoint's state

On a cluster whose machine publishes its sound card, this installs
the operator:

    kubectl apply -n liken-system \
      -f https://liken.sh/audio/deploy/crds.yaml \
      -f https://liken.sh/audio/deploy/deviceclasses.yaml \
      -f https://liken.sh/audio/deploy/rbac.yaml \
      -f https://liken.sh/audio/deploy/operator.yaml

[`deploy/`](deploy/) is the source of those files. It is a `kustomize`
base with the CRDs for `Sink` and `Source`, the RBAC, the `DaemonSet`
whose pod claims every sound device on its own node, the
`DeviceClass` `sound-card`, and the `audio-api` `Deployment` that
serves the capture API. That claim names `sound-card`, and the operator does not
start without it. You create the classes that your workloads claim
through, because they are cluster policy. The install guide has the
YAML for `audio-sink` and `audio-source`, which are the classes to
start with.

## The design

[`plans/`](plans/README.md) has the design documents: the taints on an
output, why the image is a file closure on `scratch`, how the kubelet
supervises the daemons, and why each endpoint is a `Sink` or a
`Source`. The pattern that this operator follows is described in
`liken`'s repository, in
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md).
This operator's own design is in
[milestone 59](https://github.com/liken-sh/liken/blob/main/plans/completed/59-the-audio-operator.md).

## The build

    go build ./...
    go test ./...
    docker build -t audio-operator .

The image is a file closure on `scratch`, with no shell and no package
manager. It has the `pipewire` and `wireplumber` daemons, the tools
that the operator and the `capture` container run (`pw-dump`,
`pw-cli`, `pw-record`, `flac`, and `opusenc`), `wpctl` for the smoke
test, `pw-top` and `pw-metadata` for a person who runs
`kubectl exec`, the modules that the daemons load, and their
libraries. The Kubernetes libraries and
the Go version match what `liken` builds against, because the two
drivers serve the same kubelet on the same node. The ELD fixtures in
`testdata` follow the layout that the kernel's `hda_eld.c` writes,
because a machine with no monitor reports no ELD block to capture.

## License

MIT. See [LICENSE](LICENSE).
