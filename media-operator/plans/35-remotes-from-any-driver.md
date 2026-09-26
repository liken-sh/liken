# 35, Remotes from any driver

Designed, not built. It follows `liken` plan 70, which publishes a USB
CEC adapter's remote input as its own device, and equipment-operator
plan 09, which joins the CEC bus that sends the keys.

## The problem

A TV's remote sends its buttons over HDMI-CEC to the source that is
showing. When a `liken` machine has a CEC adapter on the bus, the
kernel turns those buttons into key events on an input device, and
`liken` plan 70 publishes that input device as a DRA device. A person
who wants the TV remote to drive a `Player` declares a `Remote` that
selects that device, the same way a Bluetooth remote is declared.

The reader pod reads such a device with no new code: it globs
`/dev/input/event*` and reads every node that declares key codes. It
cannot survive an unplug of the adapter.

A Bluetooth remote never takes its node away. bluetooth-operator
delivers a uinput relay node that stays in place while the remote
sleeps or disconnects, so the reader's open node stays valid. A USB
CEC adapter is different. When it is unplugged, the kernel removes
its event node, and when it comes back, the kernel makes a new one. A
running container never receives a node that appears after it
starts. `readNode` returns when its node ends, and the outer loop
waits for new nodes in the container's own `/dev`, where the new node
never appears. So after one unplug, the reader runs and reads nothing
until a person deletes the pod.

## The design

**The reader exits when its last node is gone.** When every node the
reader opened has ended and the glob finds no node that declares key
codes, the reader exits and states in its last log line which node
ended and with which error. The kubelet restarts the container, and
the new container receives the nodes the claim's CDI spec names at
that moment. This is the claimant contract of `liken` plan 70: the
machine operator rewrites the CDI spec on every pass, so an adapter
that comes back on the same USB port brings the new node to the next
container, with no new allocation.

A Bluetooth reader never meets the rule, because its relay node never
ends. So the rule changes nothing for the remotes that exist now.

The reader's claim keeps its toleration of
`bluetooth.liken.sh/disconnected`. `liken` publishes no taint for a
CEC input device, so no eviction needs a second toleration. If `liken`
adds a device taint later, a toleration derived from the
`DeviceClass`'s driver is the change to make then.

The kernel names each CEC button with the
[`rc-cec` keymap](https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/drivers/media/rc/keymaps/rc-cec.c?h=v7.2),
and a `Keymap` uses the same kernel names.

**The keys that bind now.** These `rc-cec` keys are in
`keybindings.go` and need no `Keymap` row:

* `KEY_OK`, `KEY_UP`, `KEY_DOWN`, `KEY_LEFT`, `KEY_RIGHT`, `KEY_ENTER`
* `KEY_EXIT`, which is back
* `KEY_PLAYCD` and `KEY_PLAYPAUSE`, which pause and resume
* `KEY_REWIND` and `KEY_FASTFORWARD`
* `KEY_VOLUMEUP`, `KEY_VOLUMEDOWN`, and `KEY_MUTE`
* `KEY_INFO`
* `KEY_WWW`, which is home. `rc-cec` sends it for the CEC 2.0
  Internet button.

**The keys that need a `Keymap` row.** `rc-cec` sends these, and no
binding reads them:

* `KEY_PAUSECD` and `KEY_STOPCD`, the CEC Pause and Stop buttons
* `KEY_FORWARD`, the CEC Forward button, which skips ahead
* `KEY_FASTREVERSE`, `KEY_SLOW`, and `KEY_SLOWREVERSE`, the transport
  modes of the CEC Play operand
* `KEY_ROOT_MENU`, `KEY_SETUP`, `KEY_MENU`, `KEY_MEDIA_TOP_MENU`, and
  `KEY_CONTEXT_MENU`
* `KEY_SOUND`, the CEC Sound Select button
* the numbers, the four color keys, and the channel keys

`KEY_BACK` needs care. `rc-cec` sends it for the CEC Backward button,
which skips back in a recording. `keybindings.go` binds `KEY_BACK` to
the back action. A TV that sends Backward from its back arrow would
work as intended, and a TV that sends it from a skip button would not.
A `Keymap` row corrects the second case.

The `Keymap` for a TV remote is a person's to write, because TVs send
different subsets of these keys. Discovery mode (plan 21) shows the
codes a TV sends.

## What was set aside

* **Presses forwarded over the bus.** equipment-operator could read
  the keys from the CEC device and publish them to a `Player`'s topic.
  A TV remote would then have no `Remote` object, no `Keymap`, and no
  discovery mode, and a press would pass through a second component
  and the broker. The input device carries the same keys to the
  reader that reads a Bluetooth remote.
* **A toleration for each driver's disconnected taint.** `liken`
  publishes no taint for a CEC input device, so the toleration would
  guard against nothing.
* **Reopening the node inside the container.** A running container
  never receives a node that appears after it starts, so there is
  nothing to reopen. The restart is how the reader receives the new
  node.

## How it will be proved

On a machine with a Pulse-Eight adapter on a bus in `Control` mode, a
`Remote` that selects the CEC input device drives the browser with the
TV's remote. After an unplug, the reader's container exits with a log
line that names the ended node. After the adapter is plugged back into
the same port, the restarted container reads the new node, and the
remote works again with no change to the pod.
