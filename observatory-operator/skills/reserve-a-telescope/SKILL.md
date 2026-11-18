---
name: reserve-a-telescope
description: "Reserve a telescope for a night with a Reservation, watch it start the equipment, connect KStars and Ekos to its INDI server, expose the server and PHD2 outside the cluster, and end the night safely. Use when someone wants to observe, when KStars cannot reach the telescope, or when deciding how to expose INDI from a cluster."
---

This skill is the guide at https://liken.sh/observatory/docs/guides/reserve-a-telescope/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Reserve a telescope

A `Reservation` gives one person, or one program, the use of one
telescope for a time. While it is active, the operator runs the
telescope's equipment. When it ends, the operator parks and powers
everything down. Here you reserve a telescope, connect KStars to it,
and end the reservation. You need a `Telescope` and its equipment, as
[Describe your equipment](https://liken.sh/observatory/docs/guides/describe-your-equipment/)
shows.

## Write the reservation

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Reservation
metadata:
  name: east-tonight
spec:
  telescope: east
  holder: desktop
  start: 2026-10-07T20:00:00-04:00
  end: 2026-10-08T05:00:00-04:00
```

* `telescope` is the name of the `Telescope`. You cannot change it
  after you create the reservation.
* `holder` is free text that says who uses the telescope. The operator
  shows it in messages, such as `In use by desktop`. When the holder is
  `astrophotography-operator`, that operator uses the telescope to
  image your targets, as
  [Image a target](https://liken.sh/astrophotography/docs/guides/image-a-target/)
  describes.
* `start` and `end` are RFC 3339 times. With no `start`, activation
  begins as soon as you create the reservation. With no `end`, the
  reservation lasts until you delete it.
* `outbox` is optional. Its `claimName` is a `PersistentVolumeClaim`
  in the reservation's namespace, and the telescope's imaging cameras
  save each frame to it, as
  [The outbox](https://liken.sh/observatory/docs/concepts/how-a-reservation-runs/#the-outbox)
  describes. The `PowerOn` step fails when the claim does not exist.
  Every imaging camera's pod mounts the claim, and so does the worker
  of `astrophotography-operator` when it is the holder. So do not use
  `ReadWriteOncePod`, which lets only one pod mount the volume. A
  `ReadWriteOnce` volume works only when all those pods run on the
  node where it is bound.
* `fitsKeywords` is optional. It lists FITS keywords, such as
  `OBJECT`, for the header of every frame of the reservation. Its
  values replace the keywords of the same names on the `Observatory`,
  the `Telescope`, and the `Camera`, as
  [FITS keywords](https://liken.sh/observatory/docs/concepts/fits-keywords/) describes.

A telescope serves one reservation at a time. A second reservation of
the same telescope stays in its `Wait` step, and its message names the
reservation it waits for. Waiting reservations get the telescope in
the order of their `start` times. Two telescopes in one observatory
can each have an active reservation, and they share the observatory's
dome and weather station.

## Watch it start

```sh
kubectl apply -n observatory -f east-tonight.yaml
kubectl get rsv -n observatory -w
```

The reservation is `Scheduled` until its start time, then
`Activating` while the operator works through its steps, and then
`Ready`. Each line of the watch shows the current step and what it
waits for, such as a camera that is still cooling. In order, the
operator:

1. starts the observatory's INDI server and its devices, unless
   another reservation already started them
2. waits for the conditions in the telescope's `spec.requires`, such
   as a person's confirmation that a portable telescope is
   [set up](https://liken.sh/observatory/docs/guides/set-up-a-portable-telescope/)
3. starts the telescope's INDI server, and switches on the power
   outputs that its devices' `power` fields refer to
4. starts a pod for each device, and waits for each driver to appear
   on the server
5. connects each device
6. writes to the drivers the settings in each device's spec, the
   site's location, the optics, the outbox, and the FITS keywords
7. runs the activation procedures that you declared, such as
   unparking the dome and cooling the camera
8. starts PHD2, if the telescope has a `Guider`

[How a reservation runs](https://liken.sh/observatory/docs/concepts/how-a-reservation-runs/)
gives each step's exact behavior and deadline. To follow one step:

```sh
kubectl describe reservation east-tonight -n observatory
```

## Connect KStars

When the reservation is `Ready`, `status.endpoint` has the address of
the telescope's INDI server, such as
`east-telescope.observatory.svc:7624`:

```sh
kubectl get rsv east-tonight -n observatory -o jsonpath='{.status.endpoint.host}:{.status.endpoint.port}'
```

Point an Ekos profile at that host and port, in remote mode. From a
desktop outside the cluster, use one of the ways in the next section.
Ekos lists each device by its INDI name, which `status.indiDevice`
on each device resource shows.

While the reservation is `Ready`, you drive the telescope from KStars.
The operator acts on the equipment only in these cases:

* When a device's pod is deleted, it starts the pod again and
  connects the device when its driver is back. A device that you
  disconnect in KStars stays disconnected.
* It runs the triggers you declared, such as parking the dome when the
  weather station reports unsafe weather.
* It sends the park state of the dome to the mounts, and the park
  state of each mount to the dome, so the drivers' park locks work. A
  mount refuses to unpark while the dome is parked, and the dome
  refuses to park while a mount is unparked.

## Reach the telescope from outside the cluster

The operator creates a `ClusterIP` `Service` for each INDI server and
for each guider, and leaves it to you to reach them from your desktop:
a port forward, a `LoadBalancer`, a VPN, or a mesh such as Tailscale.

INDI has no authentication and no encryption. Any client that reaches
port 7624 can slew the mount, and any client that reaches port 4400
can command PHD2. Do not expose either port to a network you do not
trust.

The simplest way is a port forward from the desktop, which needs
nothing in the cluster:

```sh
kubectl port-forward -n observatory svc/east-telescope 7624
kubectl port-forward -n observatory svc/east-guider 4400
```

For anything longer-lived, write your own `Service` instead of editing
the operator's. The operator deletes its `Service`s when the
reservation ends and creates them again for the next one, so an edit
to one of them is lost. Your own `Service` selects the operator's pods
by their labels and stays between reservations. These expose the
`east` telescope's server and its guider on a load balancer:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: east-remote
  namespace: observatory
spec:
  type: LoadBalancer
  selector:
    observatory.liken.sh/kind: Telescope
    observatory.liken.sh/resource: east
  ports:
    - name: indi
      port: 7624
---
apiVersion: v1
kind: Service
metadata:
  name: east-guider-remote
  namespace: observatory
spec:
  type: LoadBalancer
  selector:
    observatory.liken.sh/kind: Guider
    observatory.liken.sh/resource: east
  ports:
    - name: phd2
      port: 4400
```

Between reservations, no pod matches, so the `Service` has no
endpoints, and a client cannot connect. The labels, ports, and names
the operator uses are in
[Objects the operator creates](https://liken.sh/observatory/docs/reference/objects-the-operator-creates/).

The operator writes no `NetworkPolicy`, because a policy of its own
would block the path you chose. If your cluster enforces policies,
allow your path to the server and guider pods, and allow traffic
between the pods of the `observatory` namespace, which the drivers,
the servers, and PHD2 need.

## End the night

The reservation ends at its `end` time, or when you delete it:

```sh
kubectl delete reservation east-tonight -n observatory
```

Deactivation runs the activation steps in reverse. The operator stops
PHD2's guiding and any exposure, and runs your deactivation
procedures, such as closing the dust cap, warming the camera, and
parking the mount and the dome. Then it disconnects the devices, stops
their pods, and switches their power off.

The delete completes after deactivation is done. A reservation that
reaches its `end` time stays in the phase `Released` until you delete
it. When the condition `SafeToPowerOff` is `True`, you can switch the
equipment off by hand:

```sh
kubectl wait --for=condition=SafeToPowerOff reservation/east-tonight -n observatory --timeout=30m
```

If a step fails, the reservation is `Failed`, and the message of its
`Ready` condition names the step and the device.
[Troubleshoot](https://liken.sh/observatory/docs/guides/troubleshoot/) explains what to do next.
