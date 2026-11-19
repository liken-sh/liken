# `Asleep` keeps the time of an earlier disconnect

Open bug, recorded on 2026-11-18. A unit test reproduced the first of
the two outcomes below on 2026-11-19. No unit test has run the
second.

The wake press counts the sleep from the `lastTransitionTime` of the
`Connected` condition. That time moves only when the condition's
status changes. When a remote goes from one `False` reason to
`Asleep` with no pass that saw it connected in between, the `Asleep`
condition keeps the time of the earlier disconnect, and the relay
counts the sleep from that time.

## Where it happens

- `connectedCondition` in `connected.go` builds the condition with the
  time of the pass, and then passes it through `conditions.Set`.
- `conditions.Set` in `kubernetes/conditions/conditions.go` keeps the
  stored `lastTransitionTime` when the status does not change, even
  when the reason does.
- `relays.follow` in `wake.go` sets `asleepSince` to that time, and
  returns without a change when the time equals `wakePressedFor`, the
  sleep that already got a press.
- `relays.pressToWake` writes the press when `asleepSince` is at least
  `wakeAfter`, 10 seconds, in the past.

## The two outcomes

A remote can connect and disconnect again between two passes, because
a pass runs only after `settleWindow` with no event, or at
`settleLimit`. Then the pass sees a change from one `False` reason to
another with no `LinkUp` between:

- From `ClosedByDevice`, `ClosedByRadio`, or another `False` reason,
  to `Asleep`:
  `asleepSince` is the older time. A remote that was down for less
  than `wakeAfter` can get a press, and that press is the spurious
  wake that `wakeAfter` exists to stop.
- From `Asleep` to `Asleep`: the condition does not change, so its
  time is the time of the first sleep. If that sleep already got a
  press, `follow` sees the same time as `wakePressedFor` and schedules
  nothing, and the second wake gets no press.

## The reproduction

The test built the `Connected` condition with `connectedCondition`
from a stored `ClosedByDevice` condition an hour old, for a remote
that is disconnected and whose link timed out, so the pass writes
`Asleep`. It passed that condition to `follow`, set the relay's clock
5 seconds after the pass, and opened the remote's keyboard node. It
asserted that the node receives no press, because the sleep is
shorter than `wakeAfter`, and it failed with:

    the keyboard node received 000000000000000000000000000000000100f00001000000000000000000000000000000000000000000000000000000000000000000000000000000000000000100f00000000000000000000000000000000000000000000000000000000000 5 seconds after the sleep, want nothing because the sleep is shorter than 10s

The test is not in the tree, because a failing test cannot land on
`main`. The second outcome, from `Asleep` to `Asleep`
after a press, needs a test of the same form with a stored `Asleep`
condition whose time already got a press.

## What a fix has to decide

Should the 10-second rule for a wake press count from the time that
the `Connected` condition last changed its status, or from the time
that the operator saw the link drop?

- Whether the sleep is timed from the condition, which follows the
  Kubernetes rule for `lastTransitionTime`, or from a time that the
  operator records when it reads the `Disconnected` signal.
- Whether a change of reason with the same status moves the time for
  this condition only, or whether `conditions.Set` keeps one rule for
  every component.
