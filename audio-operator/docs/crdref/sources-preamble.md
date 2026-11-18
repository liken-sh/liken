A `Source` is one capture endpoint as a Kubernetes resource: an
analog input, or the capture side of a USB card. The operator creates
a `Source` for each capture endpoint it publishes. A `Source` is
cluster-scoped, like a `Node`, and is named in the same way as a
`Sink`. You never create one, and you delete one only after its card
has gone. The operator writes all of `status`, and you write `spec`,
the settings you want for the endpoint.

```yaml
apiVersion: audio.liken.sh/v1alpha1
kind: Source
metadata:
  name: node-1-usb-0573-1573-a34004801402-usb-audio-capture
spec:
  mute: true
status:
  node: node-1
  location: "1-6"
  connectionType: usb
  pcm:
    device: 0
    id: USB Audio
  nodeName: liken.audio.card1-pcm0c
  capabilities:
    Mic Capture Volume:
      type: integer
      min: 0
      max: 16
      channels: 1
    Mic Capture Switch:
      type: boolean
      channels: 1
  observed:
    volume: 100
    mute: true
    controls:
      Mic Capture Volume: "8"
      Mic Capture Switch: "on"
  conditions:
    - type: Connected
      status: "True"
    - type: Ready
      status: "True"
```

For a microphone, `mute` is the most useful field. Setting
`spec.mute: true` on a `Source` closes that microphone for every
user. A rule that closes every microphone is one patch for each
`Source`, and `kubectl get sources` shows which microphones are open.
A pod that records still gets the microphone through a claim, and
its capture stream targets the PipeWire node that `status.nodeName`
names.

An HDA card serves several input jacks through one capture PCM and
selects the active jack with its `Input Source` control. So a
`Source` is the PCM, and you choose the jack by setting
`Input Source` in `spec.controls`. A Bluetooth headset's microphone is not published
yet, because it needs the headset profiles, which the pod does not
run.
