# Working on cec

This directory holds the HDMI-CEC driver: a Go binding of the Linux
kernel's CEC API, the messages this operator sends and reads, and the
scan and follower logic built on them. `cectest/` is a CEC bus in
memory that answers the same ioctls, for tests. `plans/completed/09-cec.md`
gives the design.

## The references

- The kernel's
  [CEC API](https://docs.kernel.org/userspace-api/media/cec/cec-api.html)
  documents each ioctl, each struct field, and each error. The
  [CEC introduction](https://docs.kernel.org/userspace-api/media/cec/cec-intro.html)
  explains the addresses, and the
  [CEC admin guide](https://docs.kernel.org/admin-guide/media/cec.html)
  covers the drivers.
- `linux/cec.h` is the source of every struct layout and request
  number in `kernel.go`. `kernel_test.go` pins each size, offset, and
  request number against values compiled from the header with
  `sizeof`, `offsetof`, and the `_IOR`/`_IOW`/`_IOWR` macros.
  `linux/cec-funcs.h` shows the operand layout of each message.
- CEC is Supplement 1 of the HDMI specification. HDMI 1.3a with its
  CEC supplement is free from the
  [HDMI specification page](https://www.hdmi.org/spec/index) after a
  form. Later supplements are available only to HDMI adopters.
- The first adapter is the Pulse-Eight USB-CEC adapter
  ([product page](https://www.pulse-eight.com/p/104/usb-hdmi-cec-adapter)).
  The kernel's `pulse8-cec` driver presents it through the CEC
  framework, so this package does not use libCEC. The notes at the
  top of
  [`pulse8-cec.c`](https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/drivers/media/cec/usb/pulse8/pulse8-cec.c?h=v7.2)
  describe the adapter's autonomous mode.
- [`cec-ctl`](https://www.mankier.com/1/cec-ctl),
  [`cec-follower`](https://www.mankier.com/1/cec-follower), and
  [`cec-compliance`](https://www.mankier.com/1/cec-compliance) from
  [v4l-utils](https://git.linuxtv.org/v4l-utils.git) check this code
  by hand.

## The message families

`message.go` groups the opcodes by family, and each group's comment
says who sends it and what answers it. The package uses four:

- **Identity.** Give Physical Address, Give OSD Name, Give Device
  Vendor ID, and Get CEC Version, with their reports. A scan asks each
  device all four. `Introduce` asks one device only for the facts the
  directory does not hold, for a device that announced itself after
  the scan.
- **Power.** Give Device Power Status and Report Power Status.
  `ReadPower` asks one device and records the answer in the
  directory. After two questions in a row that a device acknowledges
  and does not answer, the directory forgets the power it held. One
  missed answer keeps it, because a TV that wakes can miss one.
  Between reads the directory sets the TV's power from the messages
  that change it: a Standby to the TV or to every device, Image View
  On and Text View On, a Routing Change, Set Stream Path, or Request
  Active Source from the TV, and an Active Source from any device.
  Nothing in this package asks on a timer: a question is traffic, and
  some TVs answer traffic they did not expect by switching their own
  input.
- **One-touch play.** Image View On, Standby, and Active Source.
  `PowerCommand` is the generic command for a TV power state: Image
  View On for On, and Standby directed to the TV alone for Standby.
  The node workload sends Image View On and then Active Source for its
  `Display`'s physical address when it claims the input for a person,
  in the wake and for a home press. It answers Request Active Source
  and Set Stream Path with Active Source alone, and it reads every
  Active Source and routing message the bus carries. It sends the TV
  Inactive Source when its `Display` goes dark while the route still
  leads there. `cectest` has a peer that claims Active Source
  after another device's claim, the way a streaming player did in the
  first drill.
- **Remote control.** User Control Pressed and Released. The kernel
  turns them into key events on the adapter's input device when the
  claim sets passthrough, and this package never reads them.

## What the kernel does and what this package does

The kernel answers polls to a claimed address, and Give Physical
Address, Give OSD Name, and Get CEC Version, from what the claim
states. It broadcasts Report Physical Address after each claim. It
answers Give Device Vendor ID itself, and passes the request to no
follower: with the vendor a claim states, or with a Feature Abort for
a claim that states none, as `Claim`'s does (`cec_receive_notify` in
[`cec-adap.c`](https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/drivers/media/cec/core/cec-adap.c)).

The kernel sends a Feature Abort for an unsupported directed message
only when no follower exists. The node workload is a follower, so
`Answer` owes a Feature Abort for each directed request it does not
support, and it answers Give Device Power Status, which the kernel
never answers, with the power its caller states. The node workload
states On only while a Player's session holds the room awake on its
`Display`, because to the TV a playback device with no picture is in
standby (HDMI-CEC 1.3a, CEC 13.14). `cec-compliance --test-core --test-power-status
--test-system-information --test-device-osd-transfer` passes against
`Answer` on vivid.

`Claim` sets no `CEC_LOG_ADDRS_FL_ALLOW_UNREG_FALLBACK`, so when a
device holds each of the playback addresses 4, 8, and 11, the kernel
claims nothing and the node workload tries again later. HDMI-CEC
1.3a, CEC 10.2.1, says a device should take the unregistered address
15 then. The adapter does not, because at address 15 it could not
answer a directed request, and its Active Source could not claim the
input for a person: a retry that later finds a playback address free
is worth more than a claim that can do neither.

A monitor handle must be in no-initiator mode, and the kernel allows
it only to a process with `CAP_NET_ADMIN`. Clearing the logical
addresses needs an initiator. So Listen takes the initiator mode,
clears the addresses, and then becomes a monitor.

A received message carries no acknowledge bit for a message between
two other devices, so a monitor that hears another device's poll does
not learn whether the poll found a device. The directory counts a
device present when it sends a message, and a scan counts it present
when it acknowledges a poll. Only a NACK makes a scan forget a device:
a poll that loses arbitration or meets a wire error says nothing about
the device, so the scan keeps what it found there before.

`Answer` never answers a message from the unregistered address 15,
because its answer would go to address 15, which is the broadcast
address, and a Feature Abort and a Report Power Status are directed
messages. The kernel answers an Abort message for its own adapter and
does not pass it to the follower.

## Testing against vivid

The kernel's
[`vivid`](https://docs.kernel.org/admin-guide/media/vivid.html#cec-consumer-electronics-control)
driver emulates a TV with HDMI inputs and sources on HDMI outputs,
each with a real kernel CEC adapter on one bus. The `Vivid` tests in
this package and in the operator skip when no vivid adapter is open
to the user, which is the case in CI. To run them on a workstation:

```sh
# Three HDMI inputs and three HDMI outputs. Each output's adapter is
# on the TV's bus.
sudo modprobe vivid num_inputs=3 input_types=0x3f num_outputs=3 output_types=0x07

# An output has a physical address only while an input shows it.
# v4l2-ctl is in v4l-utils; a container keeps it off the host.
docker run --rm --device /dev/video2 debian:trixie-slim sh -c \
  'apt-get update -qq && apt-get install -qq -y v4l-utils >/dev/null &&
   v4l2-ctl -d /dev/video2 -c hdmi_000_0_is_connected_to=2 \
     -c hdmi_000_1_is_connected_to=3 -c hdmi_000_2_is_connected_to=4'

# The nodes are root:video 0660.
sudo chmod 666 /dev/cec*

go test -run Vivid -v ./cec/ .
sudo modprobe -r vivid
```

With no follower on vivid's TV adapter, the kernel's CEC core answers
Give Device Power Status with a Feature Abort, before and after an
Image View On, so something must play the TV. Each `Vivid` test plays the TV itself on the capture adapter. The wake's test also plays a
streaming player on a second output, which claims Active Source once
after the node workload's claim, and it skips when only one output is
connected. The
test that wakes the TV can leave the TV to `cec-follower` from
v4l-utils instead, which is an independent implementation of a TV's
power states: after Image View On it reports Standby for about 2
seconds and ToOn for about 6 more. Start it, then run that test alone,
because the other `Vivid` tests claim the same adapter:

```sh
# /dev/cec0 is the adapter named vivid-000-vid-cap0.
docker run -d --name vivid-tv --device /dev/cec0 debian:trixie-slim sh -c \
  'apt-get update -qq && apt-get install -qq -y v4l-utils >/dev/null &&
   cec-ctl -d0 --tv -o TV >/dev/null && exec cec-follower -d0 -s -n'

CEC_VIVID_TV=cec-follower go test -run TestVividTheNodeWorkloadWakesTheTV -v .
docker rm -f vivid-tv
```

`/dev/video2` is the capture node named `vivid-000-vid-cap` under
`/sys/devices/platform/vivid.0/video4linux`; its number depends on
the other video devices of the machine. The monitor test needs
`CAP_NET_ADMIN`, so an ordinary run skips it. Build the test binary
with `go test -c ./cec/` and run it with `sudo` to include it.

A vivid output adapter takes its physical address from its port and
refuses `CEC_ADAP_S_PHYS_ADDR` with `ENOTTY`, the way any adapter on
a video port does. `cectest` copies that for an adapter without
`CapPhysAddr`, and the node workload then announces the port's own
address and says so in its entry.

## Vendor notes

No vendor-specific behavior is built yet. `plans/completed/09-cec.md` states
the rule for adding one: take the behavior from libCEC's handler for
that brand in
[`src/libcec/implementations`](https://github.com/Pulse-Eight/libcec/tree/master/src/libcec/implementations),
link the lines it follows, note it here, and copy no code, because
libCEC's license differs from this repository's.
