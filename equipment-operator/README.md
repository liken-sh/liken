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

A `Television` is the TV at the root of that tree. When a `CECBus` in
`Control` finds a TV, the operator creates a `Television` with the
bus's name. To adopt it, apply your own `Television` under that name.
The `Television` reports the TV's power, the last active source, and
the `Display` objects whose pictures reach it. Each edit of the spec
wakes the TV or puts it in standby once, as `spec.power` asks, and the
operator reads the power back to confirm it:

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Television
metadata:
  name: den
spec:
  cec:
    bus: den
  power: "On"
```

Quote `"On"`, because `kubectl` reads an unquoted `On` as the boolean
`true`.

When a `Receiver`'s session wakes the room on an input whose monitor
is a `Display` that a `Television` lists, the TV wakes with the
receiver. A session wakes the room when a Play starts on it, when its
screen wakes, when the remote's power button turns the receiver on, or
when it appears with a Play or its screen already on. The sessions the
operator finds when it starts wake nothing. The adapter that speaks for that `Display` sends the TV
Image View On and then Active Source, so the TV and the receiver show
the machine. For 30 seconds after, it takes the input back from
another source that claims it, at most twice. `status.session` holds
the session, `status.wokeAt` and the `WakeApplied` condition report
what the wake did, and an edit of `spec.power` goes before a wake.

The manual is at [equipment.liken.sh](https://equipment.liken.sh/).
`plans/README.md` indexes the plans. `make test` runs every check CI
runs.
