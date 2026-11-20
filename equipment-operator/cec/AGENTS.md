# Working on cec

This package is the HDMI-CEC driver. It is a Go binding of the Linux
kernel's CEC API, built on `golang.org/x/sys/unix`, with no libCEC and
no cgo. On top of the binding are the messages, the scan, and the
follower. `cectest/` is an in-memory CEC bus for tests, which handles
the same ioctls as the kernel. The design is in
`plans/completed/09-cec.md`.

## The references

- The kernel's
  [CEC API](https://docs.kernel.org/userspace-api/media/cec/cec-api.html)
  documents each ioctl, each struct field, and each error. The
  [CEC introduction](https://docs.kernel.org/userspace-api/media/cec/cec-intro.html)
  explains the addresses, and the
  [CEC admin guide](https://docs.kernel.org/admin-guide/media/cec.html)
  covers the drivers.
- Every struct layout and request number in `kernel.go` comes from
  `linux/cec.h`. `kernel_test.go` checks each size, offset, and
  request number against values compiled from the header with
  `sizeof`, `offsetof`, and the `_IOR`/`_IOW`/`_IOWR` macros.
  `linux/cec-funcs.h` shows the operand layout of each message.
- CEC is Supplement 1 of the HDMI specification. HDMI 1.3a with its
  CEC supplement is free from the
  [HDMI specification page](https://www.hdmi.org/spec/index) after you
  fill in a form. Later supplements are available only to HDMI
  adopters.
- The kernel's `pulse8-cec` driver makes the Pulse-Eight USB-CEC
  adapter
  ([product page](https://www.pulse-eight.com/p/104/usb-hdmi-cec-adapter))
  work through the kernel's CEC framework, so this package does not
  need libCEC for it. The notes at the top of
  [`pulse8-cec.c`](https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/drivers/media/cec/usb/pulse8/pulse8-cec.c?h=v7.2)
  describe the adapter's autonomous mode.
- Use [`cec-ctl`](https://www.mankier.com/1/cec-ctl),
  [`cec-follower`](https://www.mankier.com/1/cec-follower), and
  [`cec-compliance`](https://www.mankier.com/1/cec-compliance) from
  [v4l-utils](https://git.linuxtv.org/v4l-utils.git) to check this
  code by hand.

## The messages

`message.go` groups the opcodes by family. The comment on each group
names the device that sends those messages and what replies to them.
The doc comments of `Directory`, `ReadPower`, `PowerCommand`,
`Answer`, and `PressVolumeKey`, and of the `cectest` `Peer` fields,
describe what the package does with each family.

Nothing in this package sends a request on a timer. Every request is
traffic on the bus, and some TVs switch their own input when they
receive traffic they did not expect. So between reads, the directory
updates the TV's power from the messages that other devices send.

## What the kernel does and what this package does

The kernel acknowledges polls to an address that the adapter has
claimed. It replies to Give Physical Address, Give OSD Name, and Get
CEC Version with the values set in the claim. It broadcasts Report
Physical Address after each claim. It also replies to Give Device
Vendor ID itself and does not pass that request to a follower. It
replies with the vendor set in the claim. When the claim sets no
vendor, as `Claim` does, it replies with a Feature Abort. See
`cec_receive_notify` in
[`cec-adap.c`](https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/drivers/media/cec/core/cec-adap.c).

The kernel sends a Feature Abort for an unsupported directed message
only when there is no follower. The node workload is a follower, so
`Answer` sends a Feature Abort for each directed request that it does
not support. The kernel never replies to Give Device Power Status, so
`Answer` replies to it with the power that its caller passes in. The
node workload passes On only while a `Player`'s session is awake on
its `Display`. To the TV, a playback device with no picture is in
standby (HDMI-CEC 1.3a, CEC 13.14). `cec-compliance --test-core
--test-power-status --test-system-information
--test-device-osd-transfer` passes against `Answer` on vivid.

`Claim` does not set `CEC_LOG_ADDRS_FL_ALLOW_UNREG_FALLBACK`. So when
other devices already use all of the playback addresses 4, 8, and 11,
the kernel claims no address, and the node workload tries again
later. HDMI-CEC 1.3a, CEC 10.2.1, says a device should then use the
unregistered address 15. The adapter does not, because at address 15
it could not reply to a directed request, and its Active Source could
not claim the input for a person. A later retry can find a playback
address free, and then the adapter can do both.

## Tests against the kernel

The `Vivid` tests, here and in the operator, check the code against
the kernel's own CEC core through its `vivid` driver. They skip in
CI. Read [`VIVID.md`](VIVID.md) before you run one or change one.

## Vendor notes

The package sends the same messages to every TV brand. When a brand
needs a behavior of its own, take it from libCEC's handler for that
brand in
[`src/libcec/implementations`](https://github.com/Pulse-Eight/libcec/tree/master/src/libcec/implementations).
Link to the lines that the code follows and add a note here. Write
the code here, and do not copy it, because libCEC's license differs
from this repository's (`plans/completed/09-cec.md`).
