---
title: Reading the status
weight: 20
---

The operator writes every status, and no person writes one. This page
explains the status of the devices, the telescopes and observatories,
and the reservations.

## Conditions

Each kind
has `status.conditions` in the shape of `metav1.Condition`, and
`status.observedGeneration`. `ParentFound` is `False` when a resource
that the spec names does not exist; the resource stays in place, and
the message names the missing parent. A device on the shelf has no
parent to find, so its `ParentFound` is `True` with the reason
`NoParent`.

## Devices

A device's status has the same fields in every kind, and one
`readings` block of its own:

- `phase`: `Inventory` on the shelf, with a `Ready` message such as
  `Not installed in an optical train`. `Idle` when the device is
  installed and no reservation of its telescope or its observatory is
  active, with the message `Not reserved`. From the start of a
  reservation's activation, `Starting` until the pod exists, then
  `Connecting`, `Connected`, and `Disconnecting`, or `Error`. During
  a release, a device is `Disconnecting` from its disconnect until its
  pod is gone, while the operator stops its driver or its server. A
  `Telescope`, an `Observatory`, and a `Guider` are `Idle` with no
  active reservation, then `Activating`, `Ready`, and `Deactivating`,
  or `Error`.
- `indiDevice`, `driver`, `image`, `pod`, and `node`: what runs, and
  where. `indiDevice` is the name that KStars shows.
- `readings`: the typed values of the kind, such as
  `status.readings.temperature` of a `Camera`. A reading is absent
  until the driver sends it. Each number is in the unit that its
  description in `kubectl explain` states.
- `display`: the same values as text with their units, such as
  `-9.8 °C`, for the printer columns. A program reads `readings`, and
  `kubectl get` shows `display`. INDI gives a flat panel's brightness
  no unit, so it shows against the driver's maximum, such as
  `128 of 255`.
- `properties`: every INDI property that the device defines, with its
  label, group, type, permission, state, switch rule, and the value and
  limits of each member. A vendor's own properties are here. The list
  holds no BLOB data.

## Device states

A reading that holds a state names it in one word, because a device
is in one state at a time:

| Kind | Reading | Values | From |
|---|---|---|---|
| `Mount` | `state` | `Parked`, `Parking`, `Unparking`, `Stopped`, `Slewing`, `Tracking` | `TELESCOPE_PARK`, `TELESCOPE_TRACK_STATE`, and the state of `EQUATORIAL_EOD_COORD` |
| `Dome` | `park` | `Parked`, `Unparked`, `Moving` | `DOME_PARK` |
| `Dome` | `shutter` | `Open`, `Closed`, `Moving` | `DOME_SHUTTER` |
| `DustCap` | `cover` | `Open`, `Closed`, `Moving` | `CAP_PARK` |
| `Camera` | `exposure` | `Idle`, `Exposing`, `Done`, `Failed` | the state of `CCD_EXPOSURE` |
| `FlatPanel` | `light` | `Lit`, `Dark` | `FLAT_LIGHT_CONTROL` |

A mount reads the first state that applies, in this order. A `Busy`
park is `Parking` or `Unparking`, toward the switch that is on. `PARK`
on is `Parked`, unless the park's state is `Alert`: a park that failed
or that a client aborted does not say where the mount is. A `Busy`
`EQUATORIAL_EOD_COORD` is `Slewing`, so a slew that ends in tracking
reads `Slewing` until the coordinates settle. `TRACK_ON` on is
`Tracking`, and a mount that does none of these is `Stopped`. A
`Moving` dome turns to or from its park position.

## State conditions

While a device is `Connected`, its conditions also give its state, for
a trigger or for `kubectl wait --for=condition=Parked`. Each one comes
from the property in the table, and a driver that does not define the
property gives no condition:

