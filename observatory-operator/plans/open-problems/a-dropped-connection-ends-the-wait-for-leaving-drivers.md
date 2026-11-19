# A dropped connection ends the wait for leaving drivers

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

When devices leave a running INDI server, `setDrivers` in
`serverdrivers.go` patches the server pod's driver annotation and
then waits, through the function that `stopping` returns, until the
server reports each leaving driver stopped. The caller deletes the
device pods only after this wait. The comment on `stopping` gives the
reason: a driver whose device pod is deleted first reads EOF on its
connection, and indiserver starts it again.

The wait returns at once on an `indi.Disconnected` event. The comment
there says that no `delProperty` can arrive on a closed connection,
and that a restarted server starts only the drivers in the
annotation. That reason covers a server that restarted. It does not
cover a client connection that closed while the server kept running,
for example when indiserver closes a slow client (`indi/events.go`)
or the network drops the connection. In that case the drivers can
still run, the caller deletes their device pods, and indiserver can
start the drivers again before the shim reads the new annotation.

## Evidence

- `serverdrivers.go`, `stopping`: `case e.Kind == indi.Disconnected:
  return nil`, with no check that the server pod restarted.
- `serverdrivers.go`, the comment on `stopping`: the order of the
  wait and the pod delete exists so that indiserver does not start a
  leaving driver again.

## The reproduction

A test with the fake INDI server and `fakeshim_test.go` makes the
shim stop a leaving driver after 10 seconds, deletes the `Focuser`
east of a Ready reservation, and after 2 seconds closes the
operator's client connections to the server `east-telescope` with
`indiWorld.cut`, while the server keeps running. It wants no driver
started again. It failed with:

```
restarted drivers = ["east-telescope east-focuser"] after the connection dropped, want none, because the driver stopped before its pod was deleted
```

The same test without the cut passes. The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When the operator's connection to a running INDI server drops while it
waits for a leaving driver to stop, it deletes the device's pod at
once, and indiserver starts the driver again.

- How does the wait tell a dropped connection from a server that
  restarted: by the server pod's UID, or by the driver list that the
  next connection shows?
- After a reconnect, does the wait go on until `driverStopLimit`, or
  read the devices that the new connection defines?
