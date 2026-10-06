# observatory-operator

`observatory-operator` is the hardware control layer of an observatory
on a [`liken`](https://liken.sh/) cluster, under the API group
`observatory.liken.sh`. A person describes the observatory's hardware
as resources, and a `Reservation` gives one holder the use of one
`Telescope`. While a reservation is active, the operator runs each of
the telescope's INDI devices in its own pod, serves them on one INDI
server, connects and configures each device in a fixed order, and
runs the procedures that each resource states for its activation. It
starts PHD2 for the telescope's `Guider` and connects it to the guide
camera and the mount. At the end it runs each resource's deactivation
procedures and reports that the devices are safe to power off. KStars, or
`astrophotography-operator`, drives the telescope through its server,
and guides through PHD2's event server.

The Go module `github.com/liken-sh/liken/observatory-operator` holds
the operator and four packages:

- [`observatory/`](observatory/) holds the 20 kinds of
  `observatory.liken.sh/v1alpha1` as Go types. The operator reads and
  writes them, and `astrophotography-operator` will import them to read
  a telescope's state and to create a `Reservation`.
- [`indi/`](indi/) is a client of the INDI protocol, version 1.7, in
  Go with no cgo. It keeps every device and property that a server
  defines, follows each update, sends changes that follow each
  property's definition, and waits for a device to answer.
- [`phd2/`](phd2/) is a client of PHD2's event server. It reads
  PHD2's state, calibration, equipment, pixel scale, and guide steps
  from the event stream, and sends `set_connected` and `stop_capture`.
  `phd2/phd2test` is a fake event server for tests.
- [`drivers/`](drivers/) maps each INDI driver to the image of the
  `indi` build that holds it, and names the images of the guider's pod.

[`deploy/`](deploy/) holds the namespace, the CRDs, the RBAC, and the
operator, and [`examples/simulators.yaml`](examples/simulators.yaml)
is an observatory of simulators with a device of every kind.
[`plans/00-design.md`](plans/00-design.md) is the design, and [root
plan 74](../plans/74-astrophotography.md) holds the architecture and
the tests behind it. `make test` runs every check CI runs.

## Running the operator

```sh
kubectl apply -k deploy/
kubectl apply -n observatory -f examples/simulators.yaml
kubectl get astro -n observatory
```

`deploy/` creates the namespace `observatory`, the CRDs, and the
operator: one `Deployment` with one replica, which watches the
resources of its own namespace. Every resource of the observatory
goes in that namespace. CI publishes the image
`ghcr.io/liken-sh/observatory-operator` and the kustomize base as the
OCI artifact `observatory-operator-deploy`, with the image's tag set
to the release.

The operator starts nothing for the inventory alone. The example ends
with the `Reservation` `east-tonight`, which starts the `east`
telescope at once and holds it until it is deleted.

## The resources

Every kind is namespaced, and every kind is in the category `astro`,
so `kubectl get astro` lists the whole observatory. Each resource names
its parent by name in its spec, so every reference points up the tree:

```
Observatory          the site: location, Dome, WeatherStation
 └─ Telescope        one Mount, one INDI server, one Guider
     ├─ OpticalTube  aperture and focal length; no driver
     └─ OpticalTrain one light path; names one OpticalTube
          Camera, FilterWheel, Focuser, Rotator, DustCap, FlatPanel
```

A device resource is inventory: it describes the hardware and its
settings, and the operator starts nothing for it. A `Reservation`
gives one holder the use of one `Telescope`, and the operator starts
the telescope's pods only while the reservation is active. [Plan
06](plans/completed/06-the-resources.md) gives the reasons for the
names and the tree.

Each device's parent field is optional. A device with no parent is on
the shelf: its spec describes it in full, with its driver, image,
power, and claim, but it is installed nowhere. The operator creates
no pod, no `Service`, and no `ResourceClaim` for it, because a claim
would reserve the hardware. A `SkyQualityMeter`, a `Switch`, or a
`Receiver` names at most one of `telescope` and `observatory`. To
install a device, set its parent field.

