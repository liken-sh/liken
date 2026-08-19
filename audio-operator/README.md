# audio-operator

A Kubernetes
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver that publishes each physical audio output of a
[`liken`](https://github.com/liken-sh/liken) machine as a claimable
device: each monitor's speakers, and the analog jack. It runs
PipeWire and WirePlumber in its pod, so the system image contains no
sound server. A pod that claims an output receives the PipeWire
socket and the name of the sink its streams must reach.

That makes an audio output something you give a workload from a
manifest. A video's sound plays on the same monitor that shows its
picture, paired with the display operator through one claim. A
player pod sends music to the amplifier on the analog jack. The
claim names the output, by monitor or by jack. The scheduler finds
the machine, and the container receives the socket. This needs no
SSH, no configuration on the host, and no privileged pod.

A paired Bluetooth speaker is one more such output, on a machine
that also runs the
[`bluetooth-operator`](https://bluetooth.liken.sh). That operator
publishes each radio's media bus as a device, this pod's claim
allocates it, and WirePlumber registers the A2DP endpoint with
`bluetoothd` over it. Each paired speaker then publishes as an
`audio.liken.sh` device named by its MAC address, and a consumer
claims it exactly like an HDMI output.

The operator is one of `liken`'s
[hardware operators](https://liken.sh/docs/concepts/hardware-operators/):
optional workloads, installed like any other manifest, that a
cluster runs fine without. What it needs from `liken` is the card.
`liken`'s own DRA driver publishes the raw hardware. This operator
claims the sound card through an ordinary `liken.sh` claim, and it
publishes the outputs at the grain a workload asks for: one device
per playback PCM device under `audio.liken.sh`. It uses no private
interface into `liken`: the claim, the `ResourceSlices`, and the
Container Device Interface (CDI) files are the public contracts any
DRA driver gets.

It reaches a sibling operator the same way. Its class selects on
`sound.liken.sh/supportsSound` and names no driver, so one claim
collects the sound card `liken` publishes and the media bus the
`bluetooth-operator` publishes. Nothing private passes between the
two operators.

## The manual

**[audio.liken.sh](https://audio.liken.sh)** is the manual, and it
serves the deployment manifests as raw YAML, so an install starts
and ends there:

* [Install the operator](docs/content/docs/guides/install.md)
* [Play sound to an output](docs/content/docs/guides/claim.md)
* [Pair sound with its screen](docs/content/docs/guides/pair.md):
  one claim that holds a monitor's screen and that monitor's
  speakers
* [Devices](docs/content/docs/reference/devices.md): the class, the
  attributes, the taints, and what a claim delivers

The short version, on a cluster whose machine publishes its sound
card:

    kubectl apply -n liken-system \
      -f https://audio.liken.sh/deploy/deviceclasses.yaml \
      -f https://audio.liken.sh/deploy/rbac.yaml \
      -f https://audio.liken.sh/deploy/operator.yaml

[`deploy/`](deploy/) is the source of those files: a `kustomize` base
with the RBAC, the `DaemonSet` whose pod claims every sound device
on its own node, and one `DeviceClass`, `sound-card`, which that
claim names and the operator cannot start without. The class your
workloads claim through is cluster policy, yours to create; the
install guide gives the YAML for `audio-output`, the one to start
with.

## The design

[`plans/`](plans/README.md) holds the design documents: the taints
on an output, why the image is a file closure on `scratch`, and how
the kubelet supervises the daemons. The pattern this operator is an
instance of is documented in `liken`'s repository, in
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md),
and this operator's own design in
[milestone 59](https://github.com/liken-sh/liken/blob/main/plans/completed/59-the-audio-operator.md).

## The build

    go build ./...
    go test ./...
    docker build -t audio-operator .

The image is a file closure on `scratch`: the four binaries the
pod runs (`pipewire`, `wireplumber`, `pw-dump`, `wpctl`), three
more for a person with `kubectl exec` (`pw-top`, `pw-cli`,
`pw-metadata`), the modules the daemons load, and their libraries,
with no shell and no package manager. The Kubernetes libraries and
the Go
version are pinned to what `liken` builds against, because the two
drivers serve the same kubelet on the same node. The ELD fixtures in
`testdata` are assembled the way the kernel's `hda_eld.c` lays the
block out, because a machine with no monitor reports no block to
capture.

## License

MIT. See [LICENSE](LICENSE).
