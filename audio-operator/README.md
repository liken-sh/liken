# audio-operator

`audio-operator` lets a pod use a speaker or a microphone. The pod's
manifest claims a sound output or input the same way it claims any
other device. You set the volume and the mute of each output and
input with `kubectl`, whether or not anything is playing on it.

The operator runs the PipeWire sound server on each machine. It
publishes each of the machine's outputs and inputs as a device that a
pod can claim, and it applies the settings that you set on each
`Sink` and `Source`. A pod with a claim gets the PipeWire socket and
the name of the PipeWire node that its streams play to or record
from. The operator doesn't play anything itself. It also doesn't
choose which output a workload gets, because the claim decides that.
It doesn't pair Bluetooth devices. Its capture API records what a
`Sink` plays or what a `Source` hears, for anyone whom RBAC allows to
listen.

`audio-operator` is one of `liken`'s
[extension operators for claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/).
It claims the sound card that `liken` publishes, through the same
public interfaces that any driver uses, and a cluster can run without
it. A claim that names a monitor can get the monitor's screen from
`display-operator` and its speakers from this operator. When
`bluetooth-operator` runs on the same machine, each speaker paired
through it shows up here as another output. `media-operator` builds
its `Player`s from these devices, and it sends volume changes from a
remote to the `Sink`.

## The kinds

- A `Sink` is one playback endpoint: an HDMI or DisplayPort output,
  the analog jack, the playback side of a USB card, or a Bluetooth
  speaker. Its `spec` sets the volume, the mute, the card's own
  controls, the channel layout, and how many claims can share the
  endpoint. Its `status` reports what the card declares and what the
  operator last read from it.
- A `Source` is one capture endpoint, such as the capture side of a
  USB card, with the same kind of `spec` and `status` for recording.

The operator creates a `Sink` or a `Source` for each endpoint that it
publishes. You edit the `spec`, for example to set the level of one
monitor's speakers:

```yaml
apiVersion: audio.liken.sh/v1alpha1
kind: Sink
metadata:
  name: node-1-pci-0000-00-1f-3-hdmi-0
spec:
  volume:
    level: 60
  mute: false
```

The manual is at [liken.sh/audio](https://liken.sh/audio/). It has the
install guide, the guides to play, record, and set an endpoint's
volume, and a reference for the devices, the kinds, and the API.
The design documents and the open problems are in [`plans/`](plans/).

MIT. See [LICENSE](LICENSE).