| Kind | Short name | Parent field | Key reading in `kubectl get` |
|---|---|---|---|
| `Observatory` | `obs` | none | `weather` |
| `Telescope` | `tel` | `observatory` | the server's host and port |
| `OpticalTube` | `ota` | `telescope` | `aperture`, `focalLength` in mm |
| `OpticalTrain` | `train` | `telescope` | its camera |
| `Mount` | `mnt` | `telescope` | RA as `19h17m21s`, Dec as `+12°34′56″`, parked, tracking |
| `GPS` | none | `telescope` | fix, time |
| `PolarAligner` | `pac` | `telescope` | adjustment state |
| `Camera` | `cam` | `opticalTrain` | temperature, setpoint, cooler `On` or `Off`, exposure |
| `FilterWheel` | `fw` | `opticalTrain` | slot, filter |
| `Focuser` | `foc` | `opticalTrain` | position |
| `Rotator` | `rot` | `opticalTrain` | angle |
| `DustCap` | `cap` | `opticalTrain` | `Open`, `Closed`, or `Moving` |
| `FlatPanel` | `flat` | `opticalTrain` | light, brightness |
| `Dome` | none | `observatory` | azimuth, shutter, parked |
| `WeatherStation` | `weather` | `observatory` | safety |
| `SkyQualityMeter` | `sqm` | `telescope` or `observatory` | brightness in mag/arcsec² |
| `Switch` | `sw` | `telescope` or `observatory` | the outputs that are on |
| `Receiver` | `rx` | `telescope` or `observatory` | frequency in MHz |
| `Guider` | none | `telescope` | phase, PHD2's state, RMS in arcsec |
| `Reservation` | `rsv` | `telescope` | phase, step, message |

`GPS`, `Dome`, and `Guider` have no short name, because the singular
is already short. `equipment-operator` also has a `Receiver` kind, so
name this one as `rx` or `receivers.observatory.liken.sh`. Each CRD
declares its parent fields as `selectableFields`, so
`kubectl get cam --field-selector spec.opticalTrain=east-imaging` lists
one train's cameras.

Every device kind shares these spec fields:

- `driver.name` is the INDI driver, such as `indi_simulator_ccd`. With
  no `driver.image`, the name must match `^indi_[a-z0-9_]+$`, and the
  operator runs the image that the `indi` build lists for it.
- `driver.image` is an image used as written. It must hold
  `/usr/bin/socat` and the driver on `PATH`, and run as user 1000 with
  a read-only root filesystem and a writable `/tmp`.
- `power` is `{switch, output}`: the `Switch` output that powers the
  device, from output 1. Activation switches it on before the device's
  pod starts, and deactivation switches it off after the pod stops.
- `claim` is a `ResourceClaimSpec` for real hardware. A simulator needs
  none.

The other spec fields are the few that activation needs:
`Observatory.spec.location`, the tube's
`aperture` and `focalLength` in millimeters, the camera's `gain` and
`offset`, the filter wheel's `filters`, the guider's `opticalTrain` and `pulses`, and the
reservation's `telescope`, `holder`, `start`, and `end`. [Plan
05](plans/05-the-property-schema.md) will generate typed fields for the
other standard properties. `kubectl explain` prints every field with
its unit.

## Reading the status

The operator writes every status, and no person writes one. Each kind
has `status.conditions` in the shape of `metav1.Condition`, and
`status.observedGeneration`. `ParentFound` is `False` when a resource
that the spec names does not exist; the resource stays in place, and
the message names the missing parent. A device on the shelf has no
parent to find, so its `ParentFound` is `True` with the reason
`NoParent`.

A device's status has the same fields in every kind, and one
`readings` block of its own:

- `phase`: `Inventory` on the shelf, with a `Ready` message such as
  `Not installed in an optical train`. `Idle` when the device is
  installed and no reservation of its telescope or its observatory is
  active, with the message `Not reserved`. From the start of a
  reservation's activation, `Starting` until the pod exists, then
  `Connecting`, `Connected`, and `Disconnecting`, or `Error`. A
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

