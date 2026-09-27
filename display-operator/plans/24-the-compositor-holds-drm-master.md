# The compositor holds DRM master

Plan 24. Proposed 2026-09-27.

The compositor must hold DRM master on its card, or it cannot show a
frame. The operator container opens the same card node to read the
connectors, and that open can take master from the compositor. This
plan makes the operator open the card only while a compositor holds
it, makes each open of the operator give master back at once, and
restarts a compositor that runs without master. It answers the open
problem [The compositor can start without DRM
master](open-problems/the-compositor-can-start-without-drm-master.md).

## The problem

The kernel makes a file master when it opens a primary card node and
no master exists (`drm_master_open` in `drivers/gpu/drm/drm_auth.c`).
When the master file closes, the card has no master until the next
open. A file that was not master at its open can take master later
with `DRM_IOCTL_SET_MASTER` only if the process has `CAP_SYS_ADMIN`
(`drm_master_check_perm`). The same check lets a file that was master
before take it back from the same process, but the compositor's file
was never master. The compositor runs with no capability, so a
compositor that opens the card while another file is master never
gets master. Every atomic commit that it makes then fails with
`EACCES`.

The operator container opens `/dev/dri/<card>` read-write in
`readCurrentModes` and `readConnectorModes` (`currentmode.go`). The
slice pass, the Display pass, the placement pass, and each mode
prepare run these reads, and uevents, module reports, and API events
start the passes. The compositor container has no startup probe. So
the kubelet starts the operator container as soon as the compositor's
container runs, and the operator's first reads run while the
compositor opens the card.

The log store of the home cluster has the pod's lines for the
incident. The times are the log store's times, from one node:

| Time | Container | Line |
|---|---|---|
| 21:26:53.055 | weston | `the compositor takes card1` |
| 21:26:53.128 | operator | `operating the monitors on <node>` |
| 21:26:53.157 | weston | `using /dev/dri/card1` |
| 21:26:53.454 | operator | `slice: created generation 1, ...` |
| 21:26:53.877 | weston | `atomic: couldn't commit new state: Permission denied` |

The operator started 29 ms before the compositor opened the card, and
it read the card before its first slice write, 300 ms later. If one
of those reads held the card open when the compositor opened it, the
operator's file was master and the compositor's was not. When the
operator closed its file, the card had no master. We did not read
`/sys/kernel/debug/dri/<minor>/clients` before the pod was deleted,
so this is the likely cause, not a proven one. The log shows no other
opener. The only opens of the card node in this repository and in
`liken` are in `currentmode.go`.

With no master, the kernel's fbdev client owns the card. It sets a
mode for the console on each hotplug (`drm_fb_helper_hotplug_event`),
which it can do only while no master exists. That explains the
console on the panel. It probably also explains the repeated
`drm change` uevents, because a mode set on a port into an AV
receiver can make the receiver pulse its hotplug line. That second
part is a hypothesis.

Weston cannot recover by itself. With libseat's `noop` backend, it
never calls `SET_MASTER`, and the kernel would refuse the call. The
liveness checks do not see the failure. `probeCompositor` proves that
weston answers its clients, and a compositor without master still
answers them.

## The design

Three changes. The first orders the operator's reads after the
compositor's open. The second makes each read give master back if it
got it. The third restarts a compositor that has no master.

### 1. The operator opens the card only while a compositor serves it

Weston 14 loads its backend, which opens the card, before it creates
its listening socket (`wet_main` in `frontend/main.c` of 14.0.2:
`load_backends` at line 4654, `weston_create_listening_socket` at line
4706). So a connection to the socket is a connection to a compositor
that has already opened the card. An open by the operator after that
cannot take master from that compositor.

The output watch (`outputWatch` in `wayland.go`) already holds one
standing connection to the compositor. `opened()` runs when the
connection is up, and `closed()` runs when it ends. The watch gets a
`live` bit that `opened()` sets and `closed()` clears. Every read of
the card goes through one gate that opens the card only while `live`
is set. The gate costs no round trip, and it runs no timer.

While the compositor is absent, a read opens nothing and returns the
new error `errCompositorAbsent`. Each caller treats it as it treats a
failed read today, which costs the fields that the read fills. Two
callers change:

- The card observation (`recordObservation("card", ...)` in
  `reconcile`) does not count `errCompositorAbsent` as a failed
  observation. A compositor restart is not a broken card.
- The callers log `errCompositorAbsent` only when it starts, not on
  each pass.

When `opened()` sets `live`, it wakes the slice pass and the Display
pass, so the fields come back as soon as the compositor serves. A
restart costs the slice and each Display one write to remove the
fields and one write to add them back.

A hung compositor keeps its connection, and it keeps master, so the
reads continue while it hangs.

