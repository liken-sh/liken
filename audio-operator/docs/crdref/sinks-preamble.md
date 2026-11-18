A `Sink` is one playback endpoint as a Kubernetes resource: an
analog jack, an HDMI or DisplayPort output, the playback side of a USB
card, or a Bluetooth speaker. The operator creates a `Sink` for each
endpoint it publishes. A `Sink` is cluster-scoped, like a `Node`, and
has the same name as the device in the `ResourceSlice`. You never
create one, and you delete one only after its card has gone. The
operator writes `status`, which has the endpoint's location, the
controls that the card declares, and the values that the operator last
read. media-operator writes `status.session`, which has the last
volume request from a remote's volume key. You write `spec`, the
settings you want for the endpoint.

```yaml
apiVersion: audio.liken.sh/v1alpha1
kind: Sink
metadata:
  name: node-1-usb-0573-1573-a34004801402-usb-audio
spec:
  volume:
    level: 80
    max: 100
    step: 5
  controls:
    PCM Playback Volume: "120"
status:
  node: node-1
  location: "1-6"
  connectionType: usb
  card:
    number: 1
    id: HID
    driver: USB-Audio
    name: USB Audio and HID
  pcm:
    device: 0
    id: USB Audio
  nodeName: liken.audio.card1-pcm0
  layoutSource: ChannelMap
  capabilities:
    PCM Playback Volume:
      type: integer
      min: 0
      max: 127
      minDecibels: "-63.50"
      maxDecibels: "0.00"
      channels: 2
    PCM Playback Switch:
      type: boolean
      channels: 1
  observed:
    volume: 80
    mute: false
    controls:
      PCM Playback Volume: "120"
      PCM Playback Switch: "on"
  conditions:
    - type: Connected
      status: "True"
    - type: Ready
      status: "True"
    - type: LayoutApplied
      status: "True"
```

A pod that plays sound still gets the output through a claim, as the
[claim guide](/docs/guides/claim/) shows, and it keeps its own stream
volume. The `Sink` covers everything else about the output: its
default level, its mute, and the card's own controls. Because a `Sink`
is an ordinary Kubernetes resource, anything with the right RBAC can
change it. A pod that only needs to mute one speaker needs no claim,
and a rule that lowers every speaker at night is one patch for each
`Sink`. [Set endpoint volume and controls](/docs/guides/rest/) walks
through it.
