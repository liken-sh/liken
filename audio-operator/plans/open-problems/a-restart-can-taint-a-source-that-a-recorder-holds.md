# A restart can taint a `Source` that a recorder holds

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it. The code was
checked on 2026-11-19: `audio-operator/main.go:276` passes
`sinkEndpoints(named)` to `waitForNodes`, so the wait leaves out the
capture endpoints. No unit test runs `main`, so only the drill below
can show the eviction. A small function that returns the endpoints to
wait for, extracted from `main` with no change in behavior, would let
a unit test show the shorter fact.

At startup, `main.go` in `audio-operator` calls `waitForNodes` before
the first reconcile pass, so that the pass does not run before
PipeWire has created its declared nodes. Without the wait, the pass
publishes each endpoint with no node with the `disconnected` taint,
whose effect is `NoExecute`, and the taint evicts the pods on claims
that the previous operator prepared.

The wait covers only the playback endpoints, through `sinkEndpoints`.
The comment beside it gives the reason: a capture node that PipeWire
could not create affects a recorder only when it claims the source,
and no running pod holds one at startup. That is not true when the
operator's pod is replaced while a recorder holds a `Source` claim.
The recorder's claim stays prepared across the replacement, as a
speaker's claim does. `sliceDevices` in `devices.go` gives a capture
endpoint with no node the same `NoExecute` taint as a playback
endpoint, so a first pass that runs before PipeWire creates the
capture node can evict the recorder.

## What would confirm it

A drill on the `lab` fleet, which has not run: a pod that records
from a `Source` with no toleration for the `disconnected` taint, then
a restart of the operator's pod, and the recorder's pod events.

## What a fix has to decide

Whether the wait covers every endpoint. The comment does not say why
the capture endpoints were left out, other than the claim above, so a
fix has to find out whether a capture node can take longer to appear
than a playback node, which would make the wait longer at every
start.
