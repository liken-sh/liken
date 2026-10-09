# 77. The machine operator hears the machine

Milestone 77. Proposed and built 2026-10-09. Every drill that QEMU can
run has run on `node-1`, and the results are in
[What the lab measured](#what-the-lab-measured). Two checks need
hardware that QEMU does not emulate and have not run: a ZWO camera
under its vendor's SDK, and a USB serial adapter on a physical
machine.

The second of five milestones that remove the ten-second ticker from
`machine-operator`'s reconcile loop.
[Milestone 76](76-a-pass-reports-what-it-did-not-finish.md) gives the
series and the table of every job the ticker does. This milestone
gives the operator the kernel's events for the state on the machine
that the ticker finds today: uevents for sysfs, and inotify for
`/etc/hosts`. It widens the device inventory in the same change,
because the uevent filter has to match what the inventory reads: the
inventory now reads every device that owns a device node, not only
the devices on the PCI and USB buses, and it publishes a USB device
that no kernel driver binds. It replaces the open problems "The device
inventory waits for the ticker" and "Driverless USB devices are not
published".

## What happens now

Each pass calls `publishDeviceInventory` in
[`dra.go`](../../machine-operator/dra.go), which walks the PCI and USB
buses in sysfs and writes the slice, and `refreshCDISpecs` in
[`cdi.go`](../../machine-operator/cdi.go), which writes a replugged
device's new usbfs node into the claims the kubelet prepared.

init listens for uevents in [`init/hardware.go`](../../init/hardware.go),
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

`applyHostEntries` in [`hosts.go`](../../machine-operator/hosts.go)
writes the declared entries into `/host/etc/hosts`, the host's `/etc`
that the pod mounts. When another process rewrites the file and drops
the entries, the operator writes them back on the next tick.

`machine.WatchFactsTree` can fail to start, and the operator then runs
on the ticker alone and starts the watch again on a later tick
(`main.go`). In production the facts directory always exists before
the operator starts: init publishes the facts before it writes
`/run/liken/machine.yaml` and before it starts k3s (`init/main.go`),
and the operator exits when `machine.yaml` is missing.

The inventory itself reads two buses. `hardware.DiscoverDevices` walks
`/sys/bus/pci` and `/sys/bus/usb`, and `inventoryDevices` publishes a
device only when a driver has bound it. Hardware that the firmware
describes and no bus enumerates, such as a firmware TPM, a laptop's
keyboard behind the i8042 controller, or a serial port on the board,
is not in the walk at all. A USB device that no kernel driver binds,
such as a ZWO camera that its vendor drives through libusb, is in the
walk and never publishes. The open problem "Driverless USB devices are
not published" records the second gap.


## The design

**Uevents.** The operator opens its own listener with
`hardware.ListenForUeventsMatching`, beside init's
`hardware.ListenForUevents`. A uevent for a device that is not a
network device reaches every network namespace that the initial user
namespace owns, and the pod runs with `hostNetwork: true`, so the
listener receives every device's events. The listener reads the
device path and the subsystem of each event, and passes them to a
match that the caller gives.

A relay goroutine settles each burst with a one-second quiet interval
and a five-second ceiling, the values of init's hardware watch, and
then wakes the loop. The settle runs in the relay, not in the loop, so
a burst that holds the settle at its ceiling does not delay a wake
from a watch. `settle` moved from `init/hardware.go` into the
`hardware` package as `Settle`, and init's four callers keep their own
intervals.

**The filter.** The proposal dropped every event under
`/devices/virtual/`. The built filter keeps the misc class under it.
`claimDelivery` adds `/dev/uhid` to a Bluetooth adapter's claim when
the machine has that node, and the node appears under
`/devices/virtual/misc/uhid` when the `uhid` module loads. A filter
that dropped it would leave the claim's CDI specification without the
node until some other event woke a pass. `hardware.InventoryEvent`
states the match: every event outside `/devices/virtual/`, and the
events under `/devices/virtual/misc/`. It lives beside the inventory
walk, because the two must change together.

A finer filter is not needed. On `node-1`, every event that pods
starting and stopping sent was a `net` or `queues` event under
`/devices/virtual/net/`, and nothing else came from
`/devices/virtual/`. A container that crash-loops sends none, because
the kubelet restarts the container in the same sandbox, and the
sandbox keeps its veth pair.

**The inventory outside PCI and USB.** `hardware.DiscoverInventory`
lists what `DiscoverDevices` lists, then each USB device apart from
its interfaces, then the board devices. A board device is found from
its nodes: the walk reads every link in `/sys/dev/char` and
`/sys/dev/block`, drops the nodes under `/devices/virtual/` and under
a PCI or USB device, and names the outermost device above each node
that has a driver. On `stick-1` the keyboard's event node is under an
input device, under the serio port that `atkbd` binds, under the
platform device that `i8042` binds, so the controller is the device.
The delivery walk from it collects every node beneath, the same way it
does from a PCI device. The publish rule is the same for every device:
driven, with nodes to deliver, and not a storage role. A device name
replaces every character a DNS label cannot hold with a dash, because
a firmware name such as `MSFT0101:00` or `acpi.video_bus.0` uses them,
and stops at 63 characters.

The unclaimed report and the hardware report keep the PCI and USB
walk. They answer which module to declare, and a board device that has
no driver has no module in `spec.modules` that would help.

**Serial ports with no UART.** The 8250 driver reserves 32 ports at
boot and registers a tty for each, whether a UART answers or not. Both
machines on the testbed have 32 such ports and no UART. A port with no
UART reports `type` 0, and the delivery walk skips its node, so the
`serial8250` platform device delivers nothing on those machines.

**USB devices with no kernel driver.** A USB device whose every
interface has no driver publishes whole, by its port path, such as
`usb-1-2`, and a claim on it delivers its usbfs node. The class
attributes come from its first interface. The kernel gives an
interface a `modalias` and gives the device none, so `DiscoverDevices`
never listed the device itself, and `DiscoverInventory` lists it.

The open problem offered a rule for each driverless interface. The
built rule asks that no interface of the device has a driver, and that
no program holds one through usbfs. On `liken-1`, the HID interface of
a USB audio adapter has no driver while `snd-usb-audio` drives its
audio interface. A claim on that interface would carry the usbfs node
of the whole adapter, and a pod that held it could reset the adapter
under the player that holds the audio. A device that a program holds
through usbfs leaves the slice until the program lets go, so a second
claim cannot arrive while the first one uses it. A device whose module
is not loaded yet publishes whole until the module binds an
interface, which is the cost the open problem named.

**Nodes the machine holds.** The board walk reaches two nodes that
belong to the machine: the console, which the kernel lists in
`/sys/class/tty/console/active`, and `/dev/rtc0`, which init writes
the system clock into. The delivery drops both before the publish
rule, and `claimDelivery` drops them too, so a claim never resolves
to them. The test removes the node, not the device, so a serial
controller with the console on one port keeps its other ports. On
`node-1`, QEMU puts `rtc0` under the ISA bridge, a PCI device, so the
rule covers a PCI device's delivery as well.

**`/etc/hosts`.** `machine.WatchName` watches a directory for events
on one name, with the mask `IN_CREATE`, `IN_MOVED_TO`,
`IN_CLOSE_WRITE`, `IN_DELETE`, and `IN_MOVED_FROM`. An overflow still
wakes it. The operator watches `/host/etc` for the name `hosts`. The
operator's own write ends with a rename onto `hosts`, so it wakes one
more pass, which writes nothing. A pod from an older template has no
`/host/etc` mount, and the operator runs without the hosts watch.

**A tick reads nothing that has an event.** A pass that only the
ticker woke reuses the last result of the sysfs walk and of the hosts
reconcile, and reads neither one. `localReads` in `machineevents.go`
keeps the two results, and the loop marks each pass that the ticker
alone woke. A read whose last attempt failed runs on every pass until
it succeeds, because the retry timer that answers the failure can
arrive after a tick, and a tick's pass that skipped the read would
drop the failure from its outcome and stop the timer.

The tick was also the only thing that wrote back a `ResourceSlice`
that somebody deleted. The kubelet deletes a driver's slices when it
starts, so a restart of k3s deletes the slice. The slice watch now
wakes the loop on a delete, which was milestone 78's row. It wakes on
nothing else, because the operator's own write is an update, and
milestone 78 gives the reason an update must not wake until
`WriteResourceSlice` compares against what the server returned. The
fake API server learned `DELETE` for the test.

**A writer that fights the pass.** The hosts relay settles its events
the same way the uevent relay does. The pass's own write to
`/etc/hosts` sends an event, and a process that kept writing another
file would otherwise trade writes with the pass as fast as both could
write. With the settle, a fight costs a pass every five seconds at
most, and a single write from another process is undone about a
second later.

**The sysctls stay on the tick.** Milestone 76's table says
`/proc/sys` has no events. A test in a privileged container found
that a write through `/proc/sys` does send `IN_MODIFY` and
`IN_CLOSE_WRITE`, so a sysctl watch was built, and the drill on
`node-1` showed it does not work. Each mount of procfs has inodes of
its own, and a watch sees only the writes made through its own mount.
A write through a debug pod's `/proc` reached a watch on that `/proc`
and not a watch on `/host/proc`, and the other way round. Each
container mounts its own `/proc`, so a watch in the operator's pod
sees only the operator's writes. The watch was removed, and the
sysctls stay with milestone 80's check. A value that the kernel
changes as a side effect of another write has no event on any mount
either: a write to `net.ipv4.ip_forward` sets
`net.ipv4.conf.all.forwarding`, and that file got none.

The same drill found a bug in `applySysctls`. It applied the OS
defaults and then `spec.sysctls`, so a name in both, such as the dev
cluster's `vm.max_map_count`, was written twice on every pass: the
default, and then the spec's value. The kernel held the default for a
moment on each pass, and with the sysctl watch in place the writes
woke 60 passes a minute. The operator now skips a default that the spec
overrides. init still applies both at boot, default first.

**A reader that stops.** The proposal made a facts watch that fails to
start fatal. The built loop ends the process for every reader that
fails: the facts watch that cannot open or that closes its channel,
and the uevent listener or the hosts watch that closes its channel
after it opened. `loop.run` returns the error, and `main` exits, so the
kubelet starts the container again with its backoff and the new
process opens its readers before its first pass. A reader stops only
on a poll error or a descriptor that is no longer open, and nothing
in the process can repair either one. The retry of the facts watch on
the ticker is gone, so no reader depends on the ticker.

## Tests

The loop's tests run in a `synctest` bubble with
`kubernetes/apiservertest`, with the readers as channels the test
sends on:

- A uevent wakes a pass one quiet second later, and that pass publishes
  a sound card that the fake sysfs gained.
- Twelve uevents 10 ms apart make one pass.
- A hosts event wakes a pass that writes the file back.
- A tick wakes a pass that publishes no device that arrived with no
  uevent and leaves a changed hosts file alone. The uevent's pass then
  publishes the device. A tick reruns a read whose last attempt
  failed.
- A delete of the slice wakes the loop, and an update does not.
- Two hundred hosts events over twenty seconds make at most four
  passes.
- A pass on a machine that already holds an overriding sysctl writes
  nothing.
- The uevent listener or the hosts watch that closes its channel ends
  the loop with an error that names it. A facts watch that dies or
  cannot open ends the loop too, and one that cannot open ends it
  before the first pass.

The hardware tests build sysfs trees in the shape the testbed showed:
a TPM, an i8042 keyboard two driven devices down, a power button, an
`acpi.video_bus.0` device under the PCI root that is not a PCI device,
a speaker with no driver, a GPU, and a misc node. The board walk names
the four driven board devices once each. A serial port of type 0
delivers nothing. A USB device is listed beside its interfaces, and a
card reader in a fake sysfs publishes as `usb-1-2` and resolves to its
usbfs node. The filter keeps the PCI, platform, pnp, and misc paths
and drops the veth and loop paths.

The real readers block in `poll`, which is not durably blocked inside
a bubble, so these run outside a bubble on real descriptors:

- The listener drops a veth event that the match refuses, and wakes
  for a USB event behind it.
- `WatchName` wakes for a rename onto `hosts` and for its delete, and
  not for a write or a rename of `resolv.conf` in the same directory.

## What the lab measured

`node-1` of the `lab` fleet, on 2026-10-09, under UEFI with the virtio
hardware shape, with a QMP socket and a `qemu-xhci` controller for
hot-plug. `make smoke-uefi` reported Ready after 20 and 21 seconds on
the two builds.

Before the change, on the build of milestone 76:

- Pods starting and stopping sent 132 uevents in two minutes, all of
  them `net` and `queues` under `/devices/virtual/net/`. A container
  that crash-looped sent none.
- A hot-plugged USB keyboard reached the slice 2.9 seconds after
  `device_add`. That was the phase of the ticker, and the limit was
  ten seconds.
- A USB CCID reader, which has no kernel driver, never reached the
  slice.

After the change:

- The slice held `platform-i8042` and `platform-lnxpwrbn-00` beside
  the PCI device. The console, `ttyS0` on `pnp/00:04`, and `rtc0` were
  withheld. `ttyS1` to `ttyS3` reported type 0 and delivered nothing.
- A hot-plugged USB tablet sent its last uevent at 14:28:58.266, and
  the slice held it at 14:28:59.363, 1.5 seconds after `device_add`.
  The first time is the guest's clock and the second is the host's,
  and the two agreed to the second. The slice was read every 0.2
  seconds.
- The CCID reader published as `usb-1-2` with class code `0b`. A USB
  serial adapter whose `ftdi_sio` module was not loaded published
  whole as `usb-1-4`, 1.65 seconds after `device_add`. A `spec.modules`
  entry for `ftdi_sio` loaded the module, and the slice held
  `usb-1-4-1-0`, a `tty` device, 0.2 seconds after the patch, because
  init's facts write woke the pass before the uevents settled.
- Two unplugs left the slice 1.1 and 1.3 seconds after `device_del`.
- An unprivileged pod with a claim on the reader received
  `/dev/bus/usb/001/003`. After an unplug and a plug into the same
  port, the claim's CDI specification named `/dev/bus/usb/001/006`
  within two seconds, while the pod ran, and the next pod on the claim
  received `/dev/bus/usb/001/006`.
- A write over `/etc/hosts` from a debug pod was gone at the second
  check, 10 ms after the first, three times out of three, and the file
  was identical to the operator's render.
- Pods starting and stopping sent 159 `net` and `queues` uevents in two
  minutes. The operator ran 14 passes in those two minutes and 13 in
  the two idle minutes before them. The ticker accounts for 12.

A third build added the tick that reads nothing and the slice's
delete wake. `make smoke-uefi` reported Ready after 15 seconds. QEMU
then gave `node-1` a TPM 2.0 from `swtpm`, on QEMU's `tpm-crb` device,
and a second 16550 UART at `ttyS1`:

- The slice held `platform-msft0101-00`, bound by `tpm_crb_acpi`, the
  driver the testbed's TPMs have, and `pnp-00-00`, a `tty` device for
  the new UART. The console's `ttyS0` stayed withheld.
- An unprivileged pod with a claim on each received `/dev/tpm0`,
  `/dev/tpmrm0`, and `/dev/ttyS1`, and not `/dev/ttyS0`. It sent
  `TPM2_GetRandom` for 8 bytes to `/dev/tpmrm0`, and the TPM answered
  with the success code and 8 bytes. The container ran as its own
  root user, which holds no privilege on the host.
- `insmod` of `uhid` from a debug pod, 4 seconds after a ticker pass,
  sent `add@/devices/virtual/misc/uhid`, and a pass ran 1.0 second
  later. The next tick was 6 seconds away, and nothing wrote the facts,
  so the misc event woke that pass.
- Two deletes of the slice: it was back 48 and 44 ms after `kubectl
  delete` returned.
- A write over `/etc/hosts` was gone at the second check, 10 ms after
  the first.

The testbed has two physical machines, `liken-1` and `stick-1`, on
release 2026.10.08-003, a build from before milestone 76. A read-only
survey of their sysfs found what the board walk would publish:

- On both, a firmware TPM under `platform/MSFT0101:00`, bound by
  `tpm_crb_acpi`, the ACPI power button, and the brightness keys of
  `acpi.video_bus.0`. `liken-1` also has a sleep button, and `stick-1`
  has the i8042 keyboard.
- On both, `rtc0` on the platform bus, which the held nodes withhold,
  and 32 `serial8250` ports of type 0, which deliver nothing.
- On both, the console is `tty0`, which is under `/devices/virtual/`.
- On `liken-1`, one USB interface with no driver, the HID interface of
  the USB audio adapter whose audio interface `snd-usb-audio` drives.
  The device keeps its driven interface, so it does not publish whole.
- No uevent arrived in 120 seconds on either idle machine.

The final build settles the hosts events, applies an overriding
sysctl once, and has no sysctl watch. `make smoke-uefi` reported Ready
after 10 seconds:

- An idle `node-1` ran 12 passes in two minutes, the ticker's pace.
  The build with the sysctl watch had run 60 a minute.
- A write over `/etc/hosts` was undone about one second later, three
  times out of three. A process that wrote the file every 200 ms for
  20 seconds caused 5 passes, two of them the ticker's, and the file
  held the declared entries afterward.
- A write to `vm.max_map_count` from a debug pod was undone at the next
  tick, 1.5 and 5.7 seconds later.
- A hot-plugged USB keyboard and a USB CCID reader reached the slice
  1.46 and 1.48 seconds after `device_add`.

## Verification needed

- Plug a USB serial adapter into a physical machine, measure the time
  to the slice, and replug it under a running claim.
- Run a ZWO camera's SDK in a pod under a claim on the published
  device, with no privilege and no host path.
