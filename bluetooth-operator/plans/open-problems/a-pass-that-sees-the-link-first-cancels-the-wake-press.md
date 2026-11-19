# A pass that sees the link first cancels the wake press

Open bug, recorded on 2026-11-18. A unit test reproduced the cancelled
press on 2026-11-19. How often a real remote's link comes up before its
HID device is not measured.

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
- Plan 12 states both designs. Its design section says "a pass that
  wrote `LinkUp` a moment earlier cannot stop the press", and a few
  lines before that it says "A pass that finds the controller
  connected disarms it". Its list of what the build added says that
  every reason other than `Asleep` disarms the relay, and it gives no
  reason why a `LinkUp` before the open is safe.
- The case "a remote that a pass found connected" of
  `TestTheRelayWritesNoPress` in `wake_test.go` runs `follow` with
  `Asleep` and then `LinkUp`, then opens the node, and asserts that
  the node receives nothing. So the tests hold the behavior of
  `wake.go`, not the sentence of plan 12.
- Plan 12's drill has not run, so no measurement shows how long the
  gap is on a real remote.

## The reproduction

The test armed the relay with an `Asleep` condition an hour old, then
passed it a `LinkUp` condition, as a pass that sees the remote
connected first does, and then opened the remote's keyboard node. It
asserted that the virtual device receives one press and release of
`KEY_UNKNOWN`, as plan 12 states, and it failed with:

    the keyboard node received , want one press and release of KEY_UNKNOWN

The test is not in the tree, because a failing test cannot land on
`main`. How long the gap is between BlueZ's `Connected`
signal and the HID device's uevent needs a `btmon` trace and the
operator's log for a T6 wake on the physical test cluster.

## What a fix has to decide

When a pass sees a T6 remote connected before the relay opens its
input node, does the sleeping screen still get the wake press? Plan
12's sentence says yes, and `wake.go` and its test say no.

- Whether `LinkUp` cancels a scheduled press, or only a new reason
  that ends a sleep does.
- How long a press stays scheduled after the link comes back, so that
  a remote that reconnects with no HID device does not get a press
  much later.
- Whether plan 12's design or the comment in `wake.go` describes the
  behavior that the project wants.
