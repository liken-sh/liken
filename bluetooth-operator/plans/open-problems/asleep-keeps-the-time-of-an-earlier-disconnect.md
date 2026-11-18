# `Asleep` keeps the time of an earlier disconnect

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

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

## What would confirm it

A test that runs `connectedCondition` and `follow` on a stored
`ClosedByDevice` condition and then on a stored `Asleep` condition
that already got a press, with a device that is disconnected in both
passes and a new `Asleep` link-end reason. The test checks
`asleepSince` against the time of the second pass.

## What a fix has to decide

- Whether the sleep is timed from the condition, which follows the
  Kubernetes rule for `lastTransitionTime`, or from a time that the
  operator records when it reads the `Disconnected` signal.
- Whether a change of reason with the same status moves the time for
  this condition only, or whether `conditions.Set` keeps one rule for
  every component.
