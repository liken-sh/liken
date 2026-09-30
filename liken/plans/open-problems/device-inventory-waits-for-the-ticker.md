# The device inventory waits for the ticker

Open problem. The machine operator publishes a hot-plugged device in
the node's `ResourceSlice` only on its next ten-second pass, not when
the kernel reports the device. The inventory finds hardware changes by
walking sysfs again on a timer. The repository's rule for events says
a timer that reads state again to find a change is a defect.

## What happens

The machine operator's reconcile loop in
[`main.go`](../../machine-operator/main.go) runs a pass on three
signals: an API watch, a change to init's facts tree, and a ten-second
ticker. Each pass calls `publishDeviceInventory` in
[`dra.go`](../../machine-operator/dra.go), which walks sysfs and writes
the slice. Each pass also calls `refreshCDISpecs`, which writes a
replugged device's new usbfs node into the claims the kubelet prepared.

init listens for uevents in
[`init/hardware.go`](../../init/hardware.go), but it writes the facts
tree only when the unclaimed report or the list of block devices
changes. A device that a kernel driver binds changes neither. For
example, a USB serial adapter binds `pl2303` or `ftdi_sio`, publishes a
tty, and writes no facts. The operator does not wake, and the device
reaches the slice on the next tick, up to ten seconds after the kernel
reported it. The same delay applies when a device leaves, and when a
replug gives a device a new usbfs node.

The comment on the ticker gives it two jobs. It is the heartbeat's
clock, which is a correct use of a timer. It is also the backstop for
state that no event announces, such as a sysctl or an `/etc/hosts`
entry that another process changes. sysfs is not in that group: the
kernel announces every change to it with a uevent. The comment at the
top of `dra.go` defends the walk on each pass by its cost, which is
small, but the objection to the timer is latency and the rule, not
cost.

## Candidate direction

The machine operator opens its own uevent listener with
`hardware.ListenForUevents`, the function init uses. It settles each
burst with the same quiet interval and ceiling as `settle` in
`init/hardware.go`, and then it wakes the reconcile pass. The listener
opens before the first pass, so a device that arrives during the first
walk still sends a signal after it. When the listener fails, the
operator opens it again and runs a pass.

The ticker stays as the heartbeat's clock and as the backstop for the
state with no events. The comments in `main.go` and `dra.go` then state
that a uevent drives the inventory and the CDI refresh.

The settle has a cost to check. A node that runs Kubernetes sends
uevents continuously while containers start and stop, because each
veth pair announces itself. The ceiling in `settle` limits how long a
burst delays a pass. The operator then walks sysfs on most bursts,
which can happen more often than once every ten seconds on a busy node.

## Verification needed

- Plug in a USB serial adapter with a driver and measure the time from
  the uevent to the device in the `ResourceSlice`, before and after
  the change.
- Replug a claimed libusb device and confirm that the claim's CDI
  specification names the new usbfs node before the pod restarts.
- Count the sysfs walks per minute on a node that starts and stops
  pods, and compare the count with the six walks per minute the ticker
  gives today.
