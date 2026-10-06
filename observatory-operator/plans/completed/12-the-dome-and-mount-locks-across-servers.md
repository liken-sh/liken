# 12, The dome and mount locks across servers

Proposed and built on 2026-10-06, and tested against the fake API
server and the fake INDI servers. A local experiment with the real
`indiserver` and the real simulators of the pinned `indi-simulators`
image chose the design. The drill ran on the two-node test cluster on
2026-10-06. On `2026.10.04-004-dev-077-4a3db556`, a mount unparked
under a parked dome, because the next client to connect turned
`DOME_POLICY` off. Commit 35c9c1d9 saves the driver's configuration
after the operator writes a lock policy, and on
`2026.10.04-004-dev-078-35c9c1d9` every step of the drill held. "What
the test cluster measured" gives the results. This plan closes the
open problem "The dome and mount locks cross two servers", which plan
08 recorded.

## The problem

An `Observatory` can set two lock policies between its domes and its
mounts. `domeLocksMount` sets each mount's `DOME_POLICY` to
`DOME_LOCKS`, and `mountLocksDome` sets each dome's `MOUNT_POLICY` to
`MOUNT_LOCKS`. INDI enforces both in the drivers, through a snoop: the
mount's driver reads the dome's park state, and the dome's driver reads
the mount's. A driver snoops through its own server. The dome runs on
the observatory's server and each mount on its telescope's server, so
no snoop connected them. The operator wrote neither policy, and
`Configure` noted each one it skipped. With the locks off, a dome can
park onto a telescope that points through its slit.

## The requirement

- Both policies hold for every telescope of the observatory, while the
  dome runs on the observatory's server and each mount on its
  telescope's server.
- They hold with more than one telescope. The dome must not park while
  any mount is unparked.
- Where INDI can enforce a policy in its drivers, it does. A driver
  refuses a move before the move starts, and keeps refusing it while
  the operator is down.
- The operator posts an Event when a lock refuses a move, and the
  status says plainly what holds while the operator is down.

## What INDI does

The source read is indilib/indi at commit `1dd9b34` on `master`, the
last commit before the PPA build `2.2.5+202610040455` that
`indi/package.toml` pins.

- **The mount** (`libs/indibase/inditelescope.cpp`). It snoops
  `DOME_PARK` and `DOME_SHUTTER` of the device that its
  `ACTIVE_DEVICES.ACTIVE_DOME` names, `Dome Simulator` by default. It
  reads a snooped `DOME_PARK` only while it is connected, and only when
  the report's state is `Ok`: `PARK` On locks it, and `UNPARK` On
  unlocks it. Under `DOME_LOCKS`, an unpark while it is locked answers
  `TELESCOPE_PARK` with `Alert` and `PARK` On, and the message "Cannot
  unpark mount when dome is locking". The code that parked the mount
  when the dome parks is compiled out with `#if 0`, and its comment
  says that the Watchdog driver does that. So the old CRD description,
  "parks when the dome parks", was wrong.
- **The dome** (`libs/indibase/indidome.cpp`). It snoops
  `TELESCOPE_PARK`, the coordinates, and the pier side of the device
  that its `ACTIVE_DEVICES.ACTIVE_TELESCOPE` names, `Telescope
  Simulator` by default. It reads a snooped `TELESCOPE_PARK` when the
  report's state is `Ok`, connected or not. Under `MOUNT_LOCKS`, a park
  while the mount is unparked answers `DOME_PARK` with `Alert` and
  `UNPARK` On. It snoops one mount.
- **The server** (`indiserver/`). `IDSnoopDevice` sends a
  `getProperties` with a device and a name. The server records it, and
  hands the driver every later message of that device and property
  (`DvrInfo::q2SDrivers`). A remote driver, `device@host:port`, connects
  to the other server as a client, asks for that device, and forwards
  each generic `getProperties` that the server sends it
  (`DvrInfo::q2RDrivers`). A `set*Vector` that a client sends goes to
  each driver that snoops it, and to no other client
  (`ClInfo::onMessage`); the comment there names a server chained
  upstream as the sender. `RemoteDvrInfo::start` calls `Bye()`, which
  exits the server, when its connection is refused, and a remote
  driver whose connection ends starts again.

