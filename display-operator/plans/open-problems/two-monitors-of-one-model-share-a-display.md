# Two monitors of one model share a Display

A `Display` is named from `monitorID`: the manufacturer's PNP id, the
product code, and the monitor name. The name leaves out the serial
number, the node, and the connector. So two monitors of one model are
one `Display`, and the cluster reports one of them.

The collision takes three forms:

- **Two monitors of one model on one node.** The controller's pass
  keeps the connected connector that sorts last by name, so the choice
  is deterministic while both stay connected. The `Display` moves to
  the other connector while that one is dark. Only the chosen
  connector gets DDC actuation and `spec.mode`. The placement pass
  writes both screens' surfaces and layout to the one `Display`, one
  after the other.
- **Two cables from one node to one receiver.** The receiver serves
  the same EDID identity on each of its inputs, so this is the same
  case. The CEC setup of plan 23 needs only one cable, so it does not
  cause it. When the two cables carry different physical addresses,
  the `Display` refuses to publish either one as current instead of
  picking a guess.
- **Two monitors of one model on different nodes.** Both nodes write
  the one `Display`, and its `status.node` and `status.connector`
  alternate on every pass of either node, about every ten seconds.

After a restart, the link-history seed restores only the connector
that the `Display` records, so the other dark connector reads as empty
and gets tainted.

The name cannot simply gain the serial number. The same value is the
pairing identity `monitor.liken.sh/id` that audio-operator derives
from the HDMI ELD, and the scheduler compares the two byte for byte.
The ELD holds the manufacturer, the product code, and the monitor name,
and no serial number, so a pairing identity with a serial never
matches its speakers. So the `Display` name and the pairing identity
must become two values. A `Player`'s selector names the pairing
identity today, so two monitors of one model on one node also make
that selector match two devices.

Renaming a `Display` changes every existing name and every selector
and manifest that names one. The fix needs a plan that chooses the new
name (the serial when the EDID states one, the node and the connector
when it does not), keeps the pairing identity as it is, and moves the
existing names.
