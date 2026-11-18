---
aliases: [/docs/reference/dome-and-mount-locks/]
title: The dome and mount locks
weight: 50
---

# The dome and mount locks

The dome and the mounts of an observatory lock each other out of
moves that would damage the equipment. While an `Observatory` has a
`Dome`, a mount refuses to unpark while the dome is parked or moving, so the mount cannot swing into a closed roof. A dome
refuses to park while the mount of a reserved telescope in the
observatory is unparked or moving, so the dome cannot close on a moving
telescope. No field turns these locks off. INDI's drivers enforce both
locks, and the operator sets the lock policy in each driver and passes
each driver the park states it needs to read:

- Each mount's `DOME_POLICY` is `DOME_LOCKS`. The mount refuses to
  unpark while a dome is parked or moving. The mount does not park
  when the dome parks. In INDI, that is the job of the watchdog
  driver.
- Each dome's `MOUNT_POLICY` is `MOUNT_LOCKS`. The dome refuses to
  park while the mount of any reserved telescope in the observatory is
  unparked or moving.
- Each dome's shutter follows its park state:
  `DOME_SHUTTER_PARK_POLICY` has `SHUTTER_CLOSE_ON_PARK` and
  `SHUTTER_OPEN_ON_UNPARK` both On. The driver applies this policy
  itself, also while the operator is down, but only when its park state
  changes. So a dome's `state` action also sets `DOME_SHUTTER`: after
  `state: Unparked` the shutter is open, and after `state: Parked` it
  is closed. The simulator starts unparked with its shutter closed,
  and the `state: Unparked` activation of `examples/simulators.yaml`
  opens the shutter.

In an observatory with no dome, each mount's `DOME_POLICY` is
`DOME_IGNORED`. INDI's mount starts locked and unlocks only when a
dome reports that it is unparked, so a mount under `DOME_LOCKS` with no
dome to report never unparks. The observatory then has no
`LocksRelayed` condition.

When an observatory's last `Dome` is deleted, the operator writes
`DOME_IGNORED` to each running mount of the observatory immediately,
because a mount under `DOME_LOCKS` would otherwise stay locked to the
parked state that the deleted dome last reported. A `Dome` declared
during a session takes effect in the next session, when the
`Configure` step writes `DOME_LOCKS`. After each policy write
the operator saves the driver's configuration. Without the save, the
driver loads its older saved policy again when the next client, such as
PHD2 or KStars, connects to it.

A driver reads another device's park state from its own INDI server,
but the dome runs on the observatory's server and each mount runs on
its telescope's server. So the operator relays the park states between
the servers:

- Each mount receives the state of the domes, under the device name in
  its `ACTIVE_DEVICES.ACTIVE_DOME`.
- The dome receives one combined state for all the mounts, under the
  device name in its `ACTIVE_DEVICES.ACTIVE_TELESCOPE`. The state is
  unparked while any mount is unparked, moving, or not reporting.

A mount that does not report its park state counts as unparked until
the `Deactivation` step of its reservation has ended. A dome that does
not report counts as parked. A person can delete a `Mount` or a `Dome`
during a session, alone or together with its reservation, or edit it
so that it belongs to another INDI server. Its state still counts on
the first server while its driver's pod runs there, because the device can move until its
`deactivation` parks it.

The `Observatory`'s `LocksRelayed` condition reports the relay. It is
`True` with the reason `Relayed` while the operator relays each state,
and its message names what it relays. It is `False` with the reason
`Waiting` while a device does not report what the relay needs, and
with the reason `Idle` while no dome or mount runs. While the operator is down, each driver keeps
the last state that the operator relayed, and a later park or unpark is
not relayed. A driver that restarts loses that state, and the
operator relays it again when the driver connects.

When a driver refuses a move under a lock, it sets the property to
`Alert`, and the operator posts a `Warning` `Event` on the device:
`MountUnparkRefused` on the `Mount`, or `DomeParkRefused` on the
`Dome`. The action of the procedure that requested the move fails, and
its summary gives the same explanation as the `Event`, such as
`state: Parked: Dome lab refused to park, because Mount east is unparked or moving`.
When a trigger's dome park is refused, the summary adds that you can
set `after: [{kind: Mount}]` on the dome's park so that it runs after
the mounts park. A trigger needs this because the operator starts the
runs of a trigger on every resource at the same time. Deactivation
parks the telescope's devices, such as the mounts, before it parks the
observatory's devices, such as the dome.