## The experiment

Three `indiserver` containers ran from
`ghcr.io/liken-sh/indi-simulators:20261005-1` on one Docker network on
the laptop: `obs` with `indi_simulator_dome`, `t1` with
`indi_simulator_telescope`, and `t2` with `indi_simulator_telescope`
named `Telescope Simulator 2` through the fifo's `-n`. The fifo started
each remote driver after every server listened. The clients were
`indi_getprop`, `indi_setprop`, and `indi_eval` from INDI 2.2.4 on the
laptop, and a small Python client that sends raw elements. The
containers, the network, and the scripts were removed afterwards.

| Setup | Result |
|---|---|
| The dome chained into `t1` and `t2` | Each server listed the dome once. Both mounts, under `DOME_LOCKS`, answered an unpark with `Alert` and `PARK` On while the dome was parked, and unparked after the dome unparked. Neither mount parked when the dome parked. |
| The mounts chained into `obs` as well | One `indi_getprop` on `t1` started a loop of `getProperties`. The counts in the three logs rose from 21, 9, and 8 to 765, 521, and 654 in 3 seconds, each server used 100% of a CPU, and the client received the mount's `DRIVER_INFO` 26 times. The loop went on after the client left, and after the chain to `t2` was stopped, between `obs` and `t1` alone. Ten seconds after the chain from `t1` was stopped as well, `obs` and `t1` still used 100% of a CPU, and 268 and 336 MiB of memory. |
| `t1`'s mount chained into `obs`, one way | The dome, under `MOUNT_LOCKS`, answered a park with `Alert` while the mount was unparked, and parked after the mount parked. The servers used 0.05% and 0.10% of a CPU. |
| `t1` stopped while `obs` chained its mount | `obs` logged `connect(t1,7624): Connection refused` and `good bye`, and exited with status 1. |
| A client on `t1` relays `DOME_PARK` of `Dome Simulator`, state `Ok`, with no chain | The server logged `queuing snooped <setSwitchVector device='Dome Simulator' name='DOME_PARK'>`. With `PARK` On, the mount answered an unpark with `Alert`; with `UNPARK` On, it unparked. |
| A client on `obs` sets the dome's `ACTIVE_TELESCOPE` to `All mounts` and relays its `TELESCOPE_PARK` | With `UNPARK` On, the dome answered a park with `Alert`; with `PARK` On, it parked. |

Two smaller findings. A client's `newSwitchVector` to the dome on `obs`
reached each mount through the chain, and each mount's driver logged
"Property DOME_PARK is not defined in Dome Simulator". Each generic
`getProperties` of a client on `t1` or `t2` reached `obs`, and every
driver on `obs` sent its definitions again.

## The design

The operator relays each park state that a lock needs between the
servers, as a client `set*Vector` (`indi.Client.Relay`). The drivers
enforce the locks.

### Why not a chain

- A chain in both directions loops without end. `q2RDrivers` sends a
  generic `getProperties` on to a remote driver, so each server sends
  it back to the other, with one telescope or two.
- A chain in one direction carries one lock only.
- A server whose chained server stops exits, and a server that starts
  before its chained server listens exits too. A restart of the
  observatory's server would restart every telescope's server, and lose
  each exposure in progress.
- The dome snoops one mount, so no chain covers several telescopes. Two
  telescopes with the same model of mount also give two devices of one
  name on the observatory's server.

### Why not the operator alone

An operator that reads the park states and reverses a move acts after
the move starts. A relayed state makes the driver refuse the move
before it starts, with no round trip through the operator.

### The relay

`locks.go` plans each observatory's relay from the tree and the INDI
clients, and the status writer reads the same plan for the condition.

