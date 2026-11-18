# init reads state again on timers

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

The root `AGENTS.md` calls a timer that reads state again to find a
change a defect, unless a comment at the timer names the failure that
it covers. These loops in init read state on a timer, and no comment
at them names such a failure.

## The evidence

- `provingWatch` (`init/proving.go:246`) reads the release store on
  `machineState` every 5 seconds (`init/proving.go:258`) for the whole
  trial boot, up to `provingTimeout`, to see when machine-operator has
  promoted or withdrawn the trial.
- `reportWhenReady` and `reportPods` run `k3s kubectl get` through
  `pollAndReport` (`init/supervisor.go:478`) every 3 and 5 seconds for
  up to 5 minutes (`init/supervisor.go:428` and `443`), to print the
  node and pod tables on the console.
- `awaitStorageDevices` (`init/storage.go:387`) reads `/sys/block`
  every 500 milliseconds for up to 30 seconds. Its comment says it
  polls because the wait happens once for each boot and lasts seconds.

The store directory already has an inotify watch for the reboot and
restart intents (`machine.WatchDir`, which `init/reboot.go` uses), and
init reads uevents for the disk links and the hardware watch.

## What would confirm it

A read of each loop against the rule in the root `AGENTS.md`, section
"Keep state current with events". These are rule findings, so no
machine test is needed.

## What a fix has to decide

For each loop, whether it moves to an event source, or keeps its
timer with a comment that names the failure it covers. A watch of the
store directory fits `provingWatch`. A kubectl watch (`--watch`) fits
the console tables. The uevent reader fits the disk wait. The other
waits of a boot for a device, in `claim.go`, `reportdisks.go`,
`rootimage.go`, and `resolve.go`, have the same shape and need the
same decision.
