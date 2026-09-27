# A control claim needs no compositor

Plan 25. Built on 2026-09-27.

A control device delivers the connector's i2c node, and DDC/CI
reaches the panel whether or not a compositor draws on it. Two parts
of the operator made every claim wait for the compositor's socket, so
a control-only pod waited for weston and was evicted on every
compositor restart. This plan splits the one rule into two. It
answers and replaces the open problem "A control claim waits for the
compositor".

## The two rules

`prepareClaim` checks the compositor's socket and the layout module
only for results that deliver a Wayland socket: an output result or
a draw result. `waylandServing` holds both checks. The loop calls it
once per claim, on the first result that needs it. A control result
before it builds CDI edits only and writes nothing to the panel, so a
mixed claim that waits changes nothing on that pass, whatever the
order of its results.

`compositorDown` taints output devices and draw devices only. A
control device keeps the taints that `sliceDevices` gave its output
for the monitor, so it is still tainted when a different monitor
replaced the one on the connector. A connector with no monitor
publishes no control device, so a control claim never takes a
connector with no panel.

## What changes for a pod

A control-only pod prepares while no compositor runs, and it keeps
running through a compositor restart. A claim with an output or a
draw device waits as before. The mode, heal, hung, and masterless
restarts no longer end a control-only pod.

The compositor check now runs after the claim read and the parsing of
its configuration. A claim that waits costs one API read on each
kubelet retry, and a claim with a bad configuration reports that
error before the compositor error.

## Tests

`compositorwait_test.go` holds a prepare table for control-only,
output-only, draw-only, and mixed claims in both result orders, with
the compositor up and down, and a taint table for each kind of device,
a replaced monitor included. It also checks that a control claim gets
no socket while the compositor is down, and that a waiting mixed claim
that states a brightness writes nothing to the panel.

## Not measured

This plan is not drilled on liken-1. The drill: stop the compositor,
prepare a control-only claim, set the brightness, and restart the
compositor with the pod running.
