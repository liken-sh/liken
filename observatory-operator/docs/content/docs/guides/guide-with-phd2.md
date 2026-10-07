---
title: Guide with PHD2
weight: 60
description: "Add a Guider to a telescope so the operator runs PHD2 with the guide camera and the mount, then calibrate and guide from KStars and Ekos through PHD2's event server, and read the guiding RMS from the Guider's status. Use when a telescope needs autoguiding, when choosing between mount and ST-4 pulses, or when PHD2 does not connect or keeps restarting."
---

A `Guider` makes the operator run [PHD2](https://openphdguiding.org/)
for one telescope. During a reservation, the operator starts PHD2,
connects it to the guide camera and the mount, and then leaves it
idle. You calibrate and guide from KStars, through PHD2's event
server, as you would with PHD2 on your own computer.

## Declare the guider

The guider names its telescope and the optical train whose camera
guides. A guide scope is a train on its own small tube. An off-axis
guider is a train on the imaging tube.

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: OpticalTube
metadata:
  name: east-guidescope
spec:
  telescope: east
  aperture: 50
  focalLength: 200
---
apiVersion: observatory.liken.sh/v1alpha1
kind: OpticalTrain
metadata:
  name: east-guiding
spec:
  telescope: east
  opticalTube: east-guidescope
---
apiVersion: observatory.liken.sh/v1alpha1
kind: Camera
metadata:
  name: east-guide
spec:
  opticalTrain: east-guiding
  driver: {name: indi_simulator_guide}
---
apiVersion: observatory.liken.sh/v1alpha1
kind: Guider
metadata:
  name: east
spec:
  telescope: east
  opticalTrain: east-guiding
  pulses: Mount
```

`pulses` says where PHD2 sends its corrections:

* `Mount` sends them to the mount's driver, over the mount's own
  connection. Use this unless you have a reason not to.
* `Camera` sends them through the guide camera's ST-4 port, for a
  mount that is cabled for ST-4 guiding.

The operator writes PHD2's profile from these resources: the INDI
server, the guide camera and the mount by their INDI names, and the
guide tube's focal length. PHD2 computes the pixel scale from the
focal length and the camera's pixel size, so give the guide tube's
real focal length.

## What the operator does

When a reservation of the telescope reaches its `StartGuider` step,
the operator starts PHD2, connects its camera and mount, and waits
until PHD2 reports both connected. Then the reservation is `Ready`.

The operator never loops, calibrates, or guides. The mount is not
tracking yet when the reservation becomes `Ready`, and calibration
needs a tracking mount and a star. So you align the mount, start
tracking, and then start guiding.

At the end of the reservation, the operator stops guiding and
exposures, and stops PHD2 before it disconnects the camera and the
mount.

PHD2 runs in the pod `<guider>-guider`, such as `east-guider`. The
pod runs on the same node as the telescope's INDI server, and so does
the guide camera unless its claim puts it elsewhere, because PHD2 reads
a guide frame about once a second.

## Guide from KStars

The guider's `status.endpoint` names PHD2's event server, such as
`east-guider.observatory.svc:4400`. From a desktop, forward the port:

```sh
kubectl port-forward -n observatory svc/east-guider 4400
```

In the Ekos guide module, choose PHD2 as the guider, with host
`localhost` and port `4400`, and connect. Ekos then shows PHD2's state
and its guiding graph, and its buttons start looping, calibration, and
guiding. [Reserve a telescope](/docs/guides/reserve-a-telescope/#reach-the-telescope-from-outside-the-cluster)
gives other ways to reach the port.

PHD2's own window is not visible. It draws on a headless display in
the pod, because PHD2 cannot run without one.

## Read the guiding

```sh
kubectl get guider -n observatory
kubectl get telescope -n observatory -o wide
```

The guider's status holds what PHD2 reports:

* `state`: what PHD2 does, such as `Looping`, `Calibrating`,
  `Guiding`, or `LostLock`
* `calibrated`, and `pixelScale` in arc-seconds per pixel
* `rms`: the guiding error in arc-seconds, in right ascension,
  declination, and total, over the last 100 guide steps
* `star`: the guide star's SNR and its HFD in pixels
* `alert`: the last alert PHD2 showed, or the last calibration that
  failed

The telescope's `Guider` column, in `-o wide`, shows the guider's
phase and PHD2's state together, such as `Ready, Guiding`.

## When PHD2 restarts

If PHD2's pod is deleted, or PHD2 exits, the operator starts it again
and connects its camera and mount. A new PHD2 starts idle and not
calibrated, so calibrate and start guiding again from Ekos.

PHD2 can exit by itself when it opens a dialog that waits for an
answer, because the headless display has no one to answer it. If the
guider restarts again and again, read its log:

```sh
kubectl logs -n observatory east-guider -c phd2 --previous
```
