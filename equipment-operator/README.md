# equipment-operator

A `Receiver` is an A/V receiver that a `liken` machine plays through.
The operator controls the receiver over the network and reports its
power, input, and volume. While a `Player` plays through it, the
operator turns it on, selects the machine's input, and changes its
volume from the room's remote.

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Receiver
metadata:
  name: theater
spec:
  denon:
    address: receiver.example
  volume:
    max: 70
  inputs:
    - name: MPLAY
      machine: node-1
      monitor: acm-0001-receiver
```

While a `Player` has a screen on the receiver, `media-operator`
writes the `Player`'s session into the `Receiver`'s `status.session`.
The session names the input, and `media-operator` writes a request
into it for each press of a volume or home key and for a `Player` that
turns off: `volumeAsk`, `inputAsk`, and `powerAsk`. The operator sends
each request to the receiver once, and reports the volume the receiver
reaches in `status.zones.main`. It does not connect to a message bus.

A `CECBus` is the CEC wire of one HDMI tree, which the operator uses
through a USB CEC adapter on one of the cluster's machines. In
`Listen` the adapter sends nothing and reports the devices it hears.
In `Control` it joins the bus as a playback device, announces the
machine's physical address, scans the bus, and reports each device's
address, name, vendor, and power:

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
the `Display` objects whose pictures reach it. For each edit of the
spec, the operator wakes the TV or puts it in standby once, as
`spec.power` says, and reads the power back to confirm it:

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

When the input of a `Receiver`'s session is connected to a `Display`
that a `Television` lists, the TV wakes with the receiver. A session
wakes the room in these cases:

* A `Play` starts on it.
* Its screen wakes.
* The remote's power button turns the receiver on.
* It appears with a `Play` or its screen already on.

Sessions that exist when the operator starts wake nothing and send
the receiver nothing. To wake the room, the adapter whose `spec.adapters`
entry names that `Display` sends the TV Image View On and then Active Source, so the TV
and the receiver switch to the machine's input. If another source
takes the input in the next 30 seconds, the adapter takes it back, at
most twice. The `Television`'s `status.session` holds the session, and
`status.wokeAt` and the `WakeApplied` condition report what the wake
did. The operator applies an edit of `spec.power` before a wake.

The manual is at [liken.sh/equipment](https://liken.sh/equipment/).
`plans/` holds the design documents. `make test` runs every check that
CI runs.
