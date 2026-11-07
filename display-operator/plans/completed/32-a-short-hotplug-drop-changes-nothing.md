# A short hotplug drop changes nothing

Plan 32. Proposed and built on 2026-11-06. It holds each reader of the
card through a drop of hot plug detect shorter than 5 seconds.
audio-operator holds an HDMI slot's ELD the same way, in its own plan
14, and with the same settle time.
No drill has run on real hardware. The drill is the one under "The
drill" below, on a machine whose HDMI port feeds an A/V receiver.

## The problem

The open problem
[A receiver pulses the hotplug line for hours](../open-problems/a-receiver-pulses-the-hotplug-line-for-hours.md)
records an A/V receiver in standby that drops hot plug detect on a
machine's HDMI port, three times in a burst, with a burst every 30 to
70 seconds. When it was written, the operator's passes found nothing
to write during a spell. On 2026-11-06 and 2026-11-07, read on a home
cluster from the logs and the objects alone, a spell made the
operators write on every burst:

- The port's `Display` took about 38 status writes in 240 seconds.
  The placement pass cleared `status.surfaces` and `status.layout` on
  each drop and filled them in again, the same, after the return.
- About 11 times an hour, a Display controller pass read the
  connector during a drop. The `Display` turned `Connected` False with
  `NoPanel` and `PhysicalAddressCurrent` False with `Retained`, and
  back to `PanelAttached` and `ReadFromEDID` on the next pass, with an
  `Event` for each. media-operator's `Player` followed with
  `PanelAway` and `Present`.
- audio-operator read 1,544 ELD changes and 1,544 jack changes in 6
  hours, one pair for each `drm change` uevent display-operator read.
  Twice in those 6 hours a pass read the slot during a drop and
  published it with the `no-monitor` taint for 2 to 3 seconds.
- equipment-operator's `CECBus` took about 30 writes in 240 seconds,
  and the `Television` about 12. The TV's vendor changed from
  `00903e` to `000000` at the first drop of a burst and back at a
  later one. That is a fault of its own, fixed in equipment-operator's
  CEC directory, where a vendor of `000000` now reads as unknown.

The drops are short. Weston logged each disconnect and connect of the
port. In about 6 hours of its log, 709 drops lasted between 0.22 and
2.08 seconds. Half lasted under 0.24 seconds, and 99 in 100 under
0.75 seconds. The longest, 2.08 seconds, came when the receiver
turned on. Between the drops of one burst, the port stayed connected
for up to 6.9 seconds, and between bursts for 31 seconds or more.

The bursts stopped from the moment the receiver and the TV turned on
until the moment they went to standby, about 2 hours 40 minutes
later. So the receiver in standby drives the cycle. In the two bursts
that a watch of the `CECBus` covered, the TV's vendor read `000000`
from the first drop of the burst, so the TV answers differently while
the receiver cycles its link; which device sent that report is not
known. Nothing in `liken` runs on a 30 to 70 second clock that the
bursts follow. The open problem's question stands: whether weston's
mode set after each connect makes the receiver drop the line again.

## The design

### One hold for the readers of one walk

`hotplughold.go` holds a connector that goes dark. Each reader reads
the card through a hold, and the hold answers the connector's last
lit reading, with `Held` set, until the connector has stayed dark
for `hotplugSettle`. A connector that answers again inside that time
ends the hold, and no reader sees the drop. A connector that is
still dark at the end reads dark, and a timer at that deadline wakes
the slice publisher's loop through the channel a failed write
retries on. That pass wakes the Display controller, and the
placement pass runs after it, so a monitor that really left is
reported then.

The hold sits where each reader composes its view of the card, not
on the uevent. The Display controller and the placement pass read
one walk, `screensOf`, through one view of the hold. The slice
publisher composes its own walk with the controls and the card's
current mode, so it reads through a second view, and a held connector
publishes the device it published while lit. Each view keeps its own
lit reading, and the hold keeps one dark time for each connector that
both views share, so the `Display`, its windows, and the slice report
a monitor that left at one deadline, whichever reader read it dark
first.

The slice publisher's link history (`links.go`) reads the wire before
the hold does. A monitor that leaves and a different one that answers
inside the hold is then still a replacement, and the device taints for
it, because the claim on the connector asked for the monitor that
left. A lit reading always passes through the hold as it is, so no
reader shows a different monitor as the held one.

