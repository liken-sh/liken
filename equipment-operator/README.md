# equipment-operator

A `Receiver` is one A/V receiver on the far end of a `liken` machine's
HDMI cable. The cable carries the picture and sound. The operator
reaches the receiver over the network, reports its power, input, and
volume, and while a `Player` plays through it, powers it on, selects the
input, and changes its volume from the room's remote.

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Receiver
metadata:
  name: theater
spec:
  denon:
    address: receiver.example
  inputs:
    - name: MPLAY
      machine: node-1
      monitor: hdmi-a-1
  session:
    player: house/theater
    input: MPLAY
    volumeTopic: liken/media/players/house/theater/volume
```

The volume topic belongs to the `Player` and is on the media bus run by
`media-operator`. While the session exists, the operator publishes a
retained owner mark on the topic plus `/owner`. Playback pods then leave
volume changes to the receiver operator.

The manual is at [equipment.liken.sh](https://equipment.liken.sh/).
`plans/README.md` indexes the plans. `make test` runs every check CI
runs.
