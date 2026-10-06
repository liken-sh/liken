# Adding a device restarts the server

Found on 2026-10-05, while plan 07 was built.

## The problem

The arguments of a telescope's INDI server link every device of the
telescope. When a person adds a device to the telescope's inventory
while a reservation is `Ready`, the digest of the server pod's spec
changes, and the operator replaces the server's pod. Each device on
that server restarts with it and comes back disconnected, and the
operator connects it and writes its settings again. In plan 03, the
restart took 3 seconds. On the test cluster, after the server's pod
was deleted, the mount was connected again 7.2 seconds later. An
exposure or a guide loop that runs during the restart is lost.

## What could fix it

`indiserver` reads `start` and `stop` commands for drivers from its
`-f` fifo, so the operator could add a shim to a running server. Each
device is a symbolic link to the shim, so adding a device is a new
link and a `start` command. The server's container has no shell, and
how the operator reaches the fifo in the server's pod is open.

## References

- The fifo: `indiserver/Fifo.cpp` in `indilib/indi`, and the `-f`
  option in `indiserver`'s usage text
- [Plan 07](../completed/07-the-operator-runs-the-topology.md), "Not
  built yet"
