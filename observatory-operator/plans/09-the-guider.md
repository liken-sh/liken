# 09, The guider

Proposed on 2026-10-05. The guide camera's placement is built; the
rest is not.

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

The operator of plan 07 places no pod, so this plan places all three:
the telescope's server, the guide camera's pod, and the guider's pod.
On the two-node test cluster, the scheduler put the guide camera's pod
on the other node from the server, so each guide frame crossed the
76 Mbit/s link.

### The guide camera's placement is built

Built on 2026-10-05, before the guider's pod, and tested against the
fake API server in `placement_test.go`. Not drilled on the test
cluster yet.

The camera of the `OpticalTrain` that a `Guider` names has a required
pod affinity to its telescope's server pod, on the node label
`kubernetes.io/hostname`. With no `Guider`, no pod has an affinity.

The camera follows the server, not the reverse. `PowerOn` creates the
server's pod before the guide camera's pod exists, and the `Switch`
that can power the camera runs on that server. A server with an
affinity to the camera would wait for a pod that waits for the server.

A guide camera with a `spec.claim` gets no affinity. The node of its
device decides where its pod runs, so an affinity to a server on
another node would hold the pod Pending until the step's deadline. On
real hardware, the server still lands on any node, and a guide frame
crosses the link when the server and the camera are apart. Placing the
server on the node of the claimed devices stays with this plan, with
the guider's pod.

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
