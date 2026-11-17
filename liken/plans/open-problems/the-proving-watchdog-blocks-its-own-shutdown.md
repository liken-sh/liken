# The proving watchdog blocks its own shutdown

Open bug. When a proving boot never settles, the proving watch reboots
the machine onto the proven slot, and every such reboot waits the full
ten-second shutdown timeout for the watch to stop itself. During that
wait, init starts k3s again.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

`provingWatch` (`init/proving.go`) calls `rebootMachine` from inside a
machine-plane component (`init/proving.go:292`). `rebootMachine` shuts
the plane down and waits on the plane's wait group, which counts the
goroutine that called it. Every watchdog reboot waits the full
ten-second shutdown timeout and then prints that the proving watch did
not stop. `provingWatch` has a test of its promotion path
(`init/provingwatch_test.go`) and none of its watchdog reboot.

This path also skips `stopK3s`, so `superviseK3s` starts k3s again
during the ten-second shutdown, and the new k3s keeps `clusterState`
open.

## The fix

The watch should signal the reboot and return, so the plane can stop
it like any other component. The watchdog reboot gets a test.

A watch that sends the reboot through `rebootRequests` would also stop
the `wedge-k3s` fault drill, whose main goroutine reads no requests.
And it would pass through `stopK3s`, whose wait after SIGKILL has no
bound, on the boot that a stuck k3s most likely caused. The bound of
the open problem
[two waits have no bound after SIGKILL](two-waits-have-no-bound-after-sigkill.md)
comes first.