- **To each mount**, for `domeLocksMount`: one `DOME_PARK` report under
  the name in the mount's `ACTIVE_DOME`. It is parked while any dome of
  the observatory is parked, moving, or silent, and unparked otherwise.
- **To each dome**, for `mountLocksDome`: one `TELESCOPE_PARK` report
  under the name in the dome's `ACTIVE_TELESCOPE`. It is unparked while
  the mount of any held telescope is unparked, moving, or silent, and
  parked otherwise. A mount whose reservation finished `Secure` counts
  as parked when it is silent, because `Secure` parked it before
  `Disconnect` and `StopDevices` removed its driver. A telescope that no
  reservation holds has no server, and its mount does not count.
- The operator writes no `ACTIVE_DEVICES`. It relays under the names
  that the drivers snoop, so a holder such as KStars that writes
  `ACTIVE_DEVICES` does not break the relay. It skips a name that a
  device on the target server has, because that driver snoops the real
  device.
- `Configure` writes `DOME_POLICY` to each mount, and `StartSite`
  writes `MOUNT_POLICY` to each dome, with the shutter policies. The
  runner's reapply writes them again to a driver that comes back.
- A relay goes out again when its value changes, when its target's
  connection changes, or when the target's server reports a
  structural change: a new connection, or a property defined or
  deleted. A restarted driver defines its properties again, so it
  receives the report again.
- `keepLocks` relays after each change that rings `changed`.
  `Configure` relays before `Prepare` unparks the mount, and `StopSite`
  relays before it parks the dome. The report and the move travel on
  one connection, and `indiserver` hands both to the driver in that
  order.

### The Events and the condition

When a mount answers `TELESCOPE_PARK` with `Alert` while the relayed
domes are parked, the operator posts the Warning `MountUnparkRefused` on
the `Mount`. When a dome answers `DOME_PARK` with `Alert` while the
relayed mounts are unparked, it posts `DomeParkRefused` on the `Dome`.
A transition into `Alert` posts one, and the first reading after an
operator start posts none.

The `Observatory` reports `LocksRelayed` while a lock policy is set:
`True` with the reason `Relayed` and what it relays, `False` with the
reason `Waiting` and what it waits for, or `False` with the reason
`Idle` while no dome or mount runs. The `True` message ends with "While
observatory-operator is down, each driver keeps the last state that the
operator relayed".

### Limits

- While the operator is down, a lock holds the state that the operator
  relayed last. A park or an unpark in that time is not relayed.
- A new `indi_simulator_telescope` reports `UNPARK` On. So the lock
  cannot keep a simulator mount parked under a parked dome: the mount
  never asks to unpark. A mount that restores its park state starts
  parked, and the lock refuses its unpark. A test that reserved a
  telescope under a parked dome found this.
- The mount does not park when the dome parks. INDI leaves that to its
  watchdog driver, and the operator does not add it.
- No step unparks the dome. With `domeLocksMount` and a real dome left
  parked by `StopSite`, `Prepare` fails at the mount's unpark until a
  person or a program unparks the dome.

## What was built

- `indi/relay.go`: `Client.Relay` writes a property as a `set*Vector`
  with its state.
- `locks.go`: the plan, the relay, the Events, and `keepLocks`.
  `indiconn.go` numbers each server's structural changes.
- `configure.go`: `DOME_POLICY` in `Configure`, `MOUNT_POLICY` in
  `domePolicies`, and the relay at the end of `Configure`.
  `deactivation.go`: the relay before `StopSite` parks the dome.
- `statustree.go` and `observatory/site.go`: the `LocksRelayed`
  condition.
- The CRD's descriptions of `policies`, `domeLocksMount`,
  `mountLocksDome`, and the conditions; the README section "The dome
  and mount locks"; and `examples/simulators.yaml`, which now sets both
  lock policies.