While a device is `Connected`, its conditions also give its state, for
a trigger or for `kubectl wait --for=condition=Parked`. Each one comes
from the property in the table, and a driver that does not define the
property gives no condition:

| Kind | Condition | `True` when | Property |
|---|---|---|---|
| `Dome` | `Parked` | the dome is parked | `DOME_PARK` |
| `Dome` | `Open` | the shutter is open | `DOME_SHUTTER` |
| `Mount` | `Parked` | the mount is parked | `TELESCOPE_PARK` |
| `Mount` | `Tracking` | tracking is on | `TELESCOPE_TRACK_STATE` |
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

A `Telescope`'s status names its INDI server in `status.server`, its
active reservation, its tubes, its trains with their devices, its own
devices, and its guider's phase and PHD2's state. Its `Guider` column,
in `-o wide`, shows both, such as `Ready, Guiding`, and an empty cell
for a telescope with no `Guider`. An `Observatory`'s status
names its server, its telescopes, its devices, the active reservations,
and the worst verdict of its weather stations. A `Telescope` and an
`Observatory` also report the condition `Active`, and each resource
with procedures reports their runs in `status.procedures`, as
"Procedures" below states.

A `Reservation` moves through the phases `Scheduled`, `Activating`,
`Ready`, `Deactivating`, and `Released`, or `Failed`. `status.steps`
lists the steps in order, with their states, start and stop times, and
summaries. A summary names the device that a step waits for. The list
holds the activation steps from the start, and deactivation adds its
steps when it begins, so a `Ready` reservation lists only what ran.
Each step's state is `Pending`, `Running`, `Done`, `Failed`, or
`Skipped`. `status.step` names the step that runs now, or the step
that failed, and it is empty while the reservation is `Ready` or
`Released`. "How a reservation runs" below states what each step does.

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
`In use by desktop` while the reservation is `Ready`. "The guider"
below states what a `Guider`'s status holds.

## How a reservation runs

The operator runs one step at a time, in a fixed order, and a step
starts only when the step before it is `Done` or `Skipped`. Each step
reads what the cluster and the devices report before it changes
anything, and records what it did in its summary. A step with nothing
to do is `Skipped`. A device whose driver lacks a property, such as a
guide camera with no cooler, is named in the summary, and the step
goes on.

| Step | What it does | Deadline |
|---|---|---|
| `Wait` | Waits for `spec.start`, and for no other reservation to hold the telescope. | none |
| `StartSite` | Starts the observatory's server and its devices, connects them, and writes the dome's `DOME_SHUTTER_PARK_POLICY` and its `MOUNT_POLICY`. Another reservation in the observatory may have started them already. | 10 min |
| `PowerOn` | Starts the telescope's server with a link to every device, starts and connects its `Switch` devices, and switches on each output that a device's `spec.power` names. | 10 min |
| `StartDevices` | Starts the pod of every other device, and waits until each pod is Ready and its driver defines its device on the server. A device on real hardware waits here for its claim. | 10 min |
| `Connect` | Connects the mount, the GPS, the polar aligner, the focusers, the filter wheels, the rotators, the dust caps, the flat panels, the sky quality meters, the receivers, and the cameras, in that order. | 2 min |
| `Configure` | Writes the observatory's location and the `DOME_POLICY` to the mount, the location to the GPS, each camera's `ACTIVE_DEVICES` from its train, the camera's gain and offset, the tube's focal length and aperture, and the filter names. It then relays the dome's park state to the mount, before a procedure unparks it. | 2 min |
| `Activation` | Runs the activation procedures, from the top of the tree down: the `Observatory`'s and its devices', unless another reservation in the observatory ran them, then the `Telescope`'s and its devices', then those of the devices of its trains. "Procedures" below states what they do. | none: each action's timeout |
| `StartGuider` | Starts the guider's pod, waits for PHD2's event server, sends `set_connected`, and waits until PHD2 reports its camera and mount connected. A telescope with no `Guider` skips it. | 10 min |
| `Abort` | Stops PHD2's exposures and guiding with `stop_capture`, then ends each exposure and stops the mount if it moves. | 2 min |
| `Deactivation` | Runs the deactivation procedures, from the bottom of the tree up: those of the devices of the telescope's trains, then the `Telescope`'s own devices' and its own. When the last telescope in the observatory ends, it then runs the observatory's devices' and the `Observatory`'s own. Every device is still connected. | none: each action's timeout |
| `StopGuider` | Deletes the guider's pod, `Service`, and `ConfigMap`, while its camera and mount are still connected. | 2 min |
| `Disconnect` | Disconnects the devices in the reverse order of `Connect`. | 2 min |
| `StopDevices` | Deletes the device pods. | 2 min |
| `PowerOff` | Switches the outputs off, then stops the `Switch` pods and the telescope's server. | 5 min |
| `StopSite` | Disconnects the observatory's devices, switches their outputs off, and stops its server, unless a reservation of another telescope in the observatory is active. | 5 min |

