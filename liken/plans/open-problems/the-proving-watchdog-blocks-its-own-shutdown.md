# The proving watchdog blocks its own shutdown

Open bug. When a proving boot never settles, the proving watch reboots
the machine onto the proven slot, and every such reboot waits the full
ten-second shutdown timeout for the watch to stop itself. During that
wait, init starts k3s again. A unit test reproduced the wait on
2026-11-19. No test covers the restart of k3s, because
`rebootMachine` cannot run in a test.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

`provingWatch` (`init/proving.go`) calls `rebootMachine` from inside a
machine-plane component (`init/proving.go:255`). `rebootMachine` shuts
the plane down (`init/reboot.go:150`) and waits on the plane's wait group, which counts the
goroutine that called it. Every watchdog reboot waits the full
ten-second shutdown timeout and then prints that the proving watch did
not stop. `provingWatch` has a test of its promotion path
(`init/provingwatch_test.go`) and none of its watchdog reboot.

This path also skips `stopK3s`, so `superviseK3s` starts k3s again
during the ten-second shutdown, and the new k3s keeps `clusterState`
open.

A test started a component on a new machine plane that calls
`shutdown` with a ten-second timeout from inside itself, as the
watchdog's reboot does, in a `synctest` bubble. It asserted that the
shutdown returns without a wait, and it failed with:

```
the shutdown waited 10s for the component that called it, want no wait
```

The test is not in the tree, because a failing test cannot land on
`main`.

## The fix

This needs the maintainer's decision before a fix is built.

**How the watchdog asks for its reboot.** It can send the reboot
through `rebootRequests`, as every other reboot does, so k3s is
stopped first. That stops the `wedge-k3s` fault drill and goes
through the `stopK3s` wait that has no bound, described below. Or it
can call a reboot path that runs outside the machine plane, so the
shutdown does not wait for the watch, and that path also stops k3s.

The watch should signal the reboot and return, so the plane can stop
it like any other component. The watchdog reboot gets a test.

A watch that sends the reboot through `rebootRequests` would also stop
the `wedge-k3s` fault drill, whose main goroutine reads no requests.
And it would pass through `stopK3s`, whose wait after SIGKILL has no
bound, on the boot that a stuck k3s most likely caused. The bound of
the open problem
[two waits have no bound after SIGKILL](two-waits-have-no-bound-after-sigkill.md)
comes first.
