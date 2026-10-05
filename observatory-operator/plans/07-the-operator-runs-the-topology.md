# 07, The operator runs the topology

Proposed on 2026-10-05. Not built.

## The problem

Plan 03 runs the observatory from static manifests. A person who adds
a focuser has to write a pod, a `Service`, and a new shim argument for
the server, and has to restart the server to add it.

## The requirement

The operator turns the resources of plan 06 into the topology of plan
03: one pod and one `Service` for each device, with its DRA claim, and
the server pod with one shim for each device. The manifests of plan 03
become the operator's output, and the operator's tests compare against
them.

A device added after the server starts reaches the server without a
server restart. `indiserver` reads `start` and `stop` commands for
drivers from its `-f` fifo, so the operator can add and remove shims
on a running server. Each device is a symbolic link to the shim of
plan 03, so adding a device is a new link and a `start` command. How
the operator reaches the fifo in the server's pod is open.

The operator watches through client-go's reflector, as every operator
in the repository does. The `operators` skill holds the rules.

## How we test it

Applying the resources of plan 06 brings up the simulators, and the
plan 03 measurements give the same results. Adding a device and
deleting one leave the other devices connected.

## Upstream issues

- [indi#2365](https://github.com/indilib/indi/pull/2365) and
  [indi#2340](https://github.com/indilib/indi/issues/2340): ZWO cameras
  reset their USB connection after frames, and the driver's hot-plug
  handling destroyed the device during the reset. A device pod has to
  survive the device leaving and returning on the bus.

## References

- The fifo: `indiserver/Fifo.cpp` in `indilib/indi`, and the `-f`
  option in `indiserver`'s usage text
- The `operators` skill under `.agents/skills`
