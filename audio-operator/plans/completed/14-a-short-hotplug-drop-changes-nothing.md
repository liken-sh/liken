# A short hotplug drop changes nothing

Plan 14. Proposed and built on 2026-11-06. It is audio-operator's half
of
[display-operator plan 32](../../../display-operator/plans/completed/32-a-short-hotplug-drop-changes-nothing.md),
which holds the measurements and the reasons for the settle time. No
drill has run on real hardware.

## The problem

An A/V receiver in standby drops hot plug detect on a machine's HDMI
port for 0.22 to 2.08 seconds, three times in a burst, with a burst
every 30 to 70 seconds. The graphics driver clears the slot's ELD on
each drop and writes the same block again when the receiver answers,
and the card reports both changes on its control device. On a home
cluster on 2026-11-06, the operator read 1,544 ELD changes in 6 hours.
Twice in those hours, a pass read the slot during a drop, published it
with the `disconnected` and `no-monitor` taints, and set the `Sink`'s
`Connected` condition to `NoMonitor`, and the next pass, 2 to 3
seconds later, undid both. A `NoExecute` taint that a consumer does
not tolerate ends its pod.

The two channel layout changes in the same 6 hours, each of which
restarted PipeWire, were not drops. One came when the receiver turned
on and its ELD gave 7.1, and the other when it went to standby and the
ELD gave stereo. Those are real changes of the monitor behind the
slot, and the operator still applies them.

## The design

`monitorhold.go` holds an HDMI or DisplayPort slot whose ELD goes
empty. The reconcile pass reads the card through the hold, and the
hold answers the slot's last reading with a monitor until the ELD has
stayed empty for `monitorSettle`, 5 seconds, the same settle time
display-operator holds its connector for. So the screen and its
speakers report a monitor that left at the same time. A slot whose
monitor answers again inside that time ends the hold, and the slice,
the `Sink`, and the declaration PipeWire loads never see the drop. A
slot that is still empty at the end reads empty, and a timer at that
deadline wakes a pass.

The hold is the one debounce in the operator, at the read of the
card, so the slice, the `Sink` and `Source` resources, and the layout
all read the same answer. A change of the slot's ELD between two empty reads starts the hold
again, so a burst of drops that no pass reads lit between does not
outlast the settle time. Changes restart a hold only until the slot
has been empty for `monitorCeiling`, 15 seconds, the same bound as
display-operator's `hotplugCeiling`, so both report a monitor that
left within 20 seconds in the worst case and 5 seconds when the slot
is quiet.

An operator that starts while a slot is in a drop has no reading
with a monitor to hold. For the first 5 seconds after the start, a
pass that reads an HDMI slot empty that it never read with a monitor
publishes nothing, so the slice the previous operator published
stays, and a timer wakes the pass at the end of that time. The init
container reads the card through `awaitMonitors`: when a slot reads
empty, it waits up to 5 seconds for the slot's ELD to change and
reads again, so a drop at the start does not declare stereo. A
machine with nothing on an HDMI port pays that wait on each start
before PipeWire starts. The analog jack and the USB card are not
held: neither carries an ELD, and this drop is an HDMI fault.

## The drill

On a machine whose HDMI port feeds an A/V receiver, with the receiver
in standby, read the slice and the `Sink` of the HDMI slot for 10
minutes with `kubectl get -w`. The `ELD changed` lines continue in the
operator's log, and neither object changes. Then pull the HDMI cable.
The slot gains the `no-monitor` taint between 5 and 8 seconds later.
