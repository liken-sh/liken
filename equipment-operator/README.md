# equipment-operator

`equipment-operator` lets you read and control a room's A/V receiver
and TV through the Kubernetes API. A `Receiver` shows the receiver's
power, input, volume, and settings, and you set its power and its
settings in the same manifest. A `Television` shows the TV's power
and the input it last switched to, and you set its power the same
way. The operator does not play media, draw on a screen, or set the
volume of a speaker that `liken` drives. It does not control lights
or other home automation.

The operator controls a receiver over the network, with the
receiver's own protocol, such as the Denon control protocol. The
operator's `Deployment` runs as an unprivileged pod on the host
network, so a network `Receiver` needs no `liken` machine. The
operator can find some receivers on the LAN by itself and create a
`Receiver` for each one, unless you turn discovery off. The
`Receiver` reference names the receivers that the operator supports.
The operator controls a TV over HDMI-CEC, through a USB-CEC adapter
plugged into a `liken` machine. `liken` publishes the adapter as a
device, and a pod of the operator's `DaemonSet` claims it.

When a `Player` of `media-operator` plays through this equipment, the
operator turns the equipment on and switches the TV and the receiver
to the machine's input. `media-operator` writes the `Player`'s
session and each key press into the `Receiver`'s status, so the
operator sets the receiver's volume when someone presses the volume
keys on the `Player`'s remote. When someone selects the machine's
input on the TV, the operator records that request, and
`media-operator` passes it on to the `Player`'s screen. A `CECBus` in
`Control` mode needs the `Display` that `display-operator` publishes
for the machine's video output, because the adapter announces that
`Display`'s physical address on the bus.

## The kinds

- **`Receiver`** is an A/V receiver on the network. It has exactly
  one protocol block, such as `spec.denon`, with the address and the
  settings that only that brand has. `spec.inputs` says which
  `liken` machine is connected to which input. `spec.zones` controls
  the receiver's other zones. The status shows each zone and the
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