- The Display controller counts a held connector as present, so the
  sweep for panels that left does not write it, and it skips the
  panel: no DDC/CI read, no mode, no status write. The panel is not
  on the wire, and weston has destroyed its output, so a status
  composed now would report no mode.
- The placement pass keeps a held connector in its list of wired
  Displays, so no sweep clears its windows. Weston destroys the output
  during the drop and the layout module reports it gone, so no screen
  report writes the Display. When the output returns, the pass places
  the windows again, and the report it composes is the one already
  stored, so it writes nothing.
- media-operator reads the `Display`'s `Connected` condition, so the
  `Player`'s `Screen` condition holds with it.

### The settle time

`hotplugSettle` is 5 seconds. That is more than twice the longest
drop above, and the same window the canvas heal already waits for
the compositor's outputs to stop moving (`canvasSettleWindow`). A
cable that somebody pulls out is reported 5 to 8 seconds later. The
hold starts at the first pass that reads the connector dark, which
the layout module's report of the destroyed output wakes at once and
the uevent wakes after the slice publisher's settle window of 1.5
seconds. The pass after the deadline waits for that window again.

The hold covers each drop alone. A burst of three drops spread over
7 seconds is three holds, and each one ends when the port answers
again. When no pass reads the port lit between two drops, the hold
learns of the return from the uevents: the operator counts each drm
uevent before it wakes a pass, and a dark read that finds the count
moved since the last dark read starts the hold again. A uevent that
carries the `CONNECTOR` property names the connector by its DRM object
id, which sysfs publishes in each connector's `connector_id` file, and
counts for that connector alone. A uevent with no `CONNECTOR` names
only the card, so it counts for every connector of the card, and a
neighbour that flaps on the same card would start the hold again for
as long as it flaps. So events start a hold again only until the
connector has been dark for `hotplugCeiling`, 15 seconds, which covers
a burst of 7 seconds twice over. A monitor that left is reported
within 20 seconds of the first dark read in the worst case, and 5
seconds after it when the card is quiet.

An operator that starts while its screen is in a drop reads the
connector dark before it has read it lit. The link history starts
from the `Display` resources (`links.seed`), and the screens' view
starts from the monitor that history remembers on each dark
connector, so the first dark read starts a hold as it would for a
drop the process saw begin. The slice's view needs no seed, because
the link history already publishes a dark connector's remembered
monitor with no taint.

## Considered and set aside

- **A swap with no dark read between.** The link history marks a
  replacement only when it reads the connector dark between two
  monitors. A swap that finishes inside the slice publisher's settle
  window of 1.5 seconds gives one pass that reads one monitor lit and
  the next pass the other, so the device does not taint. Treating
  every change of identity between two lit reads as a replacement
  closes that gap, and was not built: plan 23 records that a receiver
  in standby can pass the TV's own EDID through, which has another
  identity, so the rule would end the pods on the screen each time
  such a receiver turned on or off. The receiver measured here served
  its own EDID in all 709 returns, standby and on, so it does not
  decide the question for other receivers.

- **A longer settle window on the uevents.** The slice publisher
  already waits for 1.5 seconds of quiet. The placement pass does not
  wait at all, because a window that arrives is a film's first frame,
  and the Display controller wakes on the Display watch too. A window
  on the uevents delays every reader for every event and still lets
  a pass that another source wakes read the connector during a drop.
- **Keying audio-operator off the `Display`.** audio-operator watches
  no `Display`, and its witness for a monitor is the ELD that the
  graphics driver writes. A pass that read the `Display` would add a
  watch and a dependency on another operator's timing for a fact the
  card already states. So audio-operator holds its own reading with
  the same settle time.
- **A hold inside `discoverOutputs`.** The sysfs walk knows the
  connector and the EDID, and not the mode the card runs or what the
  compositor serves. A held connector would still lose its current
  mode, and the slice and the `Display` would still change.

## The drill

On a machine whose HDMI port feeds an A/V receiver, with the receiver
and the TV in standby, read the `Display`, the `Player`, and the slice
for 10 minutes with `kubectl get -w`. The `drm change` lines continue
in the operator's log, and no object changes. Then pull the HDMI
cable. The `Display` turns `Connected` False with `NoPanel` between 5
and 8 seconds later.
