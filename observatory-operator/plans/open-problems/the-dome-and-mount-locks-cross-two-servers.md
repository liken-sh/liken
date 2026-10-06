# The dome and mount locks cross two servers

Found on 2026-10-05, while plan 08 was built. Moved out of plan 08 when
it closed on 2026-10-06.

## The problem

An `Observatory` can set two lock policies between its dome and its
mounts:

- `domeLocksMount` sets the mount's `DOME_POLICY` to `DOME_LOCKS`: the
  mount does not unpark while the dome is parked, and it parks when the
  dome parks.
- `mountLocksDome` sets the dome's `MOUNT_POLICY` to `MOUNT_LOCKS`: the
  dome does not park while a mount is unparked.

INDI enforces both in the drivers, through a snoop: the mount's driver
reads the dome's park state, and the dome's driver reads the mount's. A
driver snoops only devices on its own server. The dome runs on the
observatory's server, and each mount runs on its telescope's server, so
no snoop connects them. The operator does not write either policy, and
`Configure` notes each one it skips. The shutter policies need no
snoop, and the operator writes them.

The cost is a collision: with the locks off, a dome can park onto a
telescope that points through its slit. The simulators have no
hardware to damage, so this matters first with a real dome.

## Options

- **The operator enforces the locks.** It already holds an INDI client
  on every server. It reads the dome's and the mounts' park states,
  refuses or reverses a move that breaks a policy, and reports it. The
  enforcement stops while the operator is down, which the drivers'
  own snoop does not.
- **The dome runs on each telescope's server.** One dome with one
  mount fits on one server, and the snoop works as INDI intends. A
  dome with two telescopes needs a dome driver on two servers, and one
  serial line accepts one driver.
- **A relay forwards the snooped properties between servers.** INDI
  chains servers with `indiserver`'s remote driver syntax,
  `device@host:port`, which makes a device on one server appear on
  another. Whether a snoop works through a chained device was not
  checked.

Return to this before the first real dome runs under the operator.
