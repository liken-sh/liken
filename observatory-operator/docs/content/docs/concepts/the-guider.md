---
aliases: [/docs/reference/the-guider/]
title: The guider
weight: 60
---

A `Guider` runs PHD2 for its `Telescope`, with the camera of the
`OpticalTrain` it names. `StartGuider` starts PHD2, connects it to the
camera and the mount, and leaves it idle. The operator never loops,
calibrates, or guides: tracking stays off after activation until the
holder aligns the mount, so the holder drives PHD2 from then on. KStars
or `astrophotography-operator` connects to PHD2's event server at the
`Guider`'s `status.endpoint`, such as
`east-guider.observatory.svc:4400`.

The guider's pod runs two containers. PHD2 runs from
`ghcr.io/liken-sh/indi-phd2`, which the `indi` build makes on the
`indi` image, so PHD2 links the libindi of the server it talks to. PHD2
has no headless mode, so it draws on a headless weston in a native
sidecar, through a Wayland socket that the two containers share. The
sidecar is the `weston` image that `display-operator` builds on, at the
tag that `weston/package.toml` pins, so a node that runs
`display-operator` pulls no new image for it, and one bump of `weston`
moves both. The pod has no device claim, and it runs on the node of
the telescope's server, because PHD2 reads a guide frame from the
server about once a second.

PHD2 writes its config while it runs, so the operator writes the whole
profile into the `ConfigMap` `<guider>-guider`, and the image copies it
into `$HOME` before each start. The profile names the INDI server, the
guide camera and the mount as the server names them, the guide tube's
focal length, and where the pulses go: `spec.pulses: Mount` sends them
to the mount's driver, and `Camera` to the guide camera's ST-4 port. A
change to the profile replaces the pod.

`StopGuider` deletes the pod before `Disconnect`, so PHD2 never sees
its devices drop. On a compositor with no seat, a modal dialog ends
PHD2, and the kubelet starts it again: [plan
09](https://github.com/liken-sh/liken/blob/main/observatory-operator/plans/completed/09-the-guider.md) lists the dialogs that PHD2 can open.

A `Guider`'s status holds what PHD2 reports while the operator's
connection to its event server is open:

- `state`: PHD2's state, `Stopped`, `Selected`, `Looping`,
  `Calibrating`, `Guiding`, `LostLock`, or `Paused`.
- `calibrated`, and `pixelScale` in arc-seconds per pixel.
- `rms`: the RMS of the star's distance from the lock position in
  right ascension, declination, and total, in arc-seconds, over the
  last 100 guide steps that the operator received since guiding
  started. `rms.since` is the time of the first of those steps. PHD2
  sends a new connection no earlier steps, so the window also starts
  again when the operator restarts.
- `star`: the guide star's SNR and its HFD in pixels, and
  `lastStepTime`, from the last guide step.
- `alert`: the last alert PHD2 showed, or the last calibration that
  failed.

The `Ready` condition is `True` while PHD2 runs and reports its camera
and mount connected. The status writer writes at most once a second,
so a guide step a second costs one write a second.

```sh
kubectl get guider -n observatory
kubectl port-forward -n observatory svc/east-guider 4400
```
