---
name: troubleshoot
description: "Find why a reservation failed or waits, from the step and the device its status names: a missing parent, two devices on one driver, a pod that stays Pending, a device that does not connect, a park that a lock refused, or a delete that does not finish. Then run the failed step again with the retry annotation. Use when a Reservation is Failed or stuck, when a device reports Error, or when a resource will not delete."
---

This skill is the guide at https://liken.sh/observatory/docs/guides/troubleshoot/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

Start from the reservation. Its status names the step that failed or
waits, and the step's summary names the device:

```sh
kubectl get rsv -n observatory
kubectl describe reservation east-tonight -n observatory
```

`kubectl describe` lists every step with its state and summary, and
the reservation's Events: one for each step that ends, with the time
it took, and a `Warning` for a step that failed. The `Ready`
condition's message gives the same step and device in one line.

Then read the device that the summary names, and the operator's log:

```sh
kubectl describe mount east -n observatory
kubectl logs -n observatory deployment/observatory-operator
```

The sections below follow the steps in the order they run.

## The reservation stays in Wait

`Wait` waits for the start time, for the telescope to exist, and for
no other reservation to hold it. The summary says which:

* `Missing Telescope east`: no `Telescope` of that name exists in
  `observatory`. Check the name and the namespace.
* A reservation name: another reservation holds the telescope. It
  takes the telescope when that one is `Released`. A `Released`
  reservation no longer holds anything, but a `Failed` one does until
  you delete it.
* `Telescope east is being deleted`: a delete of the telescope or its
  observatory is in progress.

## A resource reports ParentFound False

A resource whose parent does not exist reports `ParentFound` `False`,
and the message names the missing parent. It stays in place and does
nothing until the parent exists. This is almost always a typo in the
parent field, or a parent in another namespace.

## StartSite or PowerOn fails: two devices name one driver

One INDI server runs each driver once. When two devices on one server
name the same driver, the step fails, and its message names both
devices and the driver. Give one of the devices another driver, move
it to the shelf, or move it to another telescope.

A device that joins a running reservation with a driver that another
device already runs does not start. It reports `Error` with the name
of the device that holds the driver, and the running device stays
connected.

## StartDevices fails: a device never appears

This step starts each device's pod and waits until its driver appears
on the INDI server. It fails after 10 minutes. Look at the pod of the
device that the summary names:

```sh
kubectl get pod east-mount -n observatory
kubectl describe pod east-mount -n observatory
```

* **The pod is `Pending`.** Its `ResourceClaim` matches no free
  device. Check that the machine publishes the device and that the
  `DeviceClass` selects it, as
  [Connect USB equipment](https://liken.sh/observatory/docs/guides/connect-usb-equipment/) describes.
  A camera on a vendor SDK, such as a ZWO ASI, cannot be claimed today.
* **The image does not pull.** A vendor's driver image is large. The
  ToupTek image is over 300 MB, so the first pull on a slow link can
  take most of the deadline. Run the step again once the image is on
  the node.
* **The pod runs, but the driver never appears.** The driver name is
  probably wrong, so the image does not hold it. Check the spelling
  against the [INDI device list](https://indilib.org/devices.html)
  and [Drivers and images](https://liken.sh/observatory/docs/reference/drivers-and-images/).

## Connect fails

The operator connects each device and waits for the driver to answer.
A driver that cannot reach its hardware answers with an error, and the
step names it. For a USB device, the usual cause is the port: see
[Check the port](https://liken.sh/observatory/docs/guides/connect-usb-equipment/#6-check-the-port).
The INDI server's log shows the driver's own messages:

```sh
kubectl logs -n observatory east-telescope
```

## An activation or trigger action fails

Each action has a timeout, and its summary says what it waited for.
Read the procedures of the resource:

```sh
kubectl get dome lab -n observatory -o jsonpath='{.status.procedures}' | jq
```

* **`Waiting for WeatherStation lab Safe=True`**, and then a timeout:
  the action's `requires` never held. That is the procedure working as
  written, on an unsafe night.
* **A park lock refused the move.** The device posts a `Warning`:
  `MountUnparkRefused` on a `Mount` that cannot unpark while the dome
  is parked, or `DomeParkRefused` on a `Dome` that cannot park while a
  mount is unparked. In a trigger, add `after: [{kind: Mount}]` to the
  dome's park, as
  [React to the weather](https://liken.sh/observatory/docs/guides/automate-the-equipment/#react-to-the-weather)
  shows.
* **A `job` failed.** The summary gives the exit code and the last
  lines of its output. Its logs stay for an hour:

  ```sh
  kubectl get jobs -n observatory -l observatory.liken.sh/role=job
  kubectl logs -n observatory job/<name>
  ```

## Run a failed step again

After a failed activation step, the telescope stays as the steps left
it, so you can look at it, and even connect KStars to fix a setting.
After a failed deactivation step, the reservation keeps its finalizer,
because the equipment may not be safe to power off. In both cases, fix
the cause, and then run the failed step again:

```sh
kubectl annotate reservation east-tonight -n observatory observatory.liken.sh/retry=1
```

The operator removes the annotation when it starts the step. To give
up on a failed activation instead, delete the reservation, and
deactivation runs from its first step.

A trigger's run that failed does not run again for the same change of
its condition. The same annotation on the resource runs it again, if
the condition still holds:

```sh
kubectl annotate dome lab -n observatory observatory.liken.sh/retry=1
```

## A delete does not finish

A resource that the operator runs something for carries the finalizer
`observatory.liken.sh/deactivate`, and its delete waits for the
operator to stop it. A deleted device first runs its deactivation, so a
dust cap closes and a dome parks. A deleted telescope ends its
reservation first. That can take minutes, and it is expected.

If the operator is not running, the delete waits until it returns.
To let a resource go at once, remove the finalizer by hand:

```sh
kubectl patch dustcap east -n observatory --type=merge -p '{"metadata":{"finalizers":null}}'
```

That skips the device's deactivation, so its equipment stays as it is.
[Deleting a running resource](https://liken.sh/observatory/docs/reference/how-a-reservation-runs/#deleting-a-running-resource)
explains what happens to each kind.