| Kind | Condition | `True` when | Property |
|---|---|---|---|
| `Dome` | `Parked` | the dome is parked | `DOME_PARK` |
| `Dome` | `Open` | the shutter is open | `DOME_SHUTTER` |
| `Mount` | `Parked` | the mount is parked | `TELESCOPE_PARK` |
| `DustCap` | `Open` | the cover is open | `CAP_PARK` |
| `FlatPanel` | `Lit` | the light is on | `FLAT_LIGHT_CONTROL` |
| `Camera` | `Cooling` | the cooler is on | `CCD_COOLER` |
| `WeatherStation` | `Safe` | the station reports `Safe` | `SAFETY_STATUS` |

The reason names the state, such as `Parked` or `Unparked`, and the
message names the device, such as `Dome lab is parked`. A park, a
shutter, or a cover that moves is `Unknown` with the reason `Moving`
until the move ends. `Safe` is `False` with the reason `Warning` or
`Danger`. A driver that reports neither side of a switch, or a station
with no verdict, gives `Unknown` with the reason `NotReported`. A
device that is not connected has none of these conditions, so a
trigger never reads a state that the operator cannot confirm.

## Telescopes and observatories

A `Telescope`'s status names its INDI server in `status.server`, its
active reservation, its tubes, its trains with their devices, its own
devices, and its guider's phase and PHD2's state. Its `Guider` column,
in `-o wide`, shows both, such as `Ready, Guiding`, and an empty cell
for a telescope with no `Guider`. An `Observatory`'s status
names its server, its telescopes, its devices, the active reservations,
and the worst verdict of its weather stations. A `Telescope` and an
`Observatory` also report the condition `Active`, and each resource
with procedures reports their runs in `status.procedures`, as
[Procedures](/docs/reference/procedures/) states.

## Reservations

A `Reservation` moves through the phases `Scheduled`, `Activating`,
`Ready`, `Deactivating`, and `Released`, or `Failed`. `status.steps`
lists the steps in order, with their states, start and stop times, and
summaries. A summary names the device that a step waits for. The list
holds the activation steps from the start, and deactivation adds its
steps when it begins, so a `Ready` reservation lists only what ran.
Each step's state is `Pending`, `Running`, `Done`, `Failed`, or
`Skipped`. `status.step` names the step that runs now, or the step
that failed, and it is empty while the reservation is `Ready` or
`Released`. [How a reservation runs](/docs/reference/how-a-reservation-runs/) states what each step does.

```sh
kubectl get rsv -n observatory -w
kubectl wait --for=condition=Ready reservation/east-tonight -n observatory --timeout=10m
kubectl describe reservation east-tonight -n observatory
kubectl get rsv east-tonight -n observatory \
  -o jsonpath='{.status.steps[?(@.name=="Connect")].summary}'
kubectl get cam,mnt,sw -n observatory
```

`kubectl get rsv -w` prints a line for each change of the status: the
phase, the step, and the message of the step that runs, such as the
device it waits for. It also prints a line for each change of the
metadata, which repeats the line before it: one when the operator adds
its finalizer, and one when a person deletes the reservation. `-o wide` adds the start, the end, and the
endpoint. The message is the `Ready` condition's: while a step runs,
its reason is the step and its message is the step's summary.
`kubectl describe` lists each step with its name first, and the
reservation's Events: one for each step that ends, with the time it
took, one for each phase, and a `Warning` for a step that fails. The
`Ready` condition is `True` while the phase is `Ready`, and its
message and `status.endpoint` then give the host and port for KStars,
such as `east-telescope.observatory.svc:7624`.

`kubectl logs` tells the same story as the status, one line for each
phase change and for each step's start and end:

```text
observatory-operator: Reservation east-tonight: Activating
observatory-operator: Reservation east-tonight: Activation started
observatory-operator: Reservation east-tonight: Activation done in 19 s: ran the activation of Dome lab, Mount east, Camera east-main, DustCap east
observatory-operator: Reservation east-tonight: Ready at east-telescope.observatory.svc:7624
```

`SafeToPowerOff` is `True` when the deactivation steps are done. Until
then it is `False`, and its message says why, such as
`In use by desktop` while the reservation is `Ready`. [The
guider](/docs/reference/the-guider/) states what a `Guider`'s status holds.
