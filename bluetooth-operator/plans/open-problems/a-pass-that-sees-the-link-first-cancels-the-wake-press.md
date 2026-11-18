# A pass that sees the link first cancels the wake press

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When a T6 remote wakes, the relay writes a `KEY_UNKNOWN` press only if
the relay opens the remote's input node before a pass writes `LinkUp`
to the `Peripheral`. When the pass comes first, the remote reconnects
with no press, and the screen stays asleep.

## Where it happens

- `relays.follow` in `wake.go` clears `asleepSince` for every reason
  other than `Asleep`, `LinkUp` included. `writePeripheralStatus`
  calls it on each status write.
- `relays.pressToWake` writes the press only while `asleepSince` is
  set, when the relay opens a node.
- The loop in `main.go` opens a new node at once on its uevent
  (`publisher.attach`), and runs a pass when `settleWindow`, 1.5
  seconds, passes with no event, or at `settleLimit`, 10 seconds, after
  the first event of a burst. The `Connected` property change from
  BlueZ is one of those events.

So the order depends on the gap between BlueZ's `Connected` signal and
the uevent of the remote's HID device. When that gap is longer than
`settleWindow`, the pass runs first, finds the device connected, and
cancels the press.

## Evidence

- The comment at the top of `wake.go` states the outcome: "A pass that
  writes LinkUp before the relay opens the remote's node clears
  asleepSince, and no press is written."
- Plan 12 states the opposite as its design: "a pass that wrote
  `LinkUp` a moment earlier cannot stop the press". Its list of what
  the build added says that every reason other than `Asleep` disarms
  the relay, and it gives no reason why a `LinkUp` before the open is
  safe.
- `wake_test.go` and `wakepass_test.go` schedule the press and then
  open the node. No test runs a pass that writes `LinkUp` between the
  sleep and the open.
- Plan 12's drill has not run, so no measurement shows how long the
  gap is on a real remote.

## What would confirm it

- A test in a `synctest` bubble: a pass writes `Asleep`, the clock
  passes `wakeAfter`, a pass finds the remote connected, and then the
  relay opens the node. The test checks whether the virtual device
  receives the press.
- On the physical test cluster, a `btmon` trace and the operator's log
  for a T6 wake, which give the time from `Connected` to the HID
  device's uevent.

## What a fix has to decide

- Whether `LinkUp` cancels a scheduled press, or only a new reason
  that ends a sleep does.
- How long a press stays scheduled after the link comes back, so that
  a remote that reconnects with no HID device does not get a press
  much later.
- Whether plan 12's design or the comment in `wake.go` describes the
  behavior that the project wants.