Deactivation begins at `spec.end`, or when a person deletes the
reservation. The finalizer `observatory.liken.sh/deactivate` holds a
deleted reservation until deactivation is done. A reservation that
reaches `spec.end` stays, `Released`, until a person deletes it.

A step that passes its deadline, or a device that answers a change with
Alert, fails the step, and the reservation is `Failed`. The `Ready`
condition names the step and the device. After a failed activation
step, the telescope stays as the steps left it, so a person can look;
deleting the reservation runs deactivation from `Abort`. After a failed
deactivation step, the finalizer stays, because the devices may not be
safe to power off. In both cases, this runs the failed step again:

```sh
kubectl annotate reservation east-tonight -n observatory observatory.liken.sh/retry=1
```

One telescope serves one reservation at a time. A second reservation
of the telescope waits in `Wait`, and its summary names the reservation
it waits for. Waiting reservations take the telescope in the order of
their `spec.start`, and of their creation when they have none.

While a reservation is `Ready`, the operator creates again each pod
that is deleted. When a device's driver comes back on the server
disconnected, after its pod or the server restarted, the operator
connects it and writes its settings again. A device that a person
disconnects in KStars stays disconnected. The operator also creates
the guider's pod again, and connects the camera and the mount of each
new PHD2 once.

A device that joins or leaves a telescope or the observatory during a
reservation is an ordinary edit, and the operator refuses no change.
While the reservation is `Ready`, the server keeps running, and the
other devices on it stay connected. A device that joins gets its pod,
and then its driver starts on the running server, and the operator
connects it and writes its settings. A device that leaves, to the
shelf or to another telescope, has its driver stopped on the server,
and then loses its pod, its `Service`, and its `ResourceClaim`. The
operator posts a `DriverStarted` or a `DriverStopped` Event on the
`Telescope` or the `Observatory`, and the device that left gets a
`PodDeleted` Event. A device that leaves during activation keeps its
pod until the reservation is `Ready`, or until deactivation's
`StopDevices`. A device on a telescope with no active reservation
changes nothing that runs.

While such a pod is gone, its device is `Starting`, and the `Ready`
message of the device or its `Guider` reads `Creating pod <name>`.
During activation, before the steps create the pod, the device's
message reads `Waiting for activation to create pod <name>`.
When the operator creates the pod, it records a `PodCreated` Event on
the device, the `Guider`, or the `Telescope` or `Observatory` whose
INDI server the pod runs, and writes one line to its log. A new PHD2
starts idle and not calibrated, so the holder calibrates and starts
guiding again. A new INDI server starts each driver disconnected, and
the operator connects each device again.

Each resource except a `Reservation` posts an Event each time a condition first
appears or changes its status or its reason, with the condition's
reason and message. A `ParentFound` that is `False`, a `Ready` whose
reason is `Error`, and a `Safe` that is `False` are `Warning`s. `kubectl describe` lists them
for an hour.

## Procedures

Each resource states what the operator does with its equipment, in
three fields of its spec. Every device kind, the `Telescope`, and the
`Observatory` have them:

- `activation` runs as the resource's `Telescope` or `Observatory`
  turns `Active`, in the reservation's `Activation` step.
