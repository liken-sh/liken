---
name: guide-with-phd2
description: "Add a Guider to a telescope so the operator runs PHD2 with the guide camera and the mount, then calibrate and guide from KStars and Ekos through PHD2's event server, and read the guiding RMS from the Guider's status. Use when a telescope needs autoguiding, when choosing between mount and ST-4 pulses, or when PHD2 does not connect or keeps restarting."
---

This skill is the guide at https://liken.sh/observatory/docs/guides/guide-with-phd2/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Guide with PHD2

A `Guider` makes the operator run [PHD2](https://openphdguiding.org/)
for one telescope. During a reservation, the operator starts PHD2 and
connects it to the guide camera and the mount. You calibrate and guide
from KStars, through PHD2's event server, as you would with PHD2 on
your own computer. You need a `Telescope` with a mount, a guide
camera, and a `Reservation`, as
[Reserve a telescope](https://liken.sh/observatory/docs/guides/reserve-a-telescope/) shows.

## Declare the guider

A `Guider` refers to its telescope and to the optical train whose
camera guides. For a guide scope, that train is on its own small
tube. For an off-axis guider, it is on the imaging tube.

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

`pulses` sets where PHD2 sends its corrections:

* `Mount` sends them to the mount's driver, over the mount's own
  connection. Use this unless you have a reason not to.
* `Camera` sends them through the guide camera's ST-4 port, for a
  mount that is cabled for ST-4 guiding.

The operator writes PHD2's profile from these resources: the INDI
server, the INDI names of the guide camera and the mount, and the
guide tube's focal length. PHD2 computes the pixel scale from the
focal length and the camera's pixel size, so give the guide tube's
real focal length.

## What the operator does

When a reservation of the telescope reaches its `StartGuider` step,
the operator starts PHD2, connects its camera and mount, and waits
until PHD2 reports both connected. Then the reservation is `Ready`.

The operator never loops, calibrates, or guides, and it never starts
tracking, because calibration needs a tracking mount and a star. You
align the mount, start tracking, and then start guiding.

At the end of the reservation, the operator stops guiding and
exposures, and stops PHD2 before it disconnects the camera and the
mount.

PHD2 runs in the pod `<guider>-guider`, such as `east-guider`. PHD2
reads a guide frame from the INDI server about once a second, so its
pod runs on the same node as the telescope's INDI server. The guide
camera's pod runs there too, unless its claim places it on another
node.

## Guide from KStars

The guider's `status.endpoint` has the host of PHD2's event server,
such as `east-guider.observatory.svc`, and its port, 4400. From a
desktop, forward the port:

```sh
kubectl port-forward -n observatory svc/east-guider 4400
```

In the Ekos guide module, choose PHD2 as the guider, with host
`localhost` and port `4400`, and connect. Ekos then shows PHD2's state
and its guiding graph, and its buttons start looping, calibration, and
guiding. [Reserve a telescope](https://liken.sh/observatory/docs/guides/reserve-a-telescope/#reach-the-telescope-from-outside-the-cluster)
gives other ways to reach the port.

You cannot see PHD2's own window. PHD2 cannot run without a display,
so it draws on a headless display in the pod.

## Read the guiding

```sh
kubectl get guider -n observatory
kubectl get telescope -n observatory -o wide
```

The guider's status shows what PHD2 reports:

* `state`: what PHD2 is doing, such as `Looping`, `Calibrating`,
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

If PHD2's pod is deleted, or the kubelet ends it, for example for an
eviction, the operator creates it again and connects
PHD2's camera and mount. It also connects them again when the
telescope's INDI server restarts. A new PHD2 starts idle and not
calibrated, so calibrate and start guiding again from Ekos.

If PHD2 exits and the kubelet restarts its container in the same pod,
the operator does not connect the camera and the mount again. Connect
them from the Ekos guide module, or delete the pod so that the
operator creates a new one:

```sh
kubectl delete pod -n observatory east-guider
```

PHD2 can exit by itself when it opens a dialog that waits for an
answer, because nobody can answer on the headless display. If the
guider restarts again and again, read its log:

```sh
kubectl logs -n observatory east-guider -c phd2 --previous
```
