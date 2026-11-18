## The declared settings

The fields of `spec` declare the values you want a panel to keep. On
every pass the operator compares each declared value with the value
it last read from the panel, and it writes the panel only when they
differ. It first checks the value against `status.capabilities`, and
when the panel does not support it, the pass fails and nothing is
written. An empty `spec` makes no writes, so a panel with no
declarations keeps whatever its own menu sets.

Some panels accept a write and then report a different value, such
as an input under a different code from the one the operator wrote.
The operator writes such a value once for each `metadata.generation`
and records it in `status.unconfirmed`, with the value the panel
reported. It doesn't write the value again until `spec` changes. The
record is in status, so a restarted operator doesn't write it again
either.

Other panels confirm a write and later change the value on their
own, for example by switching to the input that has a signal. After
the first write, the operator writes a declared value back at most
three more times in one generation, and counts the writes in
`status.written`. After the third, it records the value in
`status.unconfirmed` with a message that the panel keeps changing it,
and writes it again only after `spec` changes. A change that a person
makes with the panel's buttons counts the same way.

Each write to a panel adds a line to the operator's log, with the
value before and after the write.

## The override

`spec.override` sets a temporary state that takes priority over the
rest of `spec`, much as `kubectl cordon` sets `spec.unschedulable` on
a `Node` without changing the rest of it. A controller or a person
adds the `override` block, and the operator applies it. When the
block is deleted, the operator restores the panel to the value that
`spec` declares, or, when `spec` declares none, to the value it
captured before the override.

Before the operator applies `backlight: Off`, it reads the panel's
brightness and writes it to `status.captured`. It writes the panel
only after the API server has saved the capture. The capture is in
`etcd`, so the restore still works after an operator restart, a pod
move, or a reboot.

A panel that is waking up can respond late, so the restore retries
until the panel reads back the value. It makes at most eight writes
of each control, over about 90 seconds. When the restore runs out of
writes, `status.captured` stays in place and `status.unconfirmed`
names the control. An edit to `spec` starts the restore again.

An override has no timeout. When the controller that set an override
crashes, the panel stays dark until the controller returns or a
person deletes the block. `kubectl get display` shows the active
override, and the field manager of the `override` block names the
controller that has to remove it.

## The declared mode

`spec.mode` works like the other declared values, except that the
compositor applies a mode, and applying it restarts the compositor.
That ends every Wayland client on the graphics card. So the operator
applies `spec.mode` only while no claim holds the screen. A claim's
own `mode` parameter takes priority for as long as the claim exists.
When you edit `spec.mode` during a claim, the operator applies the new
mode as soon as the kubelet unprepares the claim and frees the screen.

A claim holds the screen while a pod that is not being deleted holds
the claim, even before the claim's prepare succeeds. The kubelet
retries a failed prepare, and each prepare switches the screen to the
claim's mode. If the operator applied `spec.mode` between two
retries, it would restart the compositor once for each retry.

A new operator pod starts the compositor at each monitor's
`spec.mode`, so the screen changes mode once at startup, not first to
the preferred mode and then to `spec.mode`. When the compositor runs
a different mode after its restart, the operator records the mode in
`status.unconfirmed` and doesn't restart the compositor for that mode
again until `spec` changes.

When the compositor crashes, the kubelet delays its next start with a
crash backoff. The operator waits up to six minutes for the
compositor to start, and then gives it 10 seconds to serve the mode.

## The two values of the mode

The kernel and the compositor each report a mode, and they can
disagree, so `status.mode` has both. `kernel` is the mode that the
graphics card drives on the connector. `weston` is the mode that the
compositor lays out canvases at, which the operator reads from the
compositor's `wl_output` events. Clients draw at the `weston` mode,
so when the two differ, clients on that screen draw at the wrong
size. The operator then restarts the compositor to correct it, once
no claim holds the screen.

`weston` is absent while the operator has no connection to a
compositor. `kernel` is absent while the connector drives no display,
and also while the operator has no connection to a compositor,
because the operator opens the graphics card only while it has that
connection. `kubectl get displays` shows the two values in the `MODE`
and `CANVAS` columns.

## The physical address

