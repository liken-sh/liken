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

A `CECBus` is one HDMI tree's CEC wire, reached through a USB CEC
adapter on one of the cluster's machines. In `Listen` the adapter
sends nothing and reports the devices it hears. In `Control` it joins
the bus as a playback device that announces the machine's place in
the tree, scans the bus, and reports each device's address, name,
vendor, and power:

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: CECBus
metadata:
  name: den
spec:
  mode: Control
  adapters:
    - machine: node-1
      display: acm-0001-receiver
```

The manual is at [equipment.liken.sh](https://equipment.liken.sh/).
`plans/README.md` indexes the plans. `make test` runs every check CI
runs.
