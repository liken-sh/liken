---
aliases: [/docs/reference/dome-and-mount-locks/]
title: The dome and mount locks
weight: 50
---

While an `Observatory` has a `Dome`, the domes and the mounts lock
each other's park, and no field turns the locks off. INDI's drivers
enforce both locks, and the operator writes each policy and relays
what each driver needs to read:

- Each mount's `DOME_POLICY` is `DOME_LOCKS`. The mount refuses to
  unpark while a dome is parked or moving. The mount does not park
  when the dome parks: INDI leaves that to its watchdog driver.
- Each dome's `MOUNT_POLICY` is `MOUNT_LOCKS`. The dome refuses to
  park while the mount of any reserved telescope in the observatory is
  unparked or moving.
- Each dome's shutter follows its park state:
  `DOME_SHUTTER_PARK_POLICY` has `SHUTTER_CLOSE_ON_PARK` and
  `SHUTTER_OPEN_ON_UNPARK` both On. The dome enforces this alone, also
  while the operator is down. The driver acts on the policy only when
  its park state changes, so a dome's `state` action also sets
  `DOME_SHUTTER`: after `state: Unparked` the shutter is open, and
  after `state: Parked` it is closed. The simulator starts unparked
  with its shutter closed, and its activation opens the shutter.

In an observatory with no dome, each mount's `DOME_POLICY` is
`DOME_IGNORED`. INDI's mount starts locked and unlocks only when a
dome reports that it is unparked, so with no dome to report, a mount
under `DOME_LOCKS` never unparks. The observatory then has no
`LocksRelayed` condition. The policy follows the domes at once: when
an observatory's last `Dome` is deleted, the operator writes
`DOME_IGNORED` to each running mount of the observatory, and when a
`Dome` is declared, `DOME_LOCKS`. It saves each driver's configuration
after the write. Without that, a mount would stay locked to the last
park state that the operator relayed from the deleted dome.

A driver reads another device's park state through its own INDI
server, but the dome runs on the observatory's server and each mount
on its telescope's. So the operator relays the park states between the
servers. Each mount receives the domes' state under the name in its
`ACTIVE_DEVICES.ACTIVE_DOME`. The dome receives one state for every
mount under the name in its `ACTIVE_DEVICES.ACTIVE_TELESCOPE`:
unparked while any mount is unparked, moving, or silent. A mount that
does not report its park state counts as unparked until the
`Deactivation` step of its reservation has ended. A dome that does not
report counts as parked.

The `Observatory`'s `LocksRelayed` condition reports the relay. It is
`True` while the operator relays each state, and its message names
what it relays. It is `False` with the reason `Waiting` while a device
does not report what the relay needs, and with the reason `Idle` while
no dome or mount runs. While the operator is down, each driver keeps
the last state that the operator relayed, and a later park or unpark is
not relayed. A driver that restarts forgets that state, and the
operator relays it again when the driver connects.

When a driver refuses a move under a lock, it answers with `Alert`,
and the operator posts a `Warning` on the device: `MountUnparkRefused`
on the `Mount`, or `DomeParkRefused` on the `Dome`. A procedure's
action that asked for the move fails, and its summary gives the same
explanation as the Warning, such as
`state: Parked: Dome lab refused to park, because Mount east is unparked or moving`.
In a trigger's run, the summary of a refused dome park adds that
`after: [{kind: Mount}]` orders the dome's park after the mounts'
parks, because the tree gives a trigger no order.
