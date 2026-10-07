# 10, Access from a desktop

Proposed on 2026-10-05. Closed on 2026-10-07, built as documentation:
the cluster owner chooses the path from a desktop, and the manual names
what that path selects. This plan completes mode 1. KStars reached the
`lab` fleet's simulators through `kubectl port-forward` before the
plan closed. The full drill below, with an Ekos capture sequence, a
plate solve, and PHD2 guiding, has not run.

## The problem

In mode 1, a person drives the observatory from KStars on a desktop
outside the cluster. INDI has no authentication: a client that reaches
port 7624 can slew the mount, and a client that reaches port 4400 can
command PHD2. `indiserver` parses XML from every client that connects.

## The requirement

- KStars on a desktop reaches the INDI server and PHD2 through a path
  that the cluster controls, and nothing else in the cluster or on the
  network reaches them.
- A `NetworkPolicy` allows only that path and the observatory's own
  pods.
- The holder of the telescope's `Reservation`, from plan 06, records
  which client drives the telescope, so a person can take it with
  KStars and give it back.
- KStars receives frames as BLOBs, through the `pipes` shim, in
  `UPLOAD_CLIENT` or `UPLOAD_BOTH`.

## How we test it

KStars on a desktop connects to the `lab` fleet's simulators, runs an
Ekos capture sequence and a plate solve, and guides with PHD2. That is
the end of mode 1.

## Upstream issues

- [indi#2472](https://github.com/indilib/indi/issues/2472): the CVE of
  plan 01 crashes `indiserver` from one packet, and the issue states
  that the protocol has no authentication or encryption.
- KStars tracks bugs at bugs.kde.org. No bug about remote upload modes
  or latency was found. Two forum threads describe people driving Ekos
  over the internet:
  <https://indilib.org/forum/ekos/7783-difficulties-setting-kstars-ekos-to-work-over-internet-connection.html>
  and <https://indilib.org/forum/general/7091-kstar-ekos-remote-connection.html>.

## References

- Ekos: <https://kstars-docs.kde.org/en/user_manual/ekos.html>
- Upload modes: `UploadSP` in `libs/indibase/indiccd.cpp`
- [Root plan 74](../../plans/74-astrophotography.md), "Frames" and
  "Security"

## What was built

The operator chooses no path into the cluster and writes no
`NetworkPolicy`. A port forward, a `LoadBalancer`, a VPN, and a mesh
each suit a different network, and only the cluster owner knows which
one fits. A policy that the operator wrote would block the path the
owner chose. So the requirements changed:

- The path is the cluster owner's. The manual's guide "Reserve a
  telescope" gives a port forward and a `LoadBalancer` `Service` of the
  owner's own, which selects the server's and the guider's pods by the
  labels `observatory.liken.sh/kind` and `observatory.liken.sh/resource`.
  The operator deletes its own `Service`s at the end of each
  reservation, so an edit to them is lost, and the owner's `Service`
  stays.
- The `NetworkPolicy` is the cluster owner's. The guide states that
  INDI and PHD2 have no authentication, and which traffic the pods of
  the namespace need.
- The holder was already built: `Reservation.spec.holder` is free
  text from plan 06.
- The upload mode needs no work: the operator never writes
  `UPLOAD_MODE`, so Ekos sets it from the client.

The reference page "Objects the operator creates" lists the names, the
ports, and the labels as the contract that the owner's objects select.