- `fakelocks_test.go`: the fake mount and dome enforce the locks as
  INDI's base classes do, and the fake server hands a relayed report to
  the driver that snoops it.

## What the tests showed

Every test runs in a `synctest` bubble against the fake API server and
the fake INDI servers, with both telescopes of the example where the
case needs two.

- `TestTheOperatorWritesBothLockPolicies`: `MOUNT_POLICY` and both
  `DOME_POLICY` writes, and the first relays to each server.
- `TestTheDomeDoesNotParkWhileAnyMountIsUnparked`: with `east` parked
  and `west` unparked, the dome answers a park with `Alert`, and one
  `DomeParkRefused` names `Mount west`. With both parked, the dome
  parks.
- `TestAMountDoesNotUnparkWhileTheDomeIsParked`: the mount answers an
  unpark with `Alert`, and one `MountUnparkRefused` names `Dome lab`.
  After the dome unparks, the mount unparks.
- `TestAMountThatStopsReportingKeepsTheDomeUnparked`: `west`'s mount
  pod is deleted and held `Pending`; the relay turns to unparked, the
  dome refuses to park, and the condition waits for `Mount west`.
- `TestARestartedMountIsRelayedTheDomeAgain`: a new mount driver that
  connects after the first relay still receives the parked dome, and
  refuses to unpark.
- `TestTheObservatoryReportsTheRelay`: `Idle`, then `Relayed` with its
  message, and no condition once the policies are removed.
- `TestTheLastReleaseParksTheDome`: releasing both reservations parks
  the dome with no refusal.
- `indi/relay_test.go`: the element, its state, and its members, and
  the refusals.

With the relay's send replaced by a no-op, four of the operator's
tests failed. With the server's number and the target's connection
left out of the relay's key, the restart test failed. With the relay at
the start of `StopSite` removed, the release test still passed: in the
fake, `keepLocks` relays first, so the test does not hold that order.
`make test` measured 95.7% total coverage, over the floor of 95%.

## What the test cluster measured

The inventory was `examples/simulators.yaml`, with `closeShutterOnPark`,
`domeLocksMount`, and `mountLocksDome` set on `Observatory` `lab`, and
reservations of `east` and `west` created by hand. The drill acted as
the holder through `indi_setprop` and `indi_getprop` on port-forwards
to the three servers, and a small client timestamped each
`TELESCOPE_PARK`, `DOME_PARK`, `DOME_POLICY`, and `MOUNT_POLICY`.

### The lock that a client turned off

On `2026.10.04-004-dev-077-4a3db556`, both mounts reported
`DOME_IGNORED` On a few minutes after `Configure`. A new client's
`getProperties` drew two definitions of `DOME_POLICY` from the west
mount 6 ms apart: `DOME_LOCKS` On, then `DOME_IGNORED` On. With both
mounts and the dome parked, `east`'s mount unparked with `Ok`. The
dome's lock held: it refused to park with `Alert` while a mount was
unparked, and `DomeParkRefused` posted 97 ms after the request.

`Telescope::ISGetProperties` in `libs/indibase/inditelescope.cpp`
reads `DOME_POLICY` from the driver's configuration file before it
defines the property. The operator's first write of the mount's
location or `ACTIVE_DEVICES` made the driver save its whole
configuration, because `saveConfig` of one property writes every
property when no file exists, and `DOME_POLICY` was `DOME_IGNORED`
then. The operator's later write of `DOME_LOCKS` changed only the
driver's memory. So each new client, such as PHD2 after `StartGuider`
or a person's KStars, turned the lock off. The dome reads
`MOUNT_POLICY` from its file only at start, so its lock held. The fake
mount did not model the file, and the tests passed.

Commit 35c9c1d9 makes the operator send `CONFIG_PROCESS` `CONFIG_SAVE`
after it changes a lock policy, and adds `indi.Client.Answered`,
because `CONFIG_PROCESS` turns `CONFIG_SAVE` Off again when the save
ends. The fake now keeps a configuration file, and
`TestAClientThatConnectsLaterLeavesTheMountLocked` failed before the
change. `make test` measured 95.6% total coverage.