- `deactivation` runs as it stops being `Active`, in the
  `Deactivation` step, while every device is still connected.
- `on` lists triggers. Each one names a condition in `when`, and runs
  its actions in `run`, as "Triggers" below states. YAML reads a bare
  `on` as a boolean, so quote the key: `"on":`.

`activation`, `deactivation`, and each trigger's `run` are lists of
actions that run in order. An action is a target
state, so it is safe to run twice: the operator reads what the device
reports, and sends nothing when the device is there already. Each kind
accepts only the actions it supports, and the CRD refuses the others:

| Kind | Action | Default timeout |
|---|---|---|
| `Dome`, `Mount` | `state: Parked` or `state: Unparked` | 10 min |
| `DustCap` | `state: Open` or `state: Closed` | 10 min |
| `FlatPanel` | `state: "On"` or `state: "Off"`, the light, quoted because YAML reads a bare `On` as a boolean | 10 min |
| `Camera` | `cool: {celsius, within}`: write the setpoint and wait until the sensor is within `within`, 0.5 °C by default | 20 min |
| `Camera` | `warm: {celsius, within}`: warm the sensor, then switch the cooler off | 10 min |

A `warm` that does not reach its setpoint by its timeout still
switches the cooler off, and notes where the sensor is, because a
cooler cannot warm a sensor above the air around it. The setpoint of a
camera's first `cool` action is also the setpoint that the operator
sends again to a camera whose driver restarts, and the `Setpoint`
column of `kubectl get cam`. The `Activation` step's summary names each
camera whose driver has a cooler and whose activation does not cool
it.

Every action also takes these fields:

- `timeout`, a duration such as `20m`, bounds the action and its
  waits. An action that passes it fails.
- `requires` lists conditions, as `{kind, name, type, status}`, that
  must hold before the action runs. `status` is `"True"` unless the
  field says otherwise. The operator waits for each one until the
  timeout, and the action's summary names what it waits for, such as
  `Waiting for WeatherStation lab Safe=True`. `requires` binds only the
  operator's own actions: a move that a person makes in KStars is
  stopped only by the drivers' park locks.
- `after` lists resources, as `{kind, name}`, whose runs for the same
  step must end first. `{kind: Mount}` with no name means every
  `Mount` in the observatory. A resource with no run in the step is not
  waited for.

A reference with no `kind` names the resource itself. A `kind` of
`Observatory` or `Telescope` with no `name` names the resource's own.
A reference that names nothing that exists fails the action, and the
message names the field, such as `after[0]: no Mount north`.

The tree orders the runs. Activation runs the `Observatory` and then
its devices, then the `Telescope` and then its own devices, then the
devices of its trains. Deactivation runs the same tiers in reverse.
The runs of one tier run in parallel. So the dome unparks before the
mount, and the mount parks before the dome, with no `after`. An
`after` that names a resource of a later tier waits until the
action's timeout, and the action then fails. The observatory's tiers
run when the first reservation in it activates, under a lock, and a
second reservation finds those runs `Done`. They run again at
deactivation when the last telescope in the observatory ends.

A `Telescope` and an `Observatory` report the condition `Active`. A
telescope is `Active` from the start of its reservation's `Activation`
step until the start of its `Deactivation` step. An observatory is
`Active` from the `Activation` step of the first reservation in it
until the `Deactivation` step of the last one. Each run records the
`lastTransitionTime` of `Active` that it answers, in `since`, so a
trigger runs once for each transition.

A resource's `status.procedures` holds the last run of each trigger:
its `trigger`, `since`, `state`, start and stop times, `summary`, and
each action with its state, times, and summary. The reservation's
`Activation` and `Deactivation` steps copy the actions they waited on
into `status.steps[].actions`, each with its resource, so
`kubectl describe reservation` shows the whole activation. A run that
an operator restart interrupted resumes from the record, and runs
again each action that is not `Done`. A failed action fails its run
and the step, and the retry annotation runs the failed run again,
while a run that is `Done` stays `Done`. Each run posts an Event on its
resource when it starts and when it ends: `ProcedureStarted`,
`ProcedureDone`, or the Warning `ProcedureFailed`.

