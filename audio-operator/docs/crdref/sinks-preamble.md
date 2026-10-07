A `Sink` is one playback endpoint as a Kubernetes resource: an
analog jack, an HDMI or DisplayPort output, the playback side of a
USB card, or a Bluetooth speaker. The operator creates one for every
endpoint it publishes, cluster-scoped like a `Node`, named by the
same device name the `ResourceSlice` carries. You never create or
delete one. The operator writes `status`: where the endpoint is, the
controls the card declares, and the values it last read. The media
operator writes `status.session`, the volume asks of a remote's keys.
You write `spec`, which states the settings you want for the endpoint.

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

The claim and the `Sink` answer two different needs. A pod that
plays sound still holds the output through a claim, as the
[claim guide](/docs/guides/claim/) shows, and it still has its own
stream volume. The `Sink` is for everything about the output that
is not the sound itself: its default level, its mute, and the
card's own controls. Because it is an ordinary Kubernetes resource,
anything with the right RBAC can change it. A pod that only wants
to mute the kitchen needs no claim, and a rule that lowers every
speaker at night is one patch per `Sink`.
[Set endpoint volume and controls](/docs/guides/rest/) walks through
it.
