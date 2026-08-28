A `Source` is one capture endpoint as a Kubernetes resource: an
analog input, or the capture side of a USB card. The operator
creates one for every capture endpoint it publishes, cluster-scoped
like a `Node`, named the same way a `Sink` is. You never create or
delete one. The operator writes the whole of `status`, and you write
`spec`, which states what the endpoint rests at.

```yaml
apiVersion: audio.liken.sh/v1alpha1
kind: Source
metadata:
  name: usb-0573-1573-a34004801402-usb-audio-capture
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

For a microphone, `mute` is the field that matters most. Setting
`spec.mute: true` on a `Source` closes that microphone for everyone,
a rule that closes every microphone is one patch per `Source`, and
`kubectl get sources` shows which ones are open. A pod that records
still holds the microphone through a claim, and its stream reaches
the node that `status.nodeName` names.

An HDA card serves several input jacks through one capture PCM and
picks the live jack with its `Input Source` control, so the `Source`
is the PCM and the jack is a control in `spec.controls`. A Bluetooth
headset's microphone is not published yet, because it needs the
headset profiles the pod does not run.