```sh
kubectl get dome lab -n observatory -o jsonpath='{.status.procedures}'
kubectl get rsv east-tonight -n observatory \
  -o jsonpath='{.status.steps[?(@.name=="Activation")].actions}'
```

### Triggers

A trigger runs its actions once for each transition of its condition
to the status it names, while its resource is active: from the end of
its activation run, or from the start of its `Telescope`'s or
`Observatory`'s activity when it has no activation, until its
deactivation begins. `when` is `{kind, name, type, status, for}`, with
the same rules as a reference in `requires`. Each run records the
condition's `lastTransitionTime` in `since`. A condition that holds
when the resource's activation ends runs the trigger then, so a dome
that unparks in bad weather with no `requires` parks again at once.

`for` delays the run until the status has held that long, such as
`for: 20m`, and a change of the status before then cancels it, as
`for` does in a Prometheus alert rule. A run that goes on when its
condition changes, or when its resource's deactivation begins, stops,
and its record is `Skipped` with the reason, such as
`WeatherStation lab Safe is no longer False`. A trigger whose
condition names nothing records one `Failed` run and one Warning.

The tree gives no order to a trigger, so `after` orders the runs of
one transition: in a trigger, `after` waits for the runs of the other
resources' triggers on the same condition and status. A resource with
no such trigger, or one that is not active, is not waited for.

```yaml
kind: Dome
spec:
  "on":
  - when: {kind: WeatherStation, name: lab, type: Safe, status: "False"}
    run:
    - state: Parked
      after: [{kind: Mount}]
  - when: {kind: WeatherStation, name: lab, type: Safe, for: 20m}
    run: [state: Unparked]
```

`examples/simulators.yaml` states a whole site this way: the dome
unparks while the weather station reports `Safe`, the mounts unpark,
the cap opens, and the camera cools to -10 °C. When the weather turns
unsafe, the mounts park and then the dome parks, and after 20 minutes
of safe weather the dome unparks again. At the end, the flat panel's
light goes off, the cap closes, the camera warms to 5 °C, the mounts
park, and the dome parks.

## The dome and mount locks

While an `Observatory` has a `Dome`, the domes and the mounts lock
each other's park, and no field turns the locks off. INDI's drivers
enforce both locks, and the operator writes each policy and relays
what each driver needs to read:

- Each mount's `DOME_POLICY` is `DOME_LOCKS`. The mount refuses to
  unpark while a dome is parked or moving. The mount does not park
  when the dome parks: INDI leaves that to its watchdog driver.
- Each dome's `MOUNT_POLICY` is `MOUNT_LOCKS`. The dome refuses to
  park while the mount of any reserved telescope in the observatory is
  unparked or moving.
- Each dome's shutter follows its park state:
  `DOME_SHUTTER_PARK_POLICY` has `SHUTTER_CLOSE_ON_PARK` and
  `SHUTTER_OPEN_ON_UNPARK` both On. The dome enforces this alone, also
  while the operator is down.

In an observatory with no dome, each mount's `DOME_POLICY` is
`DOME_IGNORED`. INDI's mount starts locked and unlocks only when a
dome reports that it is unparked, so with no dome to report, a mount
under `DOME_LOCKS` never unparks. The observatory then has no
`LocksRelayed` condition.

A driver reads another device's park state through its own INDI
server, but the dome runs on the observatory's server and each mount
on its telescope's. So the operator relays the park states between the
servers. Each mount receives the domes' state under the name in its
`ACTIVE_DEVICES.ACTIVE_DOME`. The dome receives one state for every
mount under the name in its `ACTIVE_DEVICES.ACTIVE_TELESCOPE`:
unparked while any mount is unparked, moving, or silent. A mount that
does not report its park state counts as unparked until the
`Deactivation` step of its reservation has ended. A dome that does not
report counts as parked.

