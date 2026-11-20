# equipment-operator

`equipment-operator` lets you read and control a room's A/V receiver
and TV through the Kubernetes API.

- A `Receiver` shows the receiver's power, input, volume, and
  settings. You set its power and its settings in the same manifest.
- A `Television` shows the TV's power and the input it last switched
  to. You set its power the same way.

When a `Player` plays through this equipment, the operator turns the
equipment on and switches the TV and the receiver to the machine's
input. It also sets the receiver's volume when someone presses the
volume keys on the `Player`'s remote.

The operator controls a receiver over the network, with the
receiver's own protocol. For a Denon or Marantz receiver, that is the
Denon control protocol on TCP port 23. For a WiiM device, it is the
WiiM HTTPS API. The operator controls a TV over HDMI-CEC, through a
USB-CEC adapter plugged into a `liken` machine. It finds the WiiM
devices on the LAN by itself and creates a `Receiver` for each one,
unless you turn discovery off.

The operator does not play media, draw on a screen, or set the volume
of a speaker that `liken` drives. It does not control lights or other
home automation.

## What each kind needs

A network `Receiver` does not need a `liken` machine. The operator's
`Deployment` runs as an unprivileged pod on the host network. It
claims no device and connects to the receiver over the LAN.

A `CECBus` and a `Television` need a `liken` machine with a USB-CEC
adapter. `liken` publishes the adapter as a device, and a pod of the
operator's `DaemonSet` claims it. A bus in `Control` mode also needs
the `Display` that `display-operator` publishes for the machine's
video output. The adapter announces that `Display`'s physical
address on the bus.

`media-operator` uses this operator for each `Player`. It writes the
`Player`'s session and each key press into the `Receiver`'s status.
When someone selects the machine's input on the TV, the operator
records that request, and `media-operator` passes it on to the
`Player`'s screen.

## The kinds

- **`Receiver`** is an A/V receiver on the network. It has exactly
  one protocol block, `spec.denon` or `spec.wiim`, with the address
  and the settings that only that brand has. `spec.inputs` says which
  `liken` machine is connected to which input. `spec.zones` controls
  a second and a third zone. The status shows each zone and the
  brand's own settings.
- **`CECBus`** is the CEC wire of one HDMI tree, with the adapters
  connected to it. In `Listen` mode, each adapter sends nothing and
  lists the devices it hears. In `Control` mode, each adapter joins
  the bus as a playback device, scans the bus, and reports each
  device's address, name, vendor, and power. When an adapter is in no
  `CECBus`, the operator creates a `CECBus` in `Listen` mode for it.
- **`Television`** is the TV at the root of one bus. It shows the
  TV's power, the last active source, and the `Display` objects whose
  picture reaches the TV. The operator applies each edit of
  `spec.power` once. For each bus in `Control` mode whose TV has no
  `Television`, the operator creates one. To take it over, apply your
  own `Television` with the same name.

## An example

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Receiver
metadata:
  name: theater
spec:
  denon:
    address: receiver.example
    settings:
      audyssey:
        dynamicEq: true
  power: "On"
  volume:
    max: 70
  inputs:
    - name: MPLAY
      machine: node-1
      monitor: acm-0001-receiver
```

Put `"On"` in quotes, because `kubectl` reads an unquoted `On` as the
boolean `true`.

The manual is at [liken.sh/equipment](https://liken.sh/equipment/).
The design is in [`plans/00-design.md`](plans/00-design.md).

MIT. See [LICENSE](LICENSE).