`status.physicalAddress` is the HDMI-CEC physical address of the port
that this machine's cable is plugged into. Its four hex digits give
the path from the TV, one digit for each HDMI port on the way. A
machine on input 2 of a receiver that is on the TV's input 1 reads
`1.2.0.0`.

An HDMI sink serves an EDID on each of its ports, and the EDID's
vendor block has that port's address. The operator reads the address
from the EDID on the connector. A CEC adapter on the machine has no
EDID of its own, so the program that drives the adapter needs this
address to send `Active Source`, which switches the receiver and the
TV to the machine's picture. `display-operator` itself does not open
a CEC adapter or send CEC messages.

A receiver in standby can stop serving its EDID, or pass through the
TV's EDID, which belongs to another `Display`. The machine's port has
not moved, so `status.physicalAddress` keeps the last valid address,
and the `PhysicalAddressCurrent` condition says where the value came
from:

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `ReadFromEDID` | The connector's current EDID has this address. |
| `False` | `Retained` | The connector serves no EDID for this monitor, or the EDID has no valid address. The message gives the time that the connector stopped serving the address. |
| `False` | `Ambiguous` | Two connected connectors serve this monitor with different addresses. The message lists both connectors and both addresses. |

The condition is absent while the monitor has never served a valid
address, for example on a DisplayPort cable. The operator never
publishes `0.0.0.0`, the TV's own address, which a sink serves when
it has no address to give, or `f.f.f.f`, the invalid address. It also
refuses an address with a non-zero digit after a zero, such as
`1.0.2.0`, because a zero ends the path.

The output device has the same value in its `physicalAddress`
attribute, but only while the current EDID has it, so a connector with
no signal publishes none. To read the retained value, read the
`Display`.

When two connectors on one machine serve EDIDs with the same
identity, such as two cables to one receiver, the operator creates
one `Display` for them. It reports whichever connected connector has
the name that sorts last.

The two connectors can serve different addresses, for example when
one cable carries the picture into one input of a receiver and a
second cable, through a CEC adapter, goes into another input. Then
neither address is right, so the `Display` publishes no current
address. `PhysicalAddressCurrent` reads `Ambiguous`, neither
connector's device has a `physicalAddress` attribute, and
`status.physicalAddress` keeps the value it had before the connectors
disagreed.

## Shared screens

Brightness and power apply to the whole panel, so dimming a monitor
dims every input on it. The operator can't tell which input a panel
is showing, because that query is optional in DDC/CI, and a panel can
answer it with the port that the query arrived on. So the operator
applies an override whenever the panel responds, and the component
that writes the override decides whether a shared screen may go dark.
For example, the `Player` of `media-operator` has an idle policy, and
`offAfterSeconds: 0` in it leaves the panel of a shared screen alone.

## Observed values

`status.observed` holds the values that the operator last read from
the panel or wrote to it. The operator reads or writes the panel when
it probes the panel, when it captures a value before an override, and
when it applies a value. It also reads a panel every 10 to 20 seconds
while the panel is on, responds, and has no override. That read finds
a change that a person made in the panel's own menu, and the same pass
writes the declared value back, up to three times in one generation.

The operator doesn't read a panel on a timer while it is in standby
or off, while an override or a restore applies to it, or while it
doesn't respond. A DDC/CI read wakes some panels, so polling them
would light up screens that an override turned dark. For those
panels, `observed` keeps the values the operator last read.

## Arrangers

The `Layout` that `spec.layout` names divides the screen into regions
and gives each claim a region. Inside its region, one client of the
claim can place the claim's own windows through the
`liken_arrangement_v1` Wayland protocol. That client is the claim's
arranger. The [arrangement reference](/docs/reference/arrangement/)
describes the protocol for the author of a workload.

`status.arrangers` lists each claim on the screen that has an
arranger, with the region that shows the claim and the name of the
arrangement that the arranger committed last. Each window in
`status.surfaces` has the `appID` that an arrangement uses to name
it. `duplicateAppIDs` lists each `app_id` that more than one window of
the claim has, because one entry of an arrangement moves all of those
windows:

```yaml
status:
  surfaces:
    - id: film-cla-4
      claim: media/film
      appID: liken-film
      region: main
    - id: film-cla-5
      claim: media/film
      appID: liken-display
      region: main
  arrangers:
    - claim: media/film
      region: main
      pods: [media/player-0]
      arrangement: credits
```

