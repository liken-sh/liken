---
title: Reading the status
weight: 20
---

# Reading the status

`observatory-operator` writes the status of every device, telescope,
observatory, and reservation. The one exception is the `SetUp`
condition, which a person writes, as [Conditions](#conditions)
describes.

## Conditions

Every kind except `Horizon` has `status.conditions`, in the shape of
`metav1.Condition`, and `status.observedGeneration`. A `Horizon` has no
status, because the operator reads nothing from it.

`ParentFound` is `False` when a resource that the spec names does not
exist, and its message names the missing resource. A device that names
no parent, such as a device that is not in an optical train, has
nothing to find, so its `ParentFound` is `True` with the reason
`NoParent`. When you move a device while a session uses it at its old
place, `ParentFound` is `True` with the reason `MovePending`. The message says where the device goes when the session
ends, such as `Moves to OpticalTrain west-imaging when Reservation
east-tonight ends`.

A `Telescope` that sets `spec.horizon` has the condition
`HorizonFound`. It is `True` with the message `Found Horizon yard` when
that `Horizon` exists. It is `False` with the reason `HorizonMissing`
and the message `Missing Horizon yard` when it does not. A missing
horizon changes neither the telescope's phase nor its `Ready`
condition. A telescope with no `spec.horizon` has no `HorizonFound`
condition.

`SetUp` is the only condition that a person writes. You write it through
the status subresource, for example with `kubectl liken observatory
set-up`. It is `True` with the reason `SetUp` and a message such as
`Set up by ada`, or `False` with the reason `TakenDown`. The operator
keeps the condition as you wrote it until it expires. It expires at
`status.setUpUntil` when you wrote one, and otherwise at the first
astronomical dawn after you set it, at the location of the resource's
`Observatory`. Then the operator sets it `False` with the reason
`Expired`. While `SetUp` is `True`, the operator writes the time it
expires to `status.setUpExpiryTime`, which the `SET UP UNTIL` column of
`kubectl get` shows.

`AttentionNeeded` is `True` with the reason `Notified` while the
request of a `notify` action is open. It becomes `False` with the
reason `Activated` when the next activation answers the request, or
with the reason `Acknowledged` when a person's `SetUp` answers it. The
`Acknowledged` message repeats the message of that `SetUp`, such as
`Taken down by ada`. [Set up a portable
telescope](/docs/guides/set-up-a-portable-telescope/) uses both
conditions.

## Devices

Every device kind has the same status fields, plus a typed field for
each standard property of the kind's INDI interface. One INDI device
can serve several resources, such as a camera and the `FilterWheel`
that the camera's driver serves, but each INDI property appears in the
status of only one of them. The wheel's slot, for example, is in the
status of the `FilterWheel` and not of the `Camera`.

- `phase`: where the device is in its life cycle.
  - `Inventory` when the device is not installed in an optical train.
    Its `Ready` message is `Not installed in an optical train`.
  - `Idle` when the device is installed and no reservation of its
    telescope or its observatory is active. The message is `Not
    reserved`. A device is also `Idle` while it waits to start on an
    INDI server that is running for a session. Its message is then
    such as `Starts at the next reservation, after Reservation
    east-tonight ends`. This happens to a device that someone added
    during the session, and to a device that the release stopped, until
    the server stops.
  - From the start of a reservation's activation, `Starting` until the
    pod exists, then `Connecting`, `Connected`, and `Disconnecting`, or
    `Error`. When a reservation ends, the device is `Disconnecting`
    until the operator has stopped its driver and deleted its pod.
  - A `Telescope`, an `Observatory`, and a `Guider` are `Idle` while no
    reservation is active, then `Activating`, `Ready`, and
    `Deactivating`, or `Error`.
- `indiDevice` and `endpoint`: the INDI device that serves the
  resource, and the INDI server it is on. A client reaches the resource
  there, including a resource that another resource's driver serves, so
  it does not need to follow `spec.device`. `indiDevice` is the name
  that KStars shows. The resource that runs the driver also has
  `driver`, `image`, `pod`, and `node`, which say what runs and where.
  Each of these fields is present only while it is true:
  - `pod` and `node` are present while the driver's pod runs.
  - `endpoint` is present while that pod runs and is not stopping. It
    is the server named by the pod's `observatory.liken.sh/server`
    label, so a device that a person moved during a session keeps the
    server that runs it until the session ends.
  - `indiDevice` is present while the driver defines the device on its
    server.

  The operator stops the driver before it deletes the pod, so a client
  cannot reach the device through a pod that is stopping. An `Idle`
  device and an `Inventory` device have none of these fields.
- The typed fields: one for each standard property of the kind's
  interface that the driver defines, such as `status.temperature` and
  `status.gain` of a `Camera`, or `status.slot` and `status.filters` of
  a `FilterWheel`. Each holds the property's state and values.
  `kubectl explain` describes each field, its property, and its unit.
  When the driver sets the elements of a property, such as a mount's
  slew rates or a wheel's filter names, the field lists them.
- `limits`: the range that the driver sets for each writable number in
  the typed fields, by INDI property and member, such as
  `status.limits.CCD_TEMPERATURE.CCD_TEMPERATURE_VALUE` with its `min`
  and `max`.
- `readings`: on a `Mount` and a `FilterWheel` only, values that the
  operator derives from several properties and that no typed field
  holds. `status.readings.state` is what the mount does.
  `status.readings.filter` is the name of the filter in the wheel's
  slot.
- `display`: the values of the typed fields as text with their units,
  such as `-9.8 °C`, for the printer columns. A program reads the typed
  fields, and `kubectl get` shows `display`. INDI gives a flat panel's
  brightness no unit, so `display` shows it against the maximum in
  `limits`, such as `128 of 255`. For a `Switch`, `display` lists the
  numbers of the outputs that are on, such as `1, 2, 3`, because a
  printer column shows only the first value of a JSONPath that matches
  several.
- `properties`: on the resource that runs the driver, each INDI
  property that no typed field holds, with its label, group, type,
  permission, state, switch rule, and the value and limits of each
  member. It lists a vendor's own properties and the properties that
  every INDI device has, such as `DEBUG`. It also lists a standard
  property of an interface that no resource on the device implements,
  such as the `FILTER_SLOT` of a camera that has a wheel but no
  `FilterWheel`. The list has no BLOB data.

```sh
kubectl get camera east-main -n observatory -o jsonpath='{.status.gain.value}'
kubectl get camera east-main -n observatory \
  -o jsonpath='{.status.limits.CCD_TEMPERATURE.CCD_TEMPERATURE_VALUE}'
kubectl get camera east-main -n observatory \
  -o jsonpath='{.status.properties[*].name}'
```

## Device states

The printer columns of `kubectl get` show a device's state in one
word. A mount's state is in `status.readings`, because the operator
derives it from three properties. A camera's state is in `display`.
Each other state is the reason of a state condition, which
[State conditions](#state-conditions) describes:

| Kind | Column | Values | From |
|---|---|---|---|
| `Mount` | `State`, `status.readings.state` | `Parked`, `Parking`, `Unparking`, `Stopped`, `Slewing`, `Tracking` | `TELESCOPE_PARK`, `TELESCOPE_TRACK_STATE`, and the state of `EQUATORIAL_EOD_COORD` |
| `Dome` | `Park`, the reason of `Parked` | `Parked`, `Unparked`, `Moving` | `DOME_PARK` |
| `Dome` | `Shutter`, the reason of `Open` | `Open`, `Closed`, `Moving` | `DOME_SHUTTER` |
| `DustCap` | `Cover`, the reason of `Open` | `Open`, `Closed`, `Moving` | `CAP_PARK` |
| `Camera` | `Exposure`, `status.display.exposure` | the time left, such as `12 s`, while an exposure runs, then `Done` or `Failed`, and `Idle` before the first | `CCD_EXPOSURE` |
| `FlatPanel` | `Light`, the reason of `Lit` | `LightOn`, `LightOff` | `FLAT_LIGHT_CONTROL` |
| `WeatherStation` | `Safety`, the reason of `Safe` | `Safe`, `Warning`, `Danger` | `SAFETY_STATUS` |

The operator derives a mount's state from the first rule below that
applies:

1. When the park property is `Busy`, the state is `Parking` or
   `Unparking`, toward the switch that is on.
2. When `PARK` is on, the state is `Parked`, unless the park property's
   state is `Alert`. A park that failed or that a client aborted does
   not show where the mount is.
3. When `EQUATORIAL_EOD_COORD` is `Busy`, the state is `Slewing`. So a
   mount that slews and then tracks reads `Slewing` until the
   coordinates settle.
4. When `TRACK_ON` is on, the state is `Tracking`.
5. Otherwise the state is `Stopped`.

A dome that is `Moving` is turning to or from its park position.

## State conditions

While a device is `Connected`, its conditions also report its state,
for a trigger or for `kubectl wait --for=condition=Parked`. Each
condition comes from the property in the table. A driver that does not
define the property has no such condition:

| Kind | Condition | `True` when | Property |
|---|---|---|---|
| `Dome` | `Parked` | the dome is parked | `DOME_PARK` |
| `Dome` | `Open` | the shutter is open | `DOME_SHUTTER` |
| `Mount` | `Parked` | the mount is parked | `TELESCOPE_PARK` |
| `DustCap` | `Open` | the cover is open | `CAP_PARK` |
| `FlatPanel` | `Lit` | the light is on | `FLAT_LIGHT_CONTROL` |
| `Camera` | `Cooling` | the cooler is on | `CCD_COOLER` |
| `WeatherStation` | `Safe` | the station reports `Safe` | `SAFETY_STATUS` |

The reason is the state, such as `Parked` or `Unparked`, and the
message names the device, such as `Dome lab is parked`. While a park,
a shutter, or a cover moves, the condition is `Unknown` with the reason
`Moving`. `Safe` is `False` with the reason `Warning` or `Danger`. When
a driver reports neither side of a switch, or a station has no safety
status, the condition is `Unknown` with the reason `NotReported`. A
device that is not connected has none of these conditions, so a trigger
never acts on a state that the operator cannot confirm.

## Telescopes and observatories

A `Telescope`'s status gives its INDI server in `status.server`, its
active reservation, its tubes, its optical trains and their devices,
its own devices, and the phase of its guider and the state of PHD2. The
`Guider` column, in `-o wide`, shows the last two, such as `Ready,
Guiding`. The cell is empty for a telescope with no `Guider`. An
`Observatory`'s status gives its server, its telescopes, its devices,
its active reservations, and the worst safety status of its weather
stations. A `Telescope` and an `Observatory` also have the condition
`Active`. A resource with procedures records their runs in
`status.procedures`, as [Procedures](/docs/concepts/procedures/)
describes.

## Reservations

A `Reservation` moves through the phases `Scheduled`, `Activating`,
`Ready`, `Deactivating`, and `Released`, or ends in `Failed`.
`status.steps` lists the steps in order, with each step's state, start
and stop times, and summary. The summary names the device that the
step waits for. The list holds the activation steps from the start.
The deactivation steps are added when deactivation begins, so a
`Ready` reservation lists only the steps that ran. A step's state is
`Pending`, `Running`, `Done`, `Failed`, or `Skipped`. `status.step` is
the step that runs now or the step that failed, and it is empty while
the reservation is `Ready` or `Released`. [How a reservation
runs](/docs/concepts/how-a-reservation-runs/) describes what each step
does.

`status.session` records the state the session started with: the
telescope's `observatory`, the devices of the observatory's server in
`site`, and the `devices`, `trains`, `tubes`, and `guiders` of the
telescope's server. Each device is under its kind and name, such as
`Focuser/east`. The `StartSite` and `PowerOn` steps write it once. When
you retry a failed activation step, the operator writes it again from
the current specs. Until the reservation is `Released`, the operator
reads the session record and ignores later edits to the specs. So you
can compare the record with a spec to see which edits wait for the next
session:

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
phase, the step that runs, and the message of the `Ready` condition.
While a step runs, that condition's reason is the step's name and its
message is the step's summary, such as the device the step waits for.
`-w` also prints a line when the metadata changes, such as when the
operator adds its finalizer or a person deletes the reservation, and
that line repeats the one before it. `-o wide` adds the start, the end,
and the endpoint.

`kubectl describe` lists each step with its name first. It also lists
the reservation's Events: one for each step that ends, with the time
the step took, one for each phase, and a `Warning` for a step that
fails. The `Ready` condition is `True` while the phase is `Ready`. Its
message and `status.endpoint` then give the host and port for KStars,
such as `east-telescope.observatory.svc:7624`.

The operator's log has one line for each phase change and for the
start and end of each step. Each line names the namespace after the
operator's name:

```text
observatory-operator: observatory: Reservation east-tonight: Activating
observatory-operator: observatory: Reservation east-tonight: Activation started
observatory-operator: observatory: Reservation east-tonight: Activation done in 19 s: ran the activation of Dome lab, Mount east, Camera east-main, DustCap east
observatory-operator: observatory: Reservation east-tonight: Ready at east-telescope.observatory.svc:7624
```

`SafeToPowerOff` is `True` when the deactivation steps are done. Until
then it is `False`, and its message gives the reason, such as `In use
by desktop` while the reservation is `Ready`.

`EditsPending` is `True` while an edit of the outbox or of a FITS
keyword waits for the next session. The reservation has this condition from the
moment `PowerOn` records the session until the reservation is
`Released`. It is `False` with the reason `NoEditPending` while
`status.session` holds the same outbox and merged keywords that the
specs give now. It is `True` with the reason `EditPending` when an edit
of `spec.outbox` changes what the next session records. An edit of
`spec.fitsKeywords` on the `Observatory`, the `Telescope`, a `Camera`,
or the `Reservation` does the same. The message lists each change with
its next value, separated by `;`:

```text
spec.outbox.claimName changes from frames to nightly at the next session; FITS keyword OBJECT of Camera east-main changes from "M31" to "M33" at the next session
```

When a keyword's value is the same and only its comment changed, the
message reads `the comment of FITS keyword TELESCOP of Camera east-main
changes from ...`. A value that the next session adds or removes reads
`none` on that side. Each change of the condition's status or reason
posts one Event with the same reason and message. A second edit while
one is already waiting changes neither, so it posts no Event. When you retry a failed activation
step, the operator records the session again from the specs, and the
condition turns `False`. A device that was moved or added does not show
here. Its own `ParentFound` or `Ready` condition shows that it waits.
[The guider](/docs/concepts/the-guider/) describes the status of a
`Guider`.
