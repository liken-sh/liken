# 09, The guider

Proposed on 2026-10-05. Not built.

## The problem

PHD2 guides the mount, and both KStars and `astrophotography-operator`
drive it over its event API on port 4400. PHD2 is a desktop program
with no headless mode, and three properties of it break in a container:
it needs a Wayland compositor, it opens modal dialogs that crash it on
a compositor with no seat, and its instance lock outlives a crash.

## The requirement

A `Guider` belongs to a `Telescope` and names the `OpticalTrain` whose
camera guides, as plan 06 describes. Its pod runs PHD2 from its PPA as
a client of the telescope's INDI server,
with no device claims, and the `weston` image as a headless sidecar.
Before each start, the pod writes PHD2's whole profile, including
`ConfigVersion=2001` and the INDI camera and mount, and removes the
`~/phd2.1` lock. A display claim puts PHD2 on a monitor when a person
wants to watch it.

[The compositor advertises no seat](../../display-operator/plans/open-problems/the-compositor-advertises-no-seat.md)
stays open until display-operator decides on a remedy. Until then, a
modal dialog during a night ends PHD2, and the pod restarts it.

On a slow link, PHD2's guide frames cost a hop each way: plan 03
measured 1,082 ms from the end of an exposure to a client two hops
away, at 76 Mbit/s, against 13 ms on one host. The guider's pod runs on
the node of the server and the guide camera when the link is slow.

## How we test it

PHD2 calibrates and guides on the simulators on the `lab` fleet, as it
did in root plan 74's experiment: 19 calibration steps, then about one guide step a
second with 1-second exposures.

## Upstream issues

- [phd2#683](https://github.com/OpenPHDGuiding/phd2/issues/683), open:
  PHD2 has no true headless mode, and its interface and its API are
  not separate.
- [phd2#485](https://github.com/OpenPHDGuiding/phd2/issues/485): the
  instance lock is a `wxSingleInstanceChecker` file in `$HOME` that
  stays after a crash.
- [phd2#1090](https://github.com/OpenPHDGuiding/phd2/issues/1090), open:
  the camera and the mount must be on one INDI server. The single
  server meets that.
- [phd2#1247](https://github.com/OpenPHDGuiding/phd2/issues/1247), open:
  the INDI camera backend sends an empty `setBLOBVector` while busy.
- [phd2#1438](https://github.com/OpenPHDGuiding/phd2/pull/1438), open:
  multi-star data for the event server. The event API is documented
  only on the wiki, at
  <https://github.com/OpenPHDGuiding/phd2/wiki/EventMonitoring>.

## References

- The PHD2 PPA: <https://launchpad.net/~pch/+archive/ubuntu/phd2>
- The event server: `src/event_server.cpp` in `OpenPHDGuiding/phd2`
- The command line: `src/phd.cpp`, and the profile: `src/phdconfig.cpp`
- The INDI backends: `src/cam_indi.cpp` and `src/scope_indi.cpp`
- [Root plan 74](../../plans/74-astrophotography.md), "Guiding"
