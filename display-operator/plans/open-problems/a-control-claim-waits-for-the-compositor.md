# A control claim waits for the compositor

Open problem. A control device delivers the connector's i2c node,
and DDC/CI needs no compositor: the i2c bus connects to the panel
whether or not a compositor draws on it. But two parts of the
operator make every claim wait for the compositor's socket, so a
control-only pod waits for Weston today.

The two parts:

* `prepareClaim` refuses every claim while no compositor answers on
  the socket, before it looks at what the claim holds.
* `compositorDown` taints every device in the slice, control
  devices included, because `controlDevice` takes the output
  device's taint list whole.

`TestCompositorDownTaintsTheControlDeviceToo` locks the current
behavior in on purpose, so a fix starts by rewriting that test.

The behavior is safe, and its only cost is a delay. A compositor
restart lasts about 1.3 seconds, and a control-only pod that waits
through it starts a moment late. The split would matter on a
machine that runs no compositor at all. No `liken` machine does
that while the operator's pod holds the card, so no machine needs
the split today.

The split replaces the one rule with two rules. First, a control
device gets the `disconnected` taint when no monitor is connected,
because then there is no panel to control, and it does not get the
taint when only the compositor is down. Second, `prepareClaim`
checks the socket only for results that deliver it. The second
rule moves the socket check inside the per-result loop, and the
failure message no longer names a socket that a control claim never
receives.
