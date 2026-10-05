# 35, Remotes from any driver

Designed, not built. A remote reader pod survives the loss of its
input node only when that node comes back in place. This plan makes
the reader exit when its last node is gone, so that the kubelet
restarts it with the nodes the claim delivers at that moment. It
follows `liken` plan 70, which publishes a USB CEC adapter's remote
input as its own device. What the CEC keys and the CEC deck messages
mean is the subject of root
[plan 73](../../plans/73-cec-menu-deck-and-language.md).

## The problem

The reader pod reads a CEC adapter's input device with no new code:
it globs `/dev/input/event*` and reads every node that declares key
codes. It cannot survive an unplug of the adapter.

A Bluetooth remote never takes its node away. bluetooth-operator
delivers a uinput relay node that stays in place while the remote
sleeps or disconnects, so the reader's open node stays valid. A USB
CEC adapter is different. When it is unplugged, the kernel removes
its event node, and when it comes back, the kernel makes a new one. A
running container never receives a node that appears after it
starts. `readNode` returns when its node ends, and `awaitNodes` scans
the container's own `/dev/input` every 2 seconds (`nodePollDelay` in
`media-operator/remote.go`), where the new node never appears. So
after one unplug, the reader runs and reads nothing until a person
deletes the pod.

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

The reader's claim keeps its toleration of
`bluetooth.liken.sh/disconnected`. `liken` publishes no taint for a
CEC input device, so no eviction needs a second toleration. If `liken`
adds a device taint later, a toleration derived from the
`DeviceClass`'s driver is the change to make then.

## What the design must answer

The design assumes that a Bluetooth reader never meets the rule,
because its relay node never ends. The comment on `nodePollDelay`
states a case that contradicts this: a relay node answers `ENODEV`
while bluetooth-operator restores its relay, and answers again when
the relay's virtual device is back. During that window the glob finds
no node that answers, so the rule as written exits a Bluetooth reader
too. The design must tell a node that the kernel removed from a node
that answers `ENODEV` for a while, or accept a restart of the
Bluetooth reader in that window.

## What was set aside

* **Presses forwarded over the bus.** equipment-operator could read
  the keys from the CEC device and publish them to a `Player`'s topic.
  A TV remote would then have no `Remote` object, no `Keymap`, and no
  discovery mode, and a press would pass through a second component
  and the broker. The input device carries the same keys to the
  reader that reads a Bluetooth remote. Since root plan 77,
  equipment-operator also holds no broker connection.
* **A toleration for each driver's disconnected taint.** `liken`
  publishes no taint for a CEC input device, so the toleration would
  guard against nothing.
* **Reopening the node inside the container.** A running container
  never receives a node that appears after it starts, so there is
  nothing to reopen. The restart is how the reader receives the new
  node.

## How it will be proved

A reader test points `inputNodePattern` at a directory of its own,
ends the reader's only node, and removes it. The reader returns with
an error that names the ended node, and does not wait on
`nodePollDelay`. A second test keeps a node that answers again, and
the reader reads it with no exit.
