# 17, A session freezes its devices

Proposed and built on 2026-10-11 to 2026-10-12. Drilled on liken-1 on
2026-10-12, with INDI simulators; the "Third drill" in [plan
16](16-kinds-are-indi-interfaces.md#third-drill) records the results.
This plan replaces the live moves that [plan
11](11-devices-join-a-running-server.md) built.

While a `Reservation` holds a server, the server runs the devices it
started and no others. A `Reservation` records in `status.session`
what its session acts on, and a spec edit that would move a device
takes effect at the next session.

## The problem

Plan 11 let a device join or leave a running server, so a person could
move a device between telescopes while a reservation was `Ready`. After
plan 16, four review rounds kept finding bugs in that code, and each
fix opened new interleavings:

- A device moved into a telescope that was still activating never got
  a driver, so the reservation failed at its step deadline.
- A device moved off a telescope whose session was `Failed` or still
  activating never reached its new server, because only a `Ready`
  session released the devices that left it.
- A new server adopted the pod of a device that moved to it, and the
  old server's release then disconnected the device and deleted the
  pod.
- Two releases shared one output, and the first switched it off under
  the second.
- A joining device's power output was switched on by no step.

Every one of these comes from the same rule: a device can change
servers at any moment, so every step has to agree with every other
step on where the device is at that moment.

## The decision

Remove live moves. From `PowerOn` (and `StartSite` for the
observatory's server) until the reservation is `Released`, a server's
devices are the ones the session started.

- **The record.** Before any pod exists, each step writes its part of
  the session once into the `Reservation`'s `status.session`: each
  device's parents, driver, power, claim, and whether a `Guider`
  guides with it, plus the telescope's trains, the optics of the tubes,
  the `Guider`s, and the observatory. Trains, tubes, and `Guider`s
  carry their UIDs, so an object deleted and created again under the
  same name does not count as the recorded one. Until the session
  ends, the operator reads the record and not the spec.
- **A move waits.** A spec edit that moves a held device (another
  telescope, another train, or the shelf) leaves it running where it
  is. Its `ParentFound` condition has the reason `MovePending` and says
  where it goes, and the condition's transition posts one `Event`. The
  release stops it as one of its own, and the next session starts it
  in its new place.
- **A new device waits.** A device created during a session does not
  start. It is `Idle`, and its `Ready` message names the reservation it
  waits for.
- **A delete leaves at once.** A deleted device runs its
  `deactivation`, and its pod stops with the others.
- **A retry records again.** A retry of a failed activation step
  records the session from the current specs, because the old record
  holds the spec that failed. The steps then rerun from the first step
  that reads a part of the record that changed, and a device the new
  record leaves out leaves the session through the delete path.

These stay live: a device's settings, every resource's procedures, the
`Observatory`'s location, and a `Reservation`'s holder, start, and end.
A person changes them during a session on purpose, and the operator
writes them from the spec when a driver comes back.

The record lives in the `Reservation`'s status for three reasons. It
outlives the pods, so a server pod that goes during a session comes
back with the same devices, and a release still finds the outputs it
must switch off. Each object has one writer, the reservation's own
steps. And the observatory server's devices are the union of the site
records of every reservation that holds it, so overlapping
reservations end in any order without a shared writer.

## What a person loses

Pulling a device off a running telescope now means ending the
reservation or deleting the device. A move or a new device shows
`MovePending` or `Idle` with the reservation it waits for.

## What was built

| Commit | Change |
|---|---|
| e3ed4492 | A held server's devices are the ones it started; a spec edit that moves one takes effect at the next session, and a new device waits. The code that moved and joined devices mid-session is gone. |
| fde466c1 | Each session wrote the parents, driver, power, claim, and guide role of its devices on its pods, after a review found five places that still read a spec during a session. |
| cf62fbdb | The record holds the trains, tube optics, and `Guider`s too, so an edit of them replaces no PHD2 pod and loses no calibration. |
| ddf674e2 | The record moved from the pods to `status.session` of the `Reservation`, so it outlives the pods. |
| 5e8a5aee | A retry of a failed activation step records the session again from the fixed spec, once a server has a record the record alone decides membership, and recorded objects carry UIDs. |
| 3a0fa155 | A retry reruns from the first step its new record changes, and a device it drops leaves through the delete path. |

e3ed4492 changed 78 files, 1517 lines added and 2003 removed, a net of
486 fewer lines. Its non-test Go files added 503 lines and removed 607.
On the drill's `Reservation east-drill`, `status.session` took 2,668
bytes of 10,659.

Two fixes found on the way:

- 399d6b73 holds the status writes of a new operator copy until it has
  read its devices. Before, a restart wrote status from an empty view,
  and CI caught the double trigger it caused, a bug older than this
  plan.
- c0a84806 stops the pod of a deleted device with the others, and
  `PowerOff` reads a deleted device's output from the record. The
  drill had left `east-dustcap` running after a bulk delete.

## The drill

The third drill of plan 16 checked the freeze in parts C to F. A
moved `Focuser east` stayed connected on its server with `MovePending`
and one `Event`, then started on the other telescope at the next
session. A device created during a session stayed `Idle`, and a delete
removed its pod within 1 s. A bulk delete of a `Reservation`, a train,
and a `DustCap` released cleanly, and a retry with a fixed driver
reached `Ready` 33 s after the annotation. Two operator restarts under
an unsafe weather reading ran no trigger again.

## Open

- A reconnect to an INDI server drops and adds the state conditions
  again, so their triggers run again. This predates the plan.
- A guider pod reads `Failed` after a release, because the kubelet
  kills PHD2 and weston with `SIGKILL` after the pod's grace period of
  1 s. It does no harm.
