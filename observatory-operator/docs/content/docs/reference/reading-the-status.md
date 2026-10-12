---
title: Reading the status
weight: 20
---

# Reading the status

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
`NoParent`. A device that a session holds where its spec no longer
places it has `ParentFound` `True` with the reason `MovePending`, and
the message says where it goes when the session ends, such as `Moves
to OpticalTrain west-imaging when Reservation east-tonight ends`.

## Devices

A device's status has the same fields in every kind, and a typed field
for each standard property of the kind's INDI interface. Each value is
in one place. One INDI device can serve several resources, such as a
camera and the `FilterWheel` that the camera's driver serves, and each
of its properties is in the status of one resource, so an update to
the wheel's slot writes the `FilterWheel` alone.

- `phase`: `Inventory` on the shelf, with a `Ready` message such as
  `Not installed in an optical train`. `Idle` when the device is
  installed and no reservation of its telescope or its observatory is
  active, with the message `Not reserved`. A device whose server runs
  for a session and does not run the device is `Idle` too, with a
  message such as `Starts at the next reservation, after Reservation
  east-tonight ends`: a device added during the session, and a device
  that the release stopped, until the server stops. From the start of
  a reservation's activation, `Starting` until the pod exists, then
  `Connecting`, `Connected`, and `Disconnecting`, or `Error`. During
  a release, a device is `Disconnecting` from its disconnect until its
  pod is gone, while the operator stops its driver or its server. A
  `Telescope`, an `Observatory`, and a `Guider` are `Idle` with no
  active reservation, then `Activating`, `Ready`, and `Deactivating`,
  or `Error`.
- `indiDevice` and `endpoint`: the INDI device and the server that
  serve the resource. A client reaches the resource there, a served
  one too, with no need to follow `spec.device`. `driver`, `image`,
  `pod`, and `node`: what runs, and where, for the resource that runs
  the driver. `indiDevice` is the name that KStars shows. The status
  says what is true now, so `pod` and `node` are present only while
  the pod of the driver runs, `endpoint` only while that pod runs and
  is not stopping, and `indiDevice` only while the driver defines the
  device on its server. The driver stops on its server before its pod
  is deleted, so a stopping pod has no driver to reach. `endpoint`
  names the server in the pod's `observatory.liken.sh/server` label,
  so a device that a person moved during a session names the server
  that runs it until the session ends. When a reservation ends, a
  person deletes the device, or the pod of the resource that serves it
  stops, the operator removes these fields. An `Idle` device and an
  `Inventory` device have none of them.
- The typed fields: one for each standard property of the kind's
  interface that the driver defines, such as `status.temperature` and
  `status.gain` of a `Camera`, or `status.slot` and `status.filters`
  of a `FilterWheel`. Each holds the property's state and its values.
  `kubectl explain` describes each field, its property, and its unit.
  Where the driver chooses the elements, such as a mount's slew rates
  or a wheel's filter names, the field lists them.
- `limits`: the range that the driver gives each number of the typed
  fields that a client can write, by INDI property and member, such as
  `status.limits.CCD_TEMPERATURE.CCD_TEMPERATURE_VALUE` with its `min`
  and `max`.
- `readings`: on a `Mount` and a `FilterWheel` only, a value that the
  operator derives from several properties and that no typed field
  holds: what the mount does, in `status.readings.state`, and the name
  of the filter in the wheel's slot, in `status.readings.filter`.
- `display`: values of the typed fields as text with their units, such
  as `-9.8 °C`, for the printer columns. A program reads the typed
  fields, and `kubectl get` shows `display`. INDI gives a flat panel's
  brightness no unit, so it shows against the maximum in `limits`,
  such as `128 of 255`. A `Switch` shows the numbers of its outputs
  that are on, such as `1, 2, 3`, because a printer column shows only
  the first value of a JSONPath that matches several.
- `properties`: on the resource that runs the driver, each INDI
  property that no typed field holds, with its label, group, type,
  permission, state, switch rule, and the value and limits of each
  member. A vendor's own properties are here, and so are the
  properties that every INDI device has, such as `DEBUG`. A standard
  property of an interface that no resource on the device is stays
  here too, such as the `FILTER_SLOT` of a camera with a wheel and no
  `FilterWheel`. The list holds no BLOB data.

```sh
kubectl get camera east-main -n observatory -o jsonpath='{.status.gain.value}'
kubectl get camera east-main -n observatory \
  -o jsonpath='{.status.limits.CCD_TEMPERATURE.CCD_TEMPERATURE_VALUE}'
kubectl get camera east-main -n observatory \
  -o jsonpath='{.status.properties[*].name}'
```

## Device states

A device is in one state at a time, and the printer columns name it in
one word. A mount's state comes from three properties, so it is a
reading. Each other state is the reason of a state condition (below),
or the camera's display:

| Kind | Column | Values | From |
|---|---|---|---|
| `Mount` | `State`, `status.readings.state` | `Parked`, `Parking`, `Unparking`, `Stopped`, `Slewing`, `Tracking` | `TELESCOPE_PARK`, `TELESCOPE_TRACK_STATE`, and the state of `EQUATORIAL_EOD_COORD` |
| `Dome` | `Park`, the reason of `Parked` | `Parked`, `Unparked`, `Moving` | `DOME_PARK` |
| `Dome` | `Shutter`, the reason of `Open` | `Open`, `Closed`, `Moving` | `DOME_SHUTTER` |
| `DustCap` | `Cover`, the reason of `Open` | `Open`, `Closed`, `Moving` | `CAP_PARK` |
| `Camera` | `Exposure`, `status.display.exposure` | the time left, such as `12 s`, while an exposure runs, then `Done` or `Failed`, and `Idle` before the first | `CCD_EXPOSURE` |
| `FlatPanel` | `Light`, the reason of `Lit` | `LightOn`, `LightOff` | `FLAT_LIGHT_CONTROL` |
| `WeatherStation` | `Safety`, the reason of `Safe` | `Safe`, `Warning`, `Danger` | `SAFETY_STATUS` |

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
with no safety status, gives `Unknown` with the reason `NotReported`. A
device that is not connected has none of these conditions, so a
trigger never reads a state that the operator cannot confirm.

## Telescopes and observatories

A `Telescope`'s status names its INDI server in `status.server`, its
active reservation, its tubes, its trains with their devices, its own
devices, and its guider's phase and PHD2's state. Its `Guider` column,
in `-o wide`, shows both, such as `Ready, Guiding`, and an empty cell
for a telescope with no `Guider`. An `Observatory`'s status
names its server, its telescopes, its devices, the active reservations,
and the worst safety status of its weather stations. A `Telescope` and an
`Observatory` also report the condition `Active`, and each resource
with procedures reports their runs in `status.procedures`, as
[Procedures](/docs/concepts/procedures/) states.

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
`Released`. [How a reservation runs](/docs/concepts/how-a-reservation-runs/) states what each step does.

`status.session` records what the session started with: the
telescope's `observatory`, the devices of the observatory's server in
`site`, and the `devices`, `trains`, `tubes`, and `guiders` of the
telescope's server. Each device is under its kind and name, such as
`Focuser/east`. `StartSite` and `PowerOn` write it once, and the
operator reads it in place of the specs until the reservation is
`Released`. Compare it with a spec to see which edit waits for the
next session:

```sh
kubectl get rsv east-tonight -n observatory \
  -o jsonpath='{.status.session.devices.Focuser/east.power}'
```

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
guider](/docs/concepts/the-guider/) states what a `Guider`'s status holds.
