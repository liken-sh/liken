---
aliases: [/docs/reference/the-guider/]
title: The guider
weight: 60
---

# The guider

A `Guider` runs PHD2 for its `Telescope`, with the camera of the
`OpticalTrain` that the `Guider` names. The `StartGuider` step starts
PHD2, connects it to the camera and the mount, and leaves it idle. The
operator never loops, calibrates, or guides, because calibration needs
a tracking mount and a star. The client that the `Reservation` names in
`spec.holder`, such as a person at KStars or `astrophotography-operator`,
aligns the mount, starts tracking, and then starts guiding. It connects
to PHD2's event server at the `Guider`'s `status.endpoint`, such as
`east-guider.observatory.svc:4400`.

The guider's pod runs two containers. PHD2 runs from
`ghcr.io/liken-sh/indi-phd2`, which the `indi` build makes from the
`indi` image, so PHD2 uses the same libindi as the server it connects
to. PHD2 has no headless mode, so it draws on a headless weston in a
native sidecar container, through a Wayland socket that the two
containers share. The sidecar is the `weston` image that
`display-operator` builds on, at the tag that `weston/package.toml`
pins, so a node that runs `display-operator` already has that image.
The pod has no device claim. It runs on the node of the telescope's
INDI server, because PHD2 downloads every guide frame from the server.

PHD2 writes its configuration while it runs, so the operator writes the
whole profile into the `ConfigMap` `<guider>-guider`, and the image
copies it into `$HOME` before each start. The profile contains the INDI
server's address, the guide camera and the mount by their INDI device
names, the guide tube's focal length, and where guide pulses go.
`spec.pulses: Mount` sends them to the mount's driver, and `Camera`
sends them to the guide camera's ST-4 port. A change to the profile
replaces the pod. During a session, the operator reads the `Guider`,
its train, and the guide tube from the session's record, so an edit of
them reaches the profile at the next session, and PHD2 keeps its
calibration until then.

The `StopGuider` step deletes the pod before `Disconnect`, so PHD2
never sees its devices disconnect and never opens the progress dialog
of its reconnect code. On a compositor with no seat, a modal dialog
ends PHD2, and the kubelet starts it again.
[Plan 09](https://github.com/liken-sh/liken/blob/main/observatory-operator/plans/completed/09-the-guider.md)
lists the dialogs that PHD2 can open.

While the operator's connection to PHD2's event server is open, the
operator writes what PHD2 reports into the `Guider`'s status:

- `state`: PHD2's state, `Stopped`, `Selected`, `Looping`,
  `Calibrating`, `Guiding`, `LostLock`, or `Paused`.
- `calibrated`, and `pixelScale` in arc-seconds per pixel.
- `rms`: the root mean square of the star's distance from the lock
  position in right ascension, declination, and total, in arc-seconds,
  over the last 100 guide steps that the operator received since
  guiding started. `rms.since` is the time of the oldest of those
  steps. PHD2 does not send earlier steps to a new connection, so the
  window starts again when the operator restarts.
- `star`: the guide star's SNR and its HFD in pixels, and
  `lastStepTime`, from the last guide step.
- `alert`: the last alert that PHD2 showed, or the last calibration
  that failed.

The `Ready` condition is `True` while PHD2 runs and reports its camera
and mount connected. The operator writes a `Guider`'s status at most
once a second, however often PHD2 reports guide steps.

```sh
kubectl get guider -n observatory
kubectl port-forward -n observatory svc/east-guider 4400
```
