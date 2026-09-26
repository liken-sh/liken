# 70. Init attaches serio devices to their serial lines

Milestone 70. Proposed 2026-09-26. Some USB devices present a serial
line, and their kernel driver binds only after a program attaches the
serial line to the kernel's serio layer and keeps it attached. The
first of these is the Pulse-Eight USB-CEC adapter, which the
equipment-operator's CEC support needs. A new `spec.serio` field
declares each attachment. Init holds the attachment for the life of
the boot, and the machine operator's DRA driver publishes what the
attached driver creates: a CEC device and an input device.

## The problem

The [Pulse-Eight USB-CEC adapter](https://www.pulse-eight.com/p/104/usb-hdmi-cec-adapter)
puts a machine on an HDMI-CEC bus. CEC is the one-wire control
channel that every HDMI port carries. It lets a device wake a TV,
switch the inputs of a TV or a receiver, and receive the presses of
the TV's remote. The consumer of this milestone is the CEC support in
equipment-operator
([plan 09](https://github.com/liken-sh/equipment-operator/blob/main/plans/09-cec.md)).
display-operator's
[plan 23](https://github.com/liken-sh/display-operator/blob/main/plans/23-the-cec-physical-address.md)
supplies the address the adapter announces.

The vendored kernel ships everything the adapter needs as modules:
`cdc_acm`, `serport`, and `pulse8_cec`. `CONFIG_CEC_CORE` and
`CONFIG_MEDIA_CEC_RC=y` are set too. The adapter does not work with
modules alone, because of how its driver binds:

1. `cdc_acm` binds the USB interface and creates a serial line,
   `/dev/ttyACM0`.
2. `pulse8_cec` is a serio driver. Serio is the kernel's layer for
   input devices that talk over a byte stream: old serial mice,
   touchscreens, and these CEC adapters. A serio driver binds to a
   serio port, not to a tty.
3. A serio port exists only while a program holds the `serport` line
   discipline on the tty. The program opens the tty, sets 9600 baud
   raw mode, sets the line discipline with `TIOCSETD`, states the
   device type with `SPIOCSTYPE`, and then calls `read()`. The port
   exists for as long as that `read()` blocks. When the read returns,
   the kernel unregisters the port
   ([`serport.c`](https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git/tree/drivers/input/serio/serport.c?h=v7.2.6),
   `serport_ldisc_read`).
4. When the port exists, `pulse8_cec` binds to it and registers a CEC
   adapter, `/dev/cec0`. The CEC core also registers a remote-control
   input device for the TV remote's keys, with an event node under
   `/dev/input`
   ([`cec-core.c`](https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git/tree/drivers/media/cec/core/cec-core.c?h=v7.2.6)).

On a general-purpose distribution, `udev` starts a `systemd` service
that runs `inputattach --pulse8-cec /dev/ttyACM0`, and `inputattach`
holds the read. The kernel's
[CEC admin guide](https://docs.kernel.org/admin-guide/media/cec.html)
documents that setup. `liken` has no `udev`, no `systemd`, and no
`inputattach`.

A pod cannot do the attachment either, for two reasons:

- **The nodes appear after the container starts.** The machine
  operator's DRA driver delivers the device nodes that exist when a
  container is created. `/dev/cec0` and the event node appear only
  after the attachment, so a pod that attaches the line never
  receives the nodes it created.
- **`TIOCSETD` to `N_MOUSE` needs `CAP_SYS_ADMIN`.**
  `serport_ldisc_open` refuses the line discipline without it. No
  hardware operator runs with that capability today.

## Part one: `spec.serio`

The `Machine` spec gains one list. Each entry names a protocol from
the table in part two and the USB device whose serial line carries
it:

```yaml
spec:
  modules:
  - cdc_acm
  - serport
  - pulse8_cec
  serio:
  - protocol: pulse8-cec
    usb:
      vendor: "2548"
      product: "1002"
```

- `protocol` is one of the names in part two. The schema lists them
  as an enum, so a typo fails at `kubectl apply`.
- `usb.vendor` and `usb.product` are four lowercase hex digits, the
  same spelling as the `vendor` and `product` attributes that the DRA
  driver already publishes. The match uses the identity of the
  hardware, not the tty name, because `ttyACM0` and `ttyACM1` follow
  the order in which the devices were plugged in.
- `usb.serial` is optional. Without it, the entry attaches every
  serial line whose USB device matches the vendor and the product.
  With it, the entry attaches only the device that reports that
  serial number.

The entry does not load modules for itself. `cdc_acm`, `serport`, and
the protocol's driver must be in `spec.modules`, the same as every
other driver a machine uses. Part four describes how status names a
missing module.

`spec.serio` converges on the same terms as `spec.modules`. An added
entry converges live, through the same intent that loads an added
module (`init/liveload.go`), because an attachment is live-capable.
A removed entry stages for the next boot, the same as a removed
module, so the drift rules for the two fields stay one rule.

## Part two: the protocol table

The table is the one piece of device knowledge that `liken` keeps
for this milestone. Each name maps to the values that
[`inputattach.c`](https://sourceforge.net/p/linuxconsole/code/ci/master/tree/utils/inputattach.c)
uses for the same device, and the serio type comes from the kernel's
[`serio.h`](https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git/tree/include/uapi/linux/serio.h?h=v7.2.6):

| `protocol` | Serio type | Line settings | Driver module |
| --- | --- | --- | --- |
| `pulse8-cec` | `SERIO_PULSE8_CEC` (`0x40`) | 9600 baud, 8 data bits, raw | `pulse8_cec` |
| `rainshadow-cec` | `SERIO_RAINSHADOW_CEC` (`0x41`) | 9600 baud, 8 data bits, raw | `rainshadow_cec` |

The table starts with the two USB-CEC adapters the kernel supports.
`inputattach` supports many other serio devices, and a name joins
this table only when somebody examines that device on a `liken`
machine.

The attach sequence is the one `inputattach` runs, in five system
calls:

1. `open` the tty with `O_RDWR | O_NOCTTY | O_NONBLOCK`.
2. Set the termios: `CS8 | CREAD | HUPCL | CLOCAL` in the control
   flags, `IGNBRK | IGNPAR` in the input flags, no output or local
   flags, `VMIN` 1, `VTIME` 0, and the table's baud rate in both
   directions.
3. `ioctl(TIOCSETD, N_MOUSE)`. `N_MOUSE` is the line discipline
   number that `serport` registers.
4. `ioctl(SPIOCSTYPE, type)` with the table's serio type, and id and
   extra 0.
5. `read(fd, nil, 0)`, repeated on `EINTR` and `EAGAIN`.

## Part three: init holds the attachment

A new machine-plane component, `watchSerio`, keeps each declared
attachment in place. It has the same shape as `watchDiskLinks`: it
walks sysfs once at start, then again after every settled burst of
uevents from `hardware.ListenForUevents`. Each walk lists the ttys
under `/sys/class/tty`, reads the USB identity of each tty's parent
device, and compares it with the `spec.serio` entries. A matched tty
with no holder gets one.

A holder is one goroutine for one tty. It locks itself to its OS
thread with `runtime.LockOSThread`, blocks every signal on that
thread, runs the five calls from part two, and stays in the read.
The locked thread and the signal mask matter because init is PID 1.
It reaps every orphaned process on the machine, so `SIGCHLD` arrives
often. A signal that interrupts the read ends `serport_ldisc_read`,
and the kernel unregisters the port even when the call restarts.
The CEC adapter then disappears under every pod that holds it.

A holder ends only when its read returns. On an unplug, the kernel
hangs up the tty, `serport` marks the port dead, and the read
returns. The holder closes the tty and removes itself from the
registry. The uevent that the next plug sends starts a new walk, and
the walk starts a new holder. A restart of `watchSerio` itself does
not touch the holders, because the holders are in a registry that
outlives the component, the same as the reaper's.

The holders do not stop at shutdown. They write nothing to any disk,
so the quiesce has nothing to wait for, and the reboot system call
ends them. This keeps the port in place for as long as any pod runs.

**The machine-plane rule.** `components.go` admits a concern to the
machine plane only when the cluster cannot host it. The attachment
qualifies for the same reason that module loading does: it makes the
hardware appear, and the problem section shows that no pod can do
it. Init already does comparable work: `watchDiskLinks` keeps the
`/dev/disk` trees current.

**PID 1 must not fail.** `recover` in the machine plane catches a
panic in a holder, but it does not catch a fatal runtime error, and
a fatal error in PID 1 panics the kernel. The holder code therefore
uses no maps shared across goroutines, allocates nothing in its read
loop, and treats every error from the five calls as a status fact,
not a failure. The unit tests cover every error path of the sequence
with a fake file descriptor.

## Part four: what status reports

`status.serio` has one entry for each attachment that init holds or
tried to hold:

```yaml
status:
  serio:
  - protocol: pulse8-cec
    usb:
      vendor: "2548"
      product: "1002"
    tty: ttyACM0
    port: serio0
    state: Attached
    nodes:
    - /dev/cec0
    - /dev/input/event14
  conditions:
  - type: SerioAttached
    status: "True"
    reason: AllAttached
```

`state` is one of three values:

- `Attached`: the holder is in its read, and `port` names the serio
  port. `nodes` lists the device nodes the attached driver created,
  read from sysfs under the port.
- `Missing`: no tty matches the entry. The adapter is unplugged, or
  `cdc_acm` is not loaded.
- `Refused`: a tty matches, and one of the five calls failed. The
  message gives the call and the kernel's error text word for word,
  for example `TIOCSETD: operation not permitted`, or the missing
  module: `declare serport in spec.modules`.

The `SerioAttached` condition is `True` when every entry is
`Attached`. Its reason is `AllAttached`, `Missing`, or `Refused`, and
its message names the first entry that is not attached. The
condition does not change `Ready`. A machine with an unplugged
adapter works, and its workloads that do not use CEC run as before.

**The unclaimed report.** Today a Pulse-Eight appears in
`status.hardware.unclaimed` until `cdc_acm` binds it, with the
message `declare cdc_acm in spec.modules`. After `cdc_acm` binds,
the device leaves that list, but it still does not work. The report
keeps a USB device on the list while no `spec.serio` entry matches
it and its USB identity is one the protocol table lists as a known
adapter for a protocol. The message then names the rest of the fix:
`declare serport and pulse8_cec in spec.modules and a pulse8-cec entry in spec.serio`.
The table lists one adapter identity to start: the Pulse-Eight's
`2548:1002` for `pulse8-cec`.

## Part five: the fourth examined shape

`machine-operator/publishing.go` decides how one physical device
becomes the devices a `ResourceSlice` offers. It has three examined
shapes today: a graphics device splits into its card node, its render
node, and its wires. An audio controller keeps its jack inputs with
the card. A Bluetooth adapter is recognized by its driver. A serio
attachment is the fourth shape.

The delivery walk follows a device's sysfs subtree, and it stops
only at a nested PCI, USB, or Bluetooth device. The kernel places the
serio port under the tty, the CEC adapter under the port, and the
input device under the adapter. So after the attachment, the USB
interface of the adapter delivers nodes of three subsystems: `tty`,
`cec`, and `input`.

The policy for the shape:

- **The tty is never published.** The inventory skips the tty of a
  device that a `spec.serio` entry matches, before and after the
  attachment. The machine operator reads its own `Machine` spec for
  this. A pod that received `/dev/ttyACM0` could change the line
  discipline or write to the line and take the port down under every
  other claim. This is the same rule the inventory applies to a disk
  that holds a storage role.
- **The CEC node publishes as the primary device**, with the bare
  name and the `subsystem` attribute `cec`.
- **The input node publishes as a second device** with the suffix
  `-input` and the `subsystem` attribute `input`.
- **Both devices are exclusive.** The CEC core allows several opens
  of one adapter, but it lets only one process be the exclusive
  initiator or follower, and two programs that configure logical
  addresses on one adapter undo each other. The input device carries
  one remote's key presses, and one reader must own them.

The two devices answer different claims. The CEC device is the bus,
which the equipment-operator claims to send and receive CEC
messages. The input device is the TV remote, which a media `Remote`
claims the same way it claims a Bluetooth remote. This is the
opposite of the audio shape, where the jack inputs report the state
of the output the same claim plays through. Both devices carry the
physical device's attributes, so a claim can pair them with
`matchAttribute` on `address`.

The CEC core registers no LIRC node for this input device. The
remote-control core skips `lirc_register` when a device's only
protocol is CEC
([`rc-main.c`](https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git/tree/drivers/media/rc/rc-main.c?h=v7.2.6),
`rc_register_device`), so the delivery holds no `lirc` subsystem.

Before the attachment, a matched adapter publishes nothing, because
its only node is the tty. At boot, init attaches the line long before
the machine operator publishes its first slice, so the first slice
already holds both devices.

**An unplug removes both devices from the slice.** A pod that holds
them keeps file descriptors for nodes that are gone, and DRA does not
evict it. The contract for a claimant is that a read or an `ioctl`
that returns `ENODEV` ends the program. The kubelet then restarts the
container, and a new container receives the nodes that the claim's
CDI spec names at that moment. The machine operator rewrites that
spec on every pass, so when the adapter comes back on the same USB
port, the devices return with the same names and the new nodes, and
the claimant runs again with no new allocation. A published name
follows the USB port path, so an adapter moved to another port is a
different device, and its claimant needs a new pod.
equipment-operator's plan 09 and media-operator's plan 35 carry that
contract.

## The drills

`vivid`, the kernel's virtual media driver, emulates CEC adapters,
and it is how the consumers test their CEC code. It cannot test this
milestone, because it has no serial line and no serio port. The
proof is unit tests plus a real adapter.

The unit tests cover the protocol table, the walk against a fake
sysfs tree with matching and non-matching ttys, the status derivation
for each state, the unclaimed message, and the publish policy for a
delivery of `tty`, `cec`, and `input` nodes.

The drills run on a testbed machine that has a Pulse-Eight on a
spare HDMI input of a receiver:

1. **Boot.** With the three modules and one `pulse8-cec` entry
   declared, `status.serio` reports `Attached` with a CEC node and an
   event node. The slice publishes the `cec` device and the `-input`
   device, and no device delivers the tty.
2. **Live add.** An entry added to a running machine attaches within
   one walk, with no reboot.
3. **Unplug and plug.** Unplugging the adapter moves the entry to
   `Missing` and removes both devices from the slice. Plugging it
   into the same port attaches it again within seconds, with the same
   device names.
4. **Signals.** The port number in `status.serio` stays the same for
   one hour while pods start and stop on the machine, which sends
   `SIGCHLD` to init many times.
5. **A claim.** A pod claims the CEC device and runs `cec-ctl` from
   [v4l-utils](https://git.linuxtv.org/v4l-utils.git) to claim a
   playback address and list the bus. `cec-ctl --show-topology`
   reports the TV and the receiver.
6. **A missing module.** With `pulse8_cec` removed from
   `spec.modules`, the entry reports `Refused` and names the module.

## What was considered and set aside

- **A privileged pod that runs `inputattach`.** The nodes it creates
  never reach its own container, it needs `CAP_SYS_ADMIN`, and every
  restart of the pod removes the port under the pods that claimed it.
- **A hardware operator that claims the tty and publishes the
  result,** the way bluetooth-operator claims a radio. It works: the
  operator never needs the new nodes itself, and it can write CDI
  specs that name them. It adds an operator whose only work is to
  hold one read open, and a restart of that operator removes the port
  under every claimant. The attachment is closer to loading a driver
  than to anything an operator decides.
- **The machine operator holds the read.** It restarts on every
  operator upgrade, and each restart would remove the port under
  every claimant.
- **Matching by tty name.** The name follows the order in which the
  devices were plugged in.
- **Shipping `inputattach`.** The attachment is five system calls,
  and the image ships no C userland for one tool.

## Open questions

- **A child process in place of a goroutine.** `components.go` allows
  a child process for a concern that must not take the machine down
  when it fails fatally. If drill four shows that a blocked signal
  mask on a locked thread does not hold in PID 1, the holders move to
  a child process of the same binary, which can block every signal
  for its whole life.
- **A device taint for an unplugged adapter.** DRA device taints
  could evict a claimant at once, where the `ENODEV` contract relies
  on the claimant. The DRA driver publishes no taints today, and this
  milestone does not add the first one.
- **The adapter's EEPROM.** With the `pulse8_cec` parameter
  `persistent_config=1`, the driver stores the adapter's addresses in
  its EEPROM, and the adapter can then answer on the bus by itself at
  the next power-up. The default is 0, and `spec.moduleParameters` can
  set it. Whether `liken` should refuse that value is open.
- **A second identical adapter with no serial number.** An entry
  without `usb.serial` attaches both. It is not yet confirmed that
  the Pulse-Eight reports a USB serial number, so two such adapters
  on one machine may not be separable by the spec. No known setup
  has two.
