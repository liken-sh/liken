## The resting layer

A declared field is a standing instruction. On every pass the
operator compares the declaration with the value it last saw, and it
writes the panel only where the two diverge, so a settled panel
costs nothing on the wire. A declared value is validated against
`status.capabilities`: a value the panel does not carry fails the
pass and is never written. An empty `spec` writes nothing at all.
The operator invents no value, ever: a panel with no declarations
keeps whatever its own menu holds.

A panel can take a write and read back another value. For example, a
panel can report its input under a code other than the one the
operator wrote. The operator writes such a value once for each
`metadata.generation`, records it in `status.unconfirmed` with the
value the panel read back, and does not write it again until `spec`
changes. The record is in status, so a restarted operator does not
write it again either.

A panel can also take the write, confirm it, and later change the
value by itself. For example, a panel can switch to the input that
carries a signal. The operator writes a declared value back at most
three times in one generation after the first write, and counts the
writes in `status.written`. After the third write back, it records
the value in `status.unconfirmed` with a message that says the panel
keeps changing it, and it writes the value again only after `spec`
changes. A person who changes a value at the panel's own buttons
uses the same count.

Each write to a panel prints one line in the operator's log, with
the value before and the value after.

## The override

`spec.override` holds a temporary state above the resting layer, the
way `kubectl cordon` holds `spec.unschedulable` above a `Node`'s
definition. A writer adds the block, and the operator obeys it. The
writer deletes the block, and the operator restores the panel: to
the resting declaration where `spec` states one, otherwise to the
value it captured.

The capture is the load-bearing step. Before the operator obeys
`backlight: Off`, it reads the panel's brightness and writes the
value to `status.captured`, and only a committed capture is followed
by the write that darkens the panel. A capture in `etcd` survives an
operator restart, a pod move, and a reboot, so the restore does too.
The restore retries until the panel reads back the value, because a
panel that is waking answers late. It makes at most eight writes of
each control, over about 90 seconds. A restore that runs out of
writes keeps `status.captured`, and `status.unconfirmed` names the
control. An edit to `spec` starts the restore again.

An override has no timeout. If the writer that set one crashes, the
panel stays dark until the writer returns or a person deletes the
block. That failure is visible: `kubectl get display` shows the
standing override, and the block's field manager names the writer
that owes the lift.

## The resting mode

`spec.mode` follows the resting pattern with one difference in
weight: a mode lands through the compositor, and applying it
restarts the compositor once, which ends every Wayland client on
the card. So the operator applies a resting mode only while no
claim holds the screen. A claim's own `mode` parameter wins for the
claim's lifetime, a `spec.mode` edit during a claim waits for the
claim to end, and the unprepare that frees the screen restores the
declaration promptly.

A new operator pod starts the compositor at each monitor's resting
mode, so the screen takes one modeset, not a modeset to the
preferred mode and a second one to `spec.mode`. When the compositor
serves another mode after its restart, the operator records the
mode in `status.unconfirmed` and does not restart the compositor for
it again until `spec` changes.

## The two values of the mode

`status.mode` reports the mode twice, because two parties each
report one and they can disagree. `kernel` is the mode the graphics
card is synced to on the connector. `weston` is the mode the
compositor lays canvases out at, read from the compositor's own
`wl_output` events over a standing connection the operator holds.
A client draws at the second one. When the two values differ, the
clients on that screen are drawn at the wrong size, and the
operator restarts the compositor to correct it once the screens are
free. `weston` is absent while the operator holds no connection to
a compositor. `kernel` is absent while the connector drives nothing,
and also while the operator holds no connection to a compositor,
because the operator opens the card only while it holds a connection
to a compositor. `kubectl get displays` shows the two as the `MODE`
and `CANVAS` columns.

## The physical address

`status.physicalAddress` is the HDMI-CEC physical address of the
port this machine's cable is in: four hex digits that give the path
from the TV, one digit for each HDMI port on the way. A machine on
input 2 of a receiver that is on the TV's input 1 reads `1.2.0.0`.
An HDMI sink serves each of its ports an EDID whose vendor block
states that port's address, and the operator reads it from the EDID
on the connector. A CEC adapter on the machine has no EDID of its
own, so it announces this address when it sends `Active Source`,
and the receiver and the TV switch to the machine's picture. The
operator does not open a CEC adapter or send a CEC message.

A receiver in standby can stop serving its EDID, or pass the TV's
EDID through, which belongs to another `Display`. The machine's port
has not moved, so the field keeps the last valid address, and the
`PhysicalAddressCurrent` condition states where the value came
from:

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `ReadFromEDID` | The connector's current EDID serves this address. |
| `False` | `Retained` | The connector serves no EDID for this monitor, or serves no valid address in it. The message names the time it stopped serving the address. |
| `False` | `Ambiguous` | Two connected connectors serve this monitor with different addresses. The message names both connectors and both addresses. |

The condition is absent while the monitor has never served a valid
address, for example on a DisplayPort cable. `0.0.0.0` is the TV's
own address, which a sink serves when it states none, and `f.f.f.f`
is the invalid address, so the operator publishes neither. It also
refuses an address with a non-zero digit after a zero, such as
`1.0.2.0`, because a zero ends the path. The output device publishes
the same value as its `physicalAddress` attribute, from the current
EDID only, so a dark connector publishes none. Read the retained
value from the `Display`.

The address belongs to one connector, and a `Display` has one
connector. Two connectors on one machine that serve EDIDs with the
same identity, such as two cables to one receiver, map to one
`Display`, and it reports the connector that sorts last by name
among those that are connected.

Those two connectors can serve different addresses, for example one
cable that carries the picture into a receiver input and a second,
through a CEC adapter, into another input of the same receiver. When
they disagree, the `Display` publishes no current address:
`PhysicalAddressCurrent` reads `Ambiguous`, neither connector's
device states a `physicalAddress` attribute, and `status.physicalAddress`
keeps the value it held before the two disagreed.

## Shared screens

A monitor with several inputs dims all of them at once, because
brightness and power are panel-global, and the operator writes what
an override states whenever the panel answers. Panels do not say
reliably which input they show: the query is optional, and a panel
can answer it with the name of the port the question arrived on. So
whether a screen should ever go dark is its owner's declaration,
not the operator's guess. State it in the layer that writes the
override; the media operator's `Player` carries an idle policy
whose `offAfterSeconds: 0` keeps a shared screen's panel untouched.

## Observed values

`status.observed` is what the operator last read or wrote. The
operator touches the wire when it probes, when it captures before
an override, when it actuates, and about every ten seconds for a
panel that is lit and under no override. That last read is what
finds a change a person made at the panel's own menu, and it is
what makes
a resting declaration hold: the pass that finds the divergence
writes the declaration back, up to three times in one generation. A panel in standby or off, a panel an
override holds, and a panel that answers nothing are never read on
a timer, because a DDC/CI read is itself a wake stimulus on some
panels, and a polling loop would relight the screens the override
layer darkened. For those panels, `observed` stays what the
operator last saw.

## One writer per wire

The operator is the one process that writes a panel's i2c wire for
the `Display`: resting declarations, overrides, and restores all
land through the same reconciler. The
[one-writer rule](/docs/reference/devices/#the-control-device) in
the devices reference still governs the other paths: a pod that
holds a connector's control device owns that wire while it runs, so
do not declare resting values or write overrides for a screen whose
control device a pod holds.
