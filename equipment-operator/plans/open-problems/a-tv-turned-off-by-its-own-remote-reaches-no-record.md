# A TV turned off by its own remote reaches no record

A `Player`'s `spec.power` records whether it is on (root plan 84). A
person can still turn the TV off with the TV's own remote, and the TV
then announces nothing on HDMI-CEC when it does not broadcast Standby.
The `Player` stays `On`, `status.power` on the `Television` stays
stale until a power read, and the next key press finds an awake
session with its TV off.

When the TV does broadcast Standby, the node workload writes
`status.screenAsk` `Sleep`, and the screen goes dark. That moves the
panel desire, not `spec.power`.

Open questions:

* Whether a Standby the TV broadcasts should set the `Player`'s
  `spec.power` to `Off`, so the two agree.
* What the operator can learn from a TV that announces nothing, with
  no timer that reads the TV's power again (root `AGENTS.md`, "Keep
  state current with events").