The arranger is a process in one of the pods that hold the claim.
`status.arrangers` lists those pods, and gives no pid or container,
because the compositor reads a client's pid from the socket, and the
kernel reports pid 0 for a client in another pod's pid namespace.

## One writer per wire

Declared values, overrides, and restores all go through the operator,
so it is the only process that writes a panel's DDC/CI bus for a
`Display`. A pod that holds a connector's
[control device](/docs/reference/devices/#the-control-device) writes
that bus itself while it runs. Don't set declared values or write
overrides for a screen whose control device a pod holds.

## Events

The operator posts an `Event` on a `Display` for each condition that
changes and for each action it takes on the screen. The API server
deletes an `Event` one hour after its last write. A `Display` is
cluster-scoped, so its `Event`s are in the `default` namespace.
`kubectl describe display` shows them, and `kubectl events` shows them
only with `-n default` or `-A`:

    kubectl events -n default --for display/boe-1080-display

A condition change posts one `Event` with the condition's reason and
message. A new message with the same status and reason posts nothing.
The operator posts the `Event` after the status write succeeds, so a
write that the API server refuses posts nothing. When the same
`Event` is posted again within 10 minutes, the operator adds to the
count of the first one, and `kubectl describe` shows it once, with
the count.

| Reason | Type | Condition or action |
| --- | --- | --- |
| `PanelAttached` | Normal | `Connected` is `True`: a panel is on the connector. |
| `NoPanel` | Normal | `Connected` is `False`: the connector has served no EDID for this monitor for 5 seconds. |
| `AnswersDDC` | Normal | `Responsive` is `True`. |
| `NoDDCReply` | Normal | `Responsive` is `False`. A panel in standby does not respond to DDC/CI. |
| `Serving` | Normal | `CompositorServing` is `True`. |
| `Down` | Warning | `CompositorServing` is `False`: the compositor's socket refuses the connection. |
| `Hung` | Warning | `CompositorServing` is `False`: the socket accepts the connection and the compositor does not respond. |
| `ReadFromEDID` | Normal | `PhysicalAddressCurrent` is `True`. |
| `Retained` | Normal | `PhysicalAddressCurrent` is `False`, and `status.physicalAddress` keeps the last valid address. |
| `Ambiguous` | Warning | `PhysicalAddressCurrent` is `False`: two connectors serve this monitor with different addresses. |
| `LayoutFound`, `DefaultLayout` | Normal | `LayoutResolved` is `True`. |
| `LayoutNotFound` | Warning | `LayoutResolved` is `False`: no `Layout` has the name in `spec.layout`. |
| `WriteUnconfirmed` | Warning | A write is new in `status.unconfirmed`. The message gives the control, the value, and the value the device read back. |
| `ModeChanged` | Normal | A mode change restarts the compositor, and every screen on the card goes blank. The message gives the mode before and after. |
| `CompositorKilled` | Warning | The compositor did not respond for 10 seconds, and the operator ended it. The compositor's container starts it again. The `Event` is on every `Display` of the node. |
| `PanelStandbyFailed` | Warning | The panel did not enter standby after its last claim ended, and stays on. The operator does not try again. |
| `Captured` | Normal | A capture through the API returned bytes. The message gives the subject, the aspect, and the media type. |
| `ArrangerGranted` | Normal | A client of a claim became the claim's arranger, and is new in `status.arrangers`. The message gives the claim, the pods that hold it, and the region. |
| `ArrangerLost` | Warning | A claim's arranger disconnected while a pod that holds the claim still runs and is not being deleted, as when the client crashes. The claim's windows return to fill the region. |
| `ArrangerReleased` | Normal | A claim's arranger destroyed its arranger object and gave up the region, or it disconnected after the pods that held the claim were gone or being deleted. |

An arranger that commits a new arrangement posts no `Event`, because
a commit follows each key press. `status.arrangers` shows the name of
the last arrangement. A compositor restart ends every arranger, and
the pass that reads the new compositor posts no `ArrangerLost` for
them, because `CompositorServing` or `CompositorKilled` already
reports the restart.
