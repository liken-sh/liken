# Working on cec

This package is the HDMI-CEC driver. It contains:

- a Go binding of the Linux kernel's CEC API, built on
  `golang.org/x/sys/unix`, with no libCEC and no cgo
- the messages that the operator sends and reads
- the scan and the follower, which use those messages

`cectest/` is an in-memory CEC bus for tests, which handles the same
ioctls as the kernel. The design is in `plans/completed/09-cec.md`.

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

## The message families

`message.go` groups the opcodes by family. The comment on each group
says which device sends those messages and what replies to them. The
package uses these families:

- **Identity.** Give Physical Address, Give OSD Name, Give Device
  Vendor ID, and Get CEC Version, with the reports that reply to
  them. A scan sends all four requests to each device. When a device
  announces itself after the scan, `Introduce` sends that one device
  only the requests whose results are not yet in the directory.
- **Power.** Give Device Power Status and Report Power Status.
  `ReadPower` requests the power of one device and records the reply
  in the directory. When a device acknowledges two requests in a row
  and replies to neither, the directory clears the power it recorded
  for that device. After one missed reply the directory keeps the
  recorded power, because a TV that is waking up can miss one.
  Between reads, the directory updates the TV's power from the
  messages that change it:
  - a Standby sent to the TV or to every device
  - Image View On and Text View On
  - a Routing Change, Set Stream Path, or Request Active Source from
    the TV
  - an Active Source from any device

  Nothing in this package sends a request on a timer. Every request
  is traffic on the bus, and some TVs switch their own input when
  they receive traffic they did not expect.
- **One-touch play.** Image View On, Standby, and Active Source.
  `PowerCommand` builds the command for a TV power state: Image View
  On for On, and a Standby sent to the TV alone for Standby. When the
  node workload claims the TV's input for a person, it sends Image
  View On and then Active Source with its `Display`'s physical
  address. It does this when it wakes the TV and when someone presses
  home. It replies to Request Active Source and to Set Stream Path
  with Active Source alone. It reads every Active Source and routing
  message on the bus. When its `Display` goes dark while the route
  still leads to it, it sends Inactive Source to the TV. A `cectest`
  peer with `Grabs` set claims Active Source after another device's
  claim, as some streaming players do when the TV turns on.
- **Remote control.** User Control Pressed and User Control Released.
  When the claim sets passthrough, the kernel turns these messages
  into key events on the adapter's input device. This package never
  reads them.
- **System Audio Control.** System Audio Mode Request, Set System
  Audio Mode, Give System Audio Mode Status, and System Audio Mode
  Status, in `audio.go`.
  - A scan requests the mode from the audio system at logical
    address 5, after its other requests to that device. The directory
    updates the mode from each broadcast Set System Audio Mode and
    from each System Audio Mode Status.
  - When the node workload wakes the TV and its `Display` passes
    through a receiver, it sends System Audio Mode Request with the
    `Display`'s physical address.
  - HDMI-CEC 1.3a, CEC Table 25, says that a device that sends the
    request shall be able to send the volume keys. So
    `PressVolumeKey` sends the audio system User Control Pressed for
    Volume Up, Volume Down, or Mute, and then User Control Released.
    Nothing in the operator calls it, because the receiver's network
    protocol block sets the volume.
  - `Answer` sends a Feature Abort for a directed Set System Audio
    Mode or System Audio Mode Status only on a bus that has no audio
    system. On such a bus the adapter never sends the request.
  - A `cectest` peer with `SystemAudio` set is an audio system. Its
    `AudioReply` sets what it does with a request. It can broadcast
    Set System Audio Mode [On], or send it to the TV alone. It can
    send it to the TV and leave the mode off, as when the TV rejects
    the mode. It can send no reply, or reply to the request with a
    Feature Abort.

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

A monitor handle must be in no-initiator mode, and the kernel allows
that mode only to a process with `CAP_NET_ADMIN`. To clear the
logical addresses, the handle must be an initiator. So Listen mode
first sets the handle to initiator mode, clears the addresses, and
then sets it to monitor mode.

When a monitor receives a message between two other devices, the
message has no acknowledge bit. So a monitor that hears another
device's poll cannot tell whether the poll found a device. The
directory counts a device as present when the device sends a message.
A scan counts a device as present when it acknowledges a poll. A scan
removes a device only on a NACK. A poll that loses arbitration or
hits a wire error says nothing about the device, so the scan keeps
what it found at that address before.

`Answer` never replies to a message from the unregistered address
15. Its reply would go to address 15, which is also the broadcast
address, and a Feature Abort and a Report Power Status are directed
messages. The kernel itself replies to an Abort message for its own
adapter and does not pass it to the follower.

A vivid output adapter gets its physical address from its port. It
rejects `CEC_ADAP_S_PHYS_ADDR` with `ENOTTY`, the same as any adapter
on a video port. `cectest` does the same for an adapter without
`CapPhysAddr`. The node workload then announces the port's own
address and says so in its entry.

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
