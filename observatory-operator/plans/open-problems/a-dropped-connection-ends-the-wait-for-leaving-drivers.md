# A dropped connection ends the wait for leaving drivers

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

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

## What would confirm it

A test with the fake INDI server and `fakeshim_test.go`. Start a
reservation, move one device to another server, and close the
operator's client connection with `indiWorld.cut` after the
annotation patch and before the leaving driver exits. Check whether
the device pod is deleted while its driver still runs, and whether
the server starts that driver again.

## What a fix has to decide

- How the wait tells a closed client connection from a server that
  restarted, for example by the server pod's UID or by the
  annotation that the next connection's driver list shows.
- Whether the wait subscribes again after a reconnect and keeps
  waiting until `driverStopLimit`, or reads the devices that the new
  connection defines.
