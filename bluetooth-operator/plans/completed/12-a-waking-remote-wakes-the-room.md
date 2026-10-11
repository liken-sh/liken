# A remote that wakes from sleep wakes the room

Plan 12. Built 2026-10-11. The drill in [The drill](#the-drill) has not
run yet.

A Bluetooth LE remote that sleeps sends no key for the press that
wakes it. So today a person presses once to wake the remote, and a
second time to wake the room. This plan records why each link ended,
shows it in `kubectl get peripherals`, and makes the input relay write
one key press into the remote's virtual device when the remote
connects after a sleep. That press wakes the screen, the TV, and the
receiver, the way any press does.

The press cannot stand in for a particular button. When the remote
fell asleep during a film, the first press, such as a pause, still
does nothing, and the second press pauses.

## The problem

A T6 remote ends its link after 60 idle minutes. Across 15 sleeps of
three remotes on a home cluster, the time from the last relayed press
to `hid remove` was 59.90 to 59.96 minutes (measured). The remote does
not close the link. It stops answering, and the radio reports
`Connection Timeout (0x08)` (`btmon`, measured on three sleeps).

A press wakes the remote, and the remote connects again. On
2026-10-11 on a home cluster, with the relay change of commit
`4c794ff6`, `btmon` and the operator's log measured this wake on Down:

| Time (s) | Event |
|---|---|
| 47.018 | LE link up |
| 47.035 | `hid add` |
| 47.055, 47.068 | The relay opens both evdev nodes |
| 47.093 | Encryption on, so the remote can send reports |
| 47.096 | A notification on handle `0x005c`, and battery `00` |
| 52.741 | The first key report, from the second Down |

The relay was reading before the remote could send a report, and no
report came for the first press. The bytes on `0x005c` were
`0b 00 04 00 02 00 86 00 86` on all seven connects in the record: on
wakes by Home and by Down, and on reconnects that nobody pressed for.
So they do not identify the button.

## The disconnect reasons

The reason for a disconnect separates a sleep from a reconnect that
the cluster caused:

- A sleep ends in a supervision timeout, `0x08`. The kernel reports it
  as `MGMT_DEV_DISCONN_TIMEOUT` (`hci_to_mgmt_reason`, Linux
  `net/bluetooth/mgmt.c`).
- When the radio powers off, at a `SIGTERM` to `bluetoothd` or at the
  power cycle of the input recovery, the kernel reports `LOCAL_HOST`
  or reports nothing. `bluetoothd` then reports `Unknown` for each
  link when the adapter stops (BlueZ 5.87 `src/adapter.c`,
  `adapter_stop`). A hardware error or an HCI reset takes the same
  path. None of these is a timeout.
- A sleeping remote does not connect when the radio restarts. At the
  pod roll of 2026-10-10, a sleeping remote stayed asleep (measured).
  An awake remote connects again 0.3 to 0.5 seconds after the radio
  powers on (measured).

BlueZ reports the reason on D-Bus. From 5.83, `org.bluez.Device1`
emits `Disconnected(string reason, string message)` once for each link
that ends. `reason` is one of `org.bluez.Reason.Timeout`, `Local`,
`Remote`, `Authentication`, `Suspend`, or `Unknown`
(`doc/org.bluez.Device.rst`, `src/device.c`). BlueZ emits it before
the change of `Connected` to `false`. The pod runs BlueZ 5.87.

One path reports no reason. When `bluetoothd` is killed with
`SIGKILL`, the link can outlive it, and the new `bluetoothd` reports
the link as connected with no `Disconnected` before it
(`load_connections` in `src/adapter.c`).

## The design

### The operator reads the reason

`watchBlueZ` adds `Disconnected` on `org.bluez.Device1` to the signals
it matches, and the operator reads the reason from that signal. The
other signals still only wake the loop.

The operator holds the newest reason for each device until the next
pass writes it to the `Peripheral`. The pass runs after the settle
window, usually 1.5 seconds and at most about 15 seconds later. BlueZ
holds no property with the last reason, so this memory carries the
signal to the API server and holds nothing the API server can supply.
A pass that finds the device connected discards the reason it holds.
A reason lost to a restart of the operator becomes `NotConnected`, and
`NotConnected` never wakes the room.

### The Connected condition names the reason

The `Connected` condition's reason states why the device is not
connected, from the reason BlueZ gave:

| Reason | Status | When |
|---|---|---|
| `LinkUp` | `True` | The link is up |
| `Asleep` | `False` | The link timed out, and the device sleeps between sessions |
| `LinkLost` | `False` | The link timed out, and the device does not sleep between sessions: it went out of range, or its battery ran out |
| `ClosedByDevice` | `False` | The device ended the link: it was switched off, or it disconnected |
| `ClosedByRadio` | `False` | This radio ended the link: an unpair, or the input recovery |
| `AuthenticationFailed` | `False` | The link ended because the keys did not match |
| `NotConnected` | `False` | The operator received no reason: the link ended while the operator was not running, or BlueZ reported `Unknown` or `Suspend` |
| `NotBonded` | `False` | `bluetoothd` holds no object for the device |

`sleepsBetweenSessions` still decides between `Asleep` and `LinkLost`,
from the kind of device. Today it decides between `Asleep` and
`NotConnected` for every disconnect. After this plan, a device that
sleeps between sessions shows `Asleep` only when its link timed out.

The reason changes only when the link changes. A pass that finds the
device disconnected and holds a reason from a signal writes that
reason. A pass that finds the device still disconnected and holds no
reason keeps the reason the `Peripheral` holds. So a restart of the
operator keeps `Asleep` on a sleeping remote. A remote that went to
sleep while the operator was not running shows `NotConnected`. A pod
roll shows `NotConnected` too, because the operator container stops
before `bluetoothd` reports the disconnect.

Each transition of the condition posts an `Event` with its reason, a
`Warning` for `AuthenticationFailed` and a `Normal` for the others.
`events.go` exempts the condition today because a remote's link
changes "many times an hour", and the measurements above show one
sleep after 60 idle minutes. The `Event` is the record that a person
reads in `kubectl describe` when a controller stopped working.

`bluetooth_disconnects_total` takes a `reason` label with the
condition's reason.

### Two printer columns

`kubectl get peripherals` shows the condition's reason and the time of
the condition's last change of status, after `Connected`:

```
NAME                ALIAS         DEVICE      ADDRESS             BATTERY   PAIRED   CONNECTED   REASON   SINCE   NODE     AGE
c2-11-22-33-44-55   sofa-remote   T6-Remote   C2:11:22:33:44:55   75        true     False       Asleep   42m     node-2   24d
```

`SINCE` is a `date` column on the condition's `lastTransitionTime`,
which changes only when the status changes. So for a disconnected
device it shows how long ago the link ended.

### The relay writes a press for a remote that wakes

A pass that writes `Asleep` arms the controller's relay with the
condition's `lastTransitionTime`. The first pass after a start of the
operator arms each controller whose `Peripheral` already holds
`Asleep`. A pass that finds the controller connected disarms it.

When the relay opens a node of an armed controller, and the link has
been down for at least 10 seconds, the relay writes one press and one
release of `KEY_UNKNOWN` into the virtual device, each followed by
`SYN_REPORT`. Then it disarms the controller and starts reading the
real node. The relay decides from the arming alone and reads no
`Peripheral` and no API object, so a pass that wrote `LinkUp` a moment
earlier cannot stop the press, and the lock the relay holds never waits
on the API server.

- **One press for each sleep.** The press disarms the controller, and
  only a pass that writes a new `Asleep`, with a new
  `lastTransitionTime`, arms it again. A remote opens two nodes, and an
  HID device can be removed and added again on the same link, and
  neither gives a second press.
- **10 seconds down.** A remote that lost its link while it was awake
  can connect again on its own. A timeout from interference, or from a
  stream of audio on the same radio, gives `Asleep` too. That remote
  advertises again at once, and connects within seconds. A person
  rarely presses a button within 10 seconds of the moment the remote
  went to sleep, 60 minutes after the last press. So a connect within
  10 seconds of the timeout writes no press.
- **The node.** The relay writes the press into the first node it opens
  whose stored capabilities hold a key code below `BTN_MISC` (`0x100`),
  the codes of a keyboard. A gamepad's node, with `BTN_*` codes only,
  gets none.
- **Only what a claim receives.** A node delivers only the event types
  that its prepared claims demand, and the relay sets that mask on the
  real node. A press written to the virtual device does not pass
  through the mask. So the relay writes the press only when the node's
  demand is not empty and its mask passes `EV_KEY`, from the same
  `inputMasks` that sets the real node's mask.
- **The device declares the code.** The kernel drops a key that the
  virtual device did not declare, with no error. A uinput device
  cannot declare a code after it is created. So `build` declares
  `KEY_UNKNOWN` on each virtual device whose node holds a code below
  `BTN_MISC`. The code is not added to the stored capabilities, so the
  snapshot in the bond's `Secret`, the input classes, and the
  `ResourceSlice` do not change. A virtual device created before this
  change declares the code after the next start of the pod.
  `media-operator` reads the codes from the virtual node, so a
  `Remote`'s codes include `KEY_UNKNOWN` (240).
- **A log line and a counter.** The relay writes
  `relay: controller C2:11:22:33:44:55 woke from sleep, so the relay wrote KEY_UNKNOWN to /dev/input/event10`,
  and counts the press in `bluetooth_wake_presses_total`. A key press
  posts no `Event`, and the condition's change to `LinkUp` posts one.

### `KEY_UNKNOWN` as the key

The relay cannot tell which button was pressed, and `KEY_UNKNOWN` is
the kernel's code for a key with no name. Each consumer of a `Remote`
handles a key with no binding the same way:

- On a sleeping screen, `media-screen` wakes the screen, states the
  `on` desire, and does nothing else (`media-screen/src/screen.rs`).
  This applies in a room with a `Receiver` and in one without. In a
  room with a `Receiver`, the desire wakes the TV and the receiver.
- `media-operator` sends the input ask for any press except a power
  key (`ensure.go`), so the receiver selects the unit's input.
- The idle screen, the media browser, and the playback pod bind
  nothing to it.

The second Down on 2026-10-11 caused the same effects.

`KEY_WAKEUP` is the obvious key, and it does the wrong thing here. In
`media-operator` and `media-screen` it is the TV remote's CEC Power On
Function. It asks the room for `on`, it sends no input ask, and it
leaves the screen's shade down, because the TV's own remote turned the
TV on (`media-operator/ensure.go`, root plan 72).

During a `Play`, `media-screen` passes no key to the client, and the
playback pod binds nothing to `KEY_UNKNOWN`, so the press does nothing.

Discovery logs the press as `KEY_UNKNOWN`, code 240, on each wake. A
remote can also send `KEY_UNKNOWN` for a real button, because the
kernel's HID driver gives that code to a usage it has no key for. A
`Keymap` row on `KEY_UNKNOWN` then acts on that button and on each
wake, so the guide on mapping a controller warns against it.

## The cases

| Case | Recorded reason | The next connect |
|---|---|---|
| The remote sleeps after 60 idle minutes | `Asleep` | A press: the relay writes a press |
| A pod roll while the remote is awake | `NotConnected` | Within a second, with no press: nothing written |
| The input recovery disconnects the remote | `ClosedByRadio` | Nothing written. A remote that sleeps through the recovery's connect loses its next press, as today |
| `bluetoothd` is killed and the link survives | No change | Nothing written |
| A restart of the operator while the remote sleeps | `Asleep`, kept | A press: the relay writes a press |
| The remote sleeps while the operator is not running | `NotConnected` | A press: nothing written, and the first press does nothing, as today |
| Interference ends the link of an awake remote | `Asleep` | Within seconds, with no press: nothing written |
| The remote leaves range and comes back after 10 seconds | `Asleep` | Possibly no press: the relay writes a press, and the room wakes |
| A controller switched off with its own button | `ClosedByDevice` | Nothing written |
| The first connect of a disconnected LE device after the upgrade | `Asleep`, from the old rule | The relay writes a press, once |

A remote that comes back into range wakes the room although nobody
pressed a button, and the guide says so. After the upgrade, every
disconnected LE device holds `Asleep` from the old rule, so its first
connect writes one press. For a remote that connect is almost always a
press. A gamepad gets no press, because its nodes hold no keyboard
codes.

## Alternatives set aside

- **Read the button from `0x005c`.** The bytes are the same on every
  connect.
- **Let `media-operator` wake the `Player` when the `Peripheral`
  connects.** On 2026-10-11, `media-operator` saw the connect 2
  seconds after the link came up, and the relay's path reached the
  idle screen 23 ms after a report. It also adds a second way to wake
  a room.
- **The relay reads the `Peripheral` when it opens a node.** A pass
  that writes `LinkUp` can run before the open, because the pass and
  the open run on one goroutine, and the relay then reads `LinkUp` and
  writes nothing. The read can also reach the API server while the
  relay holds its lock. Arming the relay from the pass avoids both.
- **Add `KEY_UNKNOWN` to the stored capabilities.** That changes the
  snapshot in every bond's `Secret`. Declaring the code in `build`
  changes nothing that is stored.
- **A status field for the reason, such as
  `status.link.disconnectReason`.** The condition's reason already
  says why the device is not connected, and a second field could
  disagree with it.

## Tests

Each test runs on the Go toolchain alone, through the existing fakes:

- The signal reader reads the device and the reason from a
  `Disconnected` signal built in the test.
- `Peripheral` status: each BlueZ reason gives its condition reason.
  The reason is kept while the device stays disconnected. A disconnect
  with no signal gives `NotConnected`. A reason held when the pass
  finds the device connected is discarded. Each transition posts its
  `Event`.
- The relay, through `fakeKernel`: an armed controller whose link was
  down for more than 10 seconds gets one press and one release on its
  first keyboard node, and none on its second node or on a node opened
  again. A controller that is not armed, or that connects within 10
  seconds, gets none. A node whose demand masks out `EV_KEY` gets none.
  The virtual device of a keyboard node declares `KEY_UNKNOWN`, and a
  gamepad's does not.
- `media-screen`: `KEY_UNKNOWN` on a sleeping screen wakes the screen
  and states `on`, in a room with a `Receiver` and in one without.

## The manual

- `deploy/crds.yaml`: the reasons in the `Connected` description and
  the two printer columns. The reference page `peripherals.md` is
  generated from it.
- `events.go` and `guides/install.md`: the `Event` of each transition,
  in the list of `Event`s.
- `reference/metrics.md`: the `reason` label and
  `bluetooth_wake_presses_total`.
- `reference/devices.md`: what a claim delivers when a remote wakes.
- `guides/pair-a-controller.md`: the reasons, the first press during a
  film, and the remote that wakes the room when it comes back into
  range.
- `media-operator`'s `guides/mapping-a-controller.md`: the
  `KEY_UNKNOWN` press that discovery logs after a wake.
- Every sample of `kubectl get peripherals` output.

## The drill

On a home cluster, after a remote has slept: `kubectl get peripherals`
shows `Asleep`, one press of Down wakes the room, and the operator logs
the press it wrote. Then a roll of the operator's pods with the remote
awake wakes nothing.

## What the build added

The build follows the design, with these details that the design did
not state:

- Every reason other than `Asleep` disarms the relay, not only
  `LinkUp`. A node that does not qualify for the press, and a connect
  within 10 seconds, leave the controller armed until the next pass.
- Each reason has a message, and the `Event` of the transition carries
  it. The message of `AuthenticationFailed` says to delete the
  `Peripheral` and pair the device again.
- A new or adopted `Peripheral` posts its first condition as an
  `Event`. A bond that leaves `bluetoothd` posts `NotBonded` as a
  `Normal` beside the `BondLost` `Warning`.
- The 10 seconds count from the `Asleep` condition's
  `lastTransitionTime`, which is the time of the pass that wrote it, cut
  to the second. So the link was down for at least 10 seconds.
- `main.go` and `inventory.go` passed 500 lines, so `wakes.go` and
  `delivered.go` took the settle window and the reads of the CDI files,
  with no change to that code.