### The drill on the fixed build

On `2026.10.04-004-dev-078-35c9c1d9`, both reservations were `Ready`
41 s after they were created. Each new client after `Configure`,
PHD2 among them, read `DOME_LOCKS` On from both mounts.

| Step | Result |
|---|---|
| `kubectl get observatory lab` | `LocksRelayed` `True`, `Relayed`, naming both mounts and the dome |
| Park `east`, then park the dome | `DOME_PARK` `Alert` with `UNPARK` On. `DomeParkRefused` on `Dome` `lab` named `Mount west`, 78 ms after the request |
| Park `west`, then park the dome | Parked in 17 s, with the shutter closed |
| Unpark `east` | `TELESCOPE_PARK` `Alert` with `PARK` On. `MountUnparkRefused` on `Mount` `east`, 145 ms after the request |
| Scale the operator to 0, unpark the dome, unpark `east` | The mount refused with `Alert`, and no `Event` posted |
| Scale the operator to 1, unpark `east` | The operator relayed the unparked dome, and `LocksRelayed` named it. The unpark succeeded |
| Park `east` and the dome, delete `east-mount` | The new driver connected 4 s later. The operator wrote `DOME_LOCKS` 70 ms after the driver defined it, and relayed the parked dome 5 ms after that |
| Park the new mount, then unpark it | `Alert` with `PARK` On, and `MountUnparkRefused` |

The condition's message changed with each park, and `Waiting for
Mount east to report TELESCOPE_PARK` appeared for 1 s while
`east-mount` restarted.

### Release with the locks on

| Start | Result |
|---|---|
| Dome parked, `east`'s mount unparked by the defect, on dev-077 | Both `Released` in 41 s. `Secure` parked `east`'s mount, and `StopSite` found the dome parked. No Warning |
| Dome and both mounts parked, on dev-078 | Both `Released` in 39 s, and no Warning |
| Dome and both mounts unparked, on dev-078 | `Secure` parked both mounts, `StopSite` parked the dome in 18 s, and the last reservation was `Released` 73 s after the delete. No Warning |

The limit that no step unparks the dome did not show: each new
`lab-dome` pod starts its simulator unparked, so the next activation
found the dome unparked.

### What the drill noted

- `DomeParkRefused` with two mounts reads "Mount east, Mount west is
  unparked or moving".
- A refusal posts an `Event` only on a transition into `Alert`. A
  second refused park while `DOME_PARK` was still `Alert` posted none.
- The drill stopped the operator with `kubectl scale` to 0 and
  started it again with a scale to 1, because a deleted pod comes back
  within seconds.

## The drill

On the test cluster, with the example applied and both telescopes
reserved:

1. `kubectl get observatory lab -o yaml`: `LocksRelayed` is `True`.
2. In KStars on `east-telescope`, park the mount. On `lab-observatory`,
   park the dome: it refuses, and `kubectl describe dome lab` shows
   `DomeParkRefused` naming `Mount west`.
3. Park `west`'s mount, and park the dome: it parks.
4. Unpark `east`'s mount: it refuses, and `kubectl describe mount
   east` shows `MountUnparkRefused`.
5. Delete the operator's pod, unpark the dome in KStars, and unpark a
   mount: the mount still refuses, because it keeps the last relay.
   When the operator is back, it relays the unparked dome, and a
   second unpark succeeds.
6. Delete `east-mount`, and check that the new driver refuses an
   unpark while the dome is parked.

## References

- `libs/indibase/inditelescope.cpp`, `libs/indibase/indidome.cpp`,
  `indiserver/DvrInfo.cpp`, `indiserver/ClInfo.cpp`, and
  `indiserver/RemoteDvrInfo.cpp` in indilib/indi at `1dd9b34`.
- [Plan 08](08-the-reconciler.md), which recorded the open problem.
