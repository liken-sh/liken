# 04, The INDI client in Go

Proposed on 2026-10-05. Built on 2026-10-05 as the package
`observatory-operator/indi`, and tested against transcripts recorded
from the 15 simulators of the `20261005-3` images. A temporary local
experiment also ran the client against a real `indiserver`. "What the
tests showed" gives the results.

## The problem

The operator, the reconciler, and `astrophotography-operator` all
speak INDI. No Go client is maintained:
[`goastro/indiclient`](https://github.com/goastro/indiclient) last
changed in March 2020, and its fork `jnmorley/indiclient` in October
2023. Both target `indiserver` 1.7.

## The requirement

A Go package speaks the INDI wire protocol directly, with no cgo, the
way `equipment-operator/cec/` speaks the kernel's CEC API. It follows
the repository's rule for events: it sends `getProperties`, takes the
`def*Vector` messages as the baseline, and applies each `set*Vector`
after that. When the connection fails, it connects again and takes the
baseline again.

It keeps each property's definition as the device sent it: the type,
the permission, the switch rule, the limits, and the format. It never
sends `enableBLOB` unless a caller asks for BLOBs, so a client that
reads frame paths receives no frame data.

The package is shared by two components, so where it lives is a
question for this plan. The rule against a shared junk directory
applies: it is an INDI client, and its name says so.

## The package

The package is `indi` in the module
`github.com/liken-sh/liken/observatory-operator`.
`astrophotography-operator` is the layer above, so it imports the
package from this module, and the import points down the stack. No
shared directory was needed.

- `Run` opens one connection for each call, and the caller decides when
  to open the next one. An operator can open it when the server's
  `Service` has a ready endpoint again, instead of on a timer.
- The store empties when a connection ends, and the next connection
  fills it from a new baseline. Subscribers receive `Disconnected`
  and then `Connected`.
- `Subscribe` returns a channel of events with a queue of no limit for
  each subscriber. The reader never waits for a subscriber, because a
  reader that waits stops reading the socket, and `indiserver` closes
  a client whose queue grows past its limit. `WaitFor` checks a
  condition after each change, with no timer.
- `SetNumbers`, `SetTexts`, and `SetSwitches` send every member of the
  property and follow the switch rule. `Settle` waits for the device's
  answer, and `ConnectDevice` and `DisconnectDevice` use it on
  `CONNECTION`.
- The store keeps the format and size of each BLOB, and no data. A
  caller that asked with `EnableBLOB` receives the data in the event of
  the update.

## How we test it

Go tests run against a real `indiserver` with the simulators from plan
02, in a container. They cover the baseline, updates, a server that
restarts, and a driver that restarts behind the server.

## What the tests showed

A recorder captured the bytes that `indiserver` sent with each of the
15 simulators behind it, in the topology of plan 03: the driver behind
`socat` in one container, and the server with a shim in another. Each
recording holds the reply to `getProperties`, to `CONNECTION.CONNECT`,
and to `CONNECTION.DISCONNECT`. The focuser also moved to 52000 and
then past its limit, and the dome turned to 30 degrees. The tests
replay the 48 files, 283 KB in all, from a real listener.

- **Every baseline and every connect applied.** For each simulator, the
  store held exactly the properties that the transcripts define and do
  not delete, counted with a regular expression and not with the
  client. Each device connected and disconnected through `CONNECTION`.
- **The client sent no `enableBLOB`** in any of the 15 connect and
  disconnect cycles.
- **The simulators send what a careful client must expect.** Eight of
  the 15 baselines define a property twice. The telescope updates
  `TELESCOPE_MOUNT_TYPE` before it defines it. The receiver sends a
  `delProperty` with an empty `name` on disconnect, which libindi's
  client reads as a property named "" and not as the whole device. The
  numbers hold sexagesimal formats such as `%010.6m`, and an update can
  change a member's `min`, `max`, and `step`, as the CCD simulator does
  for `CCD_FRAME`.
- **The answer to a change has two shapes.** The focuser answered a
  move with Ok and the new position in one update. The dome answered
  with five Busy updates and then Ok. Past its limit, the focuser
  answered Alert, and its reason came in a separate `message` after
  the update. The dome also sent its position, Ok at 0, after the
  move was sent and before the driver read it, so `Settle` takes an
  update as the answer only after a Busy, or when it carries the
  values sent.
- **A real server, with restarts.** In the local experiment, against
  `indiserver` and the focuser simulator in containers, the client took the baseline, connected the
  device, moved it to 52000, and disconnected it. A restart of the
  driver's container gave a `delProperty` for the whole device, and
  the driver came back disconnected. The restart, the new baseline,
  and a new connect of the device took 1.05 seconds. A restart of the
  server's container ended the connection, and the next `Run` took a
  new baseline. The restart, the new baseline, and a new connect took
  1.34 seconds. The driver's container must restart
  on its own, as a device pod's does: `socat` serves one connection
  and exits.

## Upstream issues

- The protocol version is still 1.7: `INDIV` in
  `libs/indicore/indiapi.h`. INDI 2.x added shared buffers and Unix
  sockets between the server and its drivers, which change no message
  that a client receives over TCP.
- [indi#1528](https://github.com/indilib/indi/issues/1528): the C++
  client kept the last BLOB in memory after a driver restarted. The Go
  client releases a BLOB once its caller has read it.

## References

- The INDI protocol: <https://docs.indilib.org/protocol/>
- The client side of `libindi`: `libs/indiclient/` in `indilib/indi`
- What `indiserver` sends each client, and how it reads `enableBLOB`:
  `indiserver/ClInfo.cpp` in `indilib/indi`
- [Root plan 74](../../../plans/74-astrophotography.md), "The INDI
  client in Go"