This gate orders every compositor start: the boot, a restart that the
operator orders, and a restart that the kubelet makes after weston
exits. One gap stays. `closed()` runs when the compositor's process
ends its connection. If weston closes the card before it closes its
client connections on its way out, a read in that gap can open a card
with no master. No new compositor can open the card before the old
container exits, so change 2 gives master back before a new compositor
exists. Change 3 then sees a false signal, which the pid check makes
harmless.

### 2. Each open of the card gives master back at once

After each open, and before any other ioctl, the operator calls
`DRM_IOCTL_DROP_MASTER` on its file. The kernel answers in one of
three ways (`drm_dropmaster_ioctl` and `drm_master_check_perm`):

- Success: the file was master, and the kernel dropped it. The card
  now has no master, and the next file that opens it becomes master.
- `EACCES`: the file was never master. This is the normal answer.
- Any other error: the read closes the file and returns the error. It
  reads nothing from a file that may still be master.

The drop comes before `GETCONNECTOR`. A master that calls
`GETCONNECTOR` with a zero mode count makes the kernel probe the
connector again (`drm_mode_getconnector` in `drm_connector.c`), and
the operator must never do that.

### 3. A compositor without master is restarted

A drop that succeeds means that no other file held master when the
operator opened the card. The gate opened the card only while a
compositor was live, and that compositor had already opened the card.
So that compositor does not hold master. The read sends a signal, and
one goroutine in `operate` restarts the compositor through the heal
path, with the new restart reason `masterless`. It logs one line that
names the card and says that the compositor has no DRM master.

The read does not call the restart itself, because `applyMode` reads
the card while it holds the `modeSwitches` lock, and the restart takes
the same lock. The signal goes on a buffered channel of size 1.

The check runs on each read, so it runs when the compositor becomes
live, on each uevent, and on each pass. No timer runs it.

The signal carries the compositor's pid from `compositorProcesses`
(the pod shares one process namespace). The goroutine restarts only a
pid that still runs, and it restarts each pid at most once. So the
restart cannot loop, a new compositor gets its own check, and a
signal about a compositor that has already exited ends nothing.

## What this plan does not cover

A process outside the pod that opens the card and keeps it open holds
master. No change here detects that, because the operator's drop then
answers `EACCES`. The layout module would refuse the compositor's
outputs with `no such output` in that case, which is a signal that a
later change can act on. No process outside the pod opens the card
today.

## What was considered and set aside

- **A startup probe on the compositor container.** It orders the boot
  only. And if the compositor never serves, the kubelet never starts
  the operator container. Then no DRA plugin registers, nothing taints
  the devices, and the old pod's slice stays up. That is worse than
  the race. The gate of change 1 orders every start, including the
  boot, and it never stops the operator.
- **A probe on the socket before each read.** Each read would cost a
  Wayland round trip, and a hung compositor would add the reply
  timeout to each read. The output watch already knows if a
  compositor is live.
- **A shared lock between the card reads and each restart.** The gate
  covers the same restarts, and it also covers a restart that the
  kubelet makes, which a lock cannot see.
- **The last good answer while the compositor is absent.** The mode
  that a connector runs changes when the compositor exits, because the
  console takes the card. A monitor can also arrive or leave in that
  time. An old answer would be wrong, and an error is true.
- **Read the modes from sysfs.** Sysfs has no refresh rate for a mode
  and not the mode that runs now. `currentmode.go` says why the reads
  use the ioctls.
- **Give the compositor `CAP_SYS_ADMIN`.** That is a large grant for
  one ioctl, and weston's `noop` backend does not call `SET_MASTER`.
- **Detect it from weston's log.** The log belongs to the kubelet, and
  the `Permission denied` lines are weston's words, not an interface.

## How we test it

- Unit tests:
  - The gate returns `errCompositorAbsent` and opens nothing while the
    watch is not live, and it reads while the watch is live.
  - A drop that succeeds sends one signal with the pid. A drop that
    fails with `EACCES` sends nothing. Any other error fails the read.
  - The goroutine restarts a pid once, and it skips a pid that no
    longer runs.
  - `opened()` wakes the passes.
  - `errCompositorAbsent` does not count as an invalid observation.
- A drill on liken-1:
  1. Roll the release and restart a node. Confirm that
     `/sys/kernel/debug/dri/<minor>/clients` names the compositor's
     pid as master.
  2. Make the failure on purpose. End the compositor, and open the
     card from a debug pod before the new compositor opens it, so the
     debug pod's file is master. Close that file after the new
     compositor serves. Wake a pass, for example with an annotation on
     the node's Display. Confirm the `masterless` restart and a
     compositor that shows frames.
