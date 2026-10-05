# 10, Access from a desktop

Proposed on 2026-10-05. Not built. This plan completes mode 1.

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