The `Observatory`'s `LocksRelayed` condition reports the relay. It is
`True` while the operator relays each state, and its message names
what it relays. It is `False` with the reason `Waiting` while a device
does not report what the relay needs, and with the reason `Idle` while
no dome or mount runs. While the operator is down, each driver keeps
the last state that the operator relayed, and a later park or unpark is
not relayed. A driver that restarts forgets that state, and the
operator relays it again when the driver connects.

When a driver refuses a move under a lock, it answers with `Alert`,
and the operator posts a `Warning` on the device: `MountUnparkRefused`
on the `Mount`, or `DomeParkRefused` on the `Dome`. A step that asked
for the move fails, as it does for any `Alert`.

## The guider

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
09](plans/completed/09-the-guider.md) lists the dialogs that PHD2 can open.

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

## The pods and their names

The operator names each pod, `Service`, and `ResourceClaim` it creates
`<resource-name>-<kind>`, with the kind in lowercase. The `Mount`
`east` runs in the pod `east-mount`, the `Camera` `east-main` in
`east-main-camera`, and the INDI server of the `Telescope` `east` in
`east-telescope`. So `kubectl get pods` lists one telescope's pods
together. Give a device the name of its telescope, and add a word only
when the telescope has two devices of one kind, as the example does
for its cameras.

A generated name must be a DNS label of 63 characters or fewer,
because each shim dials its device by the `Service` name. The operator
refuses a resource whose generated name breaks that rule, and the step
that needs the name fails with a message that gives the longest
resource name that fits.

The scheduler places most pods. The guider's pod, and the camera of the
`OpticalTrain` that the telescope's `Guider` names, have a required pod
affinity to the telescope's server, so they run on the server's node
and the guide frames cross no link between nodes. A guide camera with a
`spec.claim` gets no affinity, because the node of its device decides
where it runs. A telescope with no `Guider` has no affinity on any
pod. The guider's pod, `Service`, and `ConfigMap` take the name
`<guider>-guider`, such as `east-guider`.

## The INDI client

A `Client` opens one connection to one `indiserver` for each call of
`Run`. The client sends `getProperties` and stores each property that
the server defines, with its label, group, type, permission, state,
switch rule, timeout, timestamp, and members. Each update after that
changes the store. When the connection ends, the store empties, and the
next `Run` fills it from a new baseline. A server that restarted has
drivers that restarted too, with their settings lost, so nothing from
the old connection is current.

```go
c := indi.NewClient("indiserver.observatory:7624")
go c.Run(ctx)

// Connect the focuser as soon as its driver defines CONNECTION.
if err := c.ConnectDevice(ctx, "Focuser Simulator"); err != nil {
	return err
}

// Move it, and wait for the driver to report the result.
sent, err := c.SetNumbers("Focuser Simulator", "ABS_FOCUS_POSITION",
	map[string]float64{"FOCUS_ABSOLUTE_POSITION": 52000})
if err != nil {
	return err
}
p, err := c.Settle(ctx, sent)
```

A caller reacts to changes in two ways, and neither reads the state
again on a timer:

- `Subscribe` returns a channel of events: `Connected`,
  `Disconnected`, `Defined`, `Updated`, `Deleted`, `Message`, and
  `Invalid`. An event names what changed, and the store holds the
  values.
- `WaitFor` waits until a condition on the store holds, such as "the
  device defines `CONNECTION`" or "the member equals 52000". It checks
  the condition after each change.

`SetNumbers`, `SetTexts`, and `SetSwitches` send every member of the
property, and refuse a change that the definition forbids: a read-only
property, the wrong type, a member the property does not have, or
switches that break the switch rule. `Settle` waits until the device
answers a change: Busy and then Ok, or Ok at once with the values sent.
It reports Alert as an `AlertError`.

The client sends no `enableBLOB` unless a caller calls `EnableBLOB`,
so by default it receives no frame data. The store records the format
and size of each BLOB and keeps none of its data. A caller that asked
for BLOBs receives the data in the update's event.

`ParseNumber` reads a decimal or sexagesimal number, such as
`12:30:00`, and `FormatNumber` prints a value in its member's format,
including INDI's `%m` formats.
