# A control claim waits for the compositor

Open problem. A control device delivers the connector's i2c node,
and DDC/CI needs no compositor: the wire runs to the panel whether
or not anything draws. But two pieces of the operator gate every
claim on the compositor's socket anyway, so a control-only pod
waits for Weston today.

The two pieces:

* `prepareClaim` refuses every claim while nothing answers the
  socket, before it looks at what the claim holds.
* `compositorDown` taints every device in the slice, control
  devices included, because `controlDevice` takes the output
  device's taint list whole.

`TestCompositorDownTaintsTheControlDeviceToo` locks the current
behavior in on purpose, so a fix starts by rewriting that test.

The behavior is safe and merely conservative. A compositor restart
lasts about 1.3 seconds, and a control-only pod that waits through
it starts a moment late. The split would matter on a machine that
runs no compositor at all, which no `liken` machine does while the
operator's pod holds the card, so nothing pushes on this today.

Splitting it means two rules where there is one: a control device
keeps the `disconnected` taint (no monitor, no panel to control)
and drops the compositor half, and `prepareClaim` checks the socket
only for results that deliver it. The second rule moves the socket
check inside the per-result loop, and the failure message stops
naming a socket a control claim never receives.
