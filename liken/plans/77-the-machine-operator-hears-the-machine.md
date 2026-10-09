# 77. The machine operator hears the machine

Milestone 77. Proposed 2026-10-09. The second of five milestones that
remove the ten-second ticker from `machine-operator`'s reconcile loop.
[Milestone 76](76-a-pass-reports-what-it-did-not-finish.md) gives the
series and the table of every job the ticker does. This milestone
gives the operator the kernel's events for the state on the machine
that the ticker finds today: uevents for sysfs, and inotify for
`/etc/hosts`. It replaces the open problem "The device inventory waits
for the ticker".

## What happens now

Each pass calls `publishDeviceInventory` in
[`dra.go`](../machine-operator/dra.go), which walks the PCI and USB
buses in sysfs and writes the slice, and `refreshCDISpecs` in
[`cdi.go`](../machine-operator/cdi.go), which writes a replugged
device's new usbfs node into the claims the kubelet prepared.

init listens for uevents in [`init/hardware.go`](../init/hardware.go),
but it writes the facts tree only when the unclaimed report or the
list of block devices changes. A device that a kernel driver binds
changes neither. For example, a USB serial adapter binds `pl2303` or
`ftdi_sio`, publishes a tty, and writes no facts. The operator does
not wake, and the device reaches the slice on the next tick, up to
ten seconds after the kernel reported it. The same delay applies when
a device leaves, and when a replug gives a device a new usbfs node.
The comment at the top of `dra.go` defends the walk on each pass by
its cost, which is small, but the objection to the timer is latency
and the rule, not cost.

`applyHostEntries` in [`hosts.go`](../machine-operator/hosts.go)
writes the declared entries into `/host/etc/hosts`, the host's `/etc`
that the pod mounts. When another process rewrites the file and drops
the entries, the operator writes them back on the next tick.

`machine.WatchFactsTree` can fail to start, and the operator then runs
on the ticker alone and starts the watch again on a later tick
(`main.go`). In production the facts directory always exists before
the operator starts: init publishes the facts before it writes
`/run/liken/machine.yaml` and before it starts k3s (`init/main.go`),
and the operator exits when `machine.yaml` is missing.

## The design

**Uevents.** The operator opens its own listener with
`hardware.ListenForUevents`, the function init uses. A uevent for a
device that is not a network device reaches every network namespace
that the initial user namespace owns, so the listener receives the
events for PCI and USB devices. The pod runs with `hostNetwork: true`,
so it also receives the events for the host's network devices.

The listener already wakes only on `add`, `remove`, `bind`, and
`unbind`, and it wakes when the socket overflows and drops events. The
operator's listener also drops every event whose device path starts
with `/devices/virtual/`. A node that runs Kubernetes sends those
events while containers start and stop, because each veth pair and
each of its queues announces itself, and a crash-looping pod keeps
them coming for minutes. The inventory reads only the devices under
`/sys/bus/pci` and `/sys/bus/usb` and the subtrees beneath them, so an
event under `/devices/virtual/` cannot change it. The filter is an
option of the listener, not a change to `hardwareChanged`, because
init's disk inventory reads `/devices/virtual/block`.

A goroutine settles each burst with a one-second quiet interval and a
five-second ceiling, the values of init's hardware watch, and then
sends on the loop's `wakes` channel. The settle runs in the goroutine,
not in the loop, so a burst that holds the settle at its ceiling does
not delay a wake from a watch. `settle` moves from `init/hardware.go`
into the `hardware` package, next to `ListenForUevents`, because the
operator is its second user. init's four callers (`hardware.go`,
`report.go`, `serio.go`, and `disklinks.go`) keep their own intervals
and call the moved function, and its tests move with it.

The listener opens before the first pass, so a device that arrives
during the first walk still sends a wake after it. When the listener's
channel closes, the loop opens it again and runs a pass, as milestone
76 describes.

**`/etc/hosts`.** A new helper in `machine/inotify.go` watches a
directory for events on one name, with the mask `IN_CREATE`,
`IN_MOVED_TO`, `IN_CLOSE_WRITE`, and `IN_DELETE`. `WatchDirMask` does
not do this: it discards the event name and wakes on every event in
the directory. With it, every write to the host's `/etc` would wake a
pass, including init's writes of `/etc/resolv.conf` and the
operator's own temporary file. The operator watches `/host/etc` for
the name `hosts`. The watch is on the directory, because a rename
replaces the file's inode, and a watch on the file would stop at the
first rename. The operator's own write ends with a rename onto
`hosts`, so it wakes one more pass. That pass finds the entries in
place and writes nothing, so the loop settles.

A pod from an older template has no `/host/etc` mount. The operator
then logs the error, runs without the hosts watch, and
`hostEntriesCondition` reports the missing mount as it does now.

**The facts watch.** A `WatchFactsTree` that fails to start is fatal.
The facts directory exists before the operator starts, so a failure
means something is wrong with the machine, and the operator exits so
the kubelet starts it again, the crash-only rule at the head of
`main.go`. The retry on the ticker goes.

## Tests

The loop function from milestone 76 takes the uevent channel and the
hosts channel as arguments. In a `synctest` bubble:

- A uevent wakes a pass, and that pass publishes a device that a fake
  sysfs tree gained.
- A burst of uevents settles into one pass.
- A hosts event wakes a pass that writes the entries back.

The real readers block in `poll`, which is not durably blocked inside
a bubble, so these run outside a bubble with a context timeout:

- The listener's filter drops an event under `/devices/virtual/net/`
  and passes one under `/devices/pci0000:00/`.
- The name-filtered inotify helper wakes for a rename onto `hosts`
  and not for a write of `resolv.conf` in the same directory.

## Verification needed

- Plug in a USB serial adapter with a driver, and measure the time
  from the uevent to the device in the `ResourceSlice`, before and
  after the change.
- Replug a claimed libusb device, and confirm that the claim's CDI
  specification names the new usbfs node before the pod restarts.
- Count the passes per minute on a node that starts and stops pods, and
  confirm that the veth churn wakes none.
