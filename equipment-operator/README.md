# equipment-operator

A `Receiver` is one A/V receiver on the far end of a `liken`
machine's HDMI cable. The cable carries the picture and the sound.
The operator carries the control: it reaches the receiver over the
network, reports its power, input, and volume, and while a `Player`
plays through it, powers it on, selects the input, and turns its
volume from the room's remote.

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

The volume topic is the `Player`'s, on the media bus the
`media-operator` runs. While the session stands, the operator holds
a retained owner mark on the topic plus `/owner`, and the pods leave
the level to the receiver.

The manual is at [equipment.liken.sh](https://equipment.liken.sh/).
`plans/README.md` indexes the plans. `make test` runs every check CI
runs.
