# observatory-operator

`observatory-operator` is the hardware control layer of an observatory
on a [`liken`](https://liken.sh/) cluster, under the API group
`observatory.liken.sh`. A person describes the observatory's hardware
as resources, and a `Reservation` gives one holder the use of one
`Telescope`. While a reservation is active, the operator runs each of
the telescope's INDI devices in its own pod, serves them on one INDI
server, and connects, configures, and prepares each device in a fixed
order. At the end it secures each device and reports that the devices
are safe to power off. KStars, or `astrophotography-operator`, drives
the telescope through its server.

The Go module `github.com/liken-sh/liken/observatory-operator` holds
the operator and three packages:

- [`observatory/`](observatory/) holds the 20 kinds of
  `observatory.liken.sh/v1alpha1` as Go types. The operator reads and
  writes them, and `astrophotography-operator` will import them to read
  a telescope's state and to create a `Reservation`.
- [`indi/`](indi/) is a client of the INDI protocol, version 1.7, in
  Go with no cgo. It keeps every device and property that a server
  defines, follows each update, sends changes that follow each
  property's definition, and waits for a device to answer.
- [`drivers/`](drivers/) maps each INDI driver to the image of the
  `indi` build that holds it.

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
Observatory          the site: location, policies, Dome, WeatherStation
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
| `Guider` | none | `telescope` | its `Ready` reason |
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
`Observatory.spec.location` and `spec.policies`, the tube's
`aperture` and `focalLength` in millimeters, the camera's `gain`,
`offset`, and `temperature` setpoint in degrees Celsius, the filter
wheel's `filters`, the guider's `opticalTrain` and `pulses`, and the
reservation's `telescope`, `holder`, `start`, and `end`. [Plan
05](plans/05-the-property-schema.md) will generate typed fields for the
other standard properties. `kubectl explain` prints every field with
its unit.

## Reading the status

The operator writes every status, and no person writes one. Each kind
has `status.conditions` in the shape of `metav1.Condition`, and
`status.observedGeneration`. `ParentFound` is `False` when a resource
that the spec names does not exist; the resource stays in place, and
the message names the missing parent.

A device's status has the same fields in every kind, and one
`readings` block of its own:

- `phase`: `Inventory` with no pod, then `Starting`, `Connecting`,
  `Connected`, and `Disconnecting`, or `Error`.
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

A `Telescope`'s status names its INDI server in `status.server`, its
active reservation, its tubes, its trains with their devices, its own
devices, and whether its guider is ready. Its `Guider` column, in
`-o wide`, shows the reason of the guider's `Ready` condition, and an
empty cell for a telescope with no `Guider`. An `Observatory`'s status
names its server, its telescopes, its devices, the active reservations,
and the worst verdict of its weather stations.

A `Reservation` moves through the phases `Scheduled`, `Activating`,
`Ready`, `Deactivating`, and `Released`, or `Failed`. `status.steps`
lists every step in order, with its state, start and stop times, and
a summary that names the device a step waits for. Each step's state is
`Pending`, `Running`, `Done`, `Failed`, or `Skipped`. `status.step`
names the step that runs now, or the step that failed, and it is
empty while the reservation is `Ready` or `Released`. "How a
reservation runs" below states what each step does.

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
device it waits for. `-o wide` adds the start, the end, and the
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
observatory-operator: Reservation east-tonight: Prepare started
observatory-operator: Reservation east-tonight: Prepare done in 19 s: cooled Camera east-main to -10 °C; found Mount east unparked
observatory-operator: Reservation east-tonight: Ready at east-telescope.observatory.svc:7624
```

`SafeToPowerOff` is `True` when the deactivation steps are done. The
`Guider` kind has its CRD, but its pod is [plan 09](plans/09-the-guider.md):
its `Ready` condition is `False` with the reason `NotImplemented`.

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
| `StartSite` | Starts the observatory's server and its devices, connects them, and writes the dome's shutter policies. Another reservation in the observatory may have started them already. | 10 min |
| `PowerOn` | Starts the telescope's server with a link to every device, starts and connects its `Switch` devices, and switches on each output that a device's `spec.power` names. | 10 min |
| `StartDevices` | Starts the pod of every other device, and waits until each pod is Ready and its driver defines its device on the server. A device on real hardware waits here for its claim. | 10 min |
| `Connect` | Connects the mount, the GPS, the polar aligner, the focusers, the filter wheels, the rotators, the dust caps, the flat panels, the sky quality meters, the receivers, and the cameras, in that order. | 2 min |
| `Configure` | Writes the observatory's location to the mount and the GPS, each camera's `ACTIVE_DEVICES` from its train, the camera's gain and offset, the tube's focal length and aperture, and the filter names. | 2 min |
| `Prepare` | Opens the dust caps, cools each camera to `spec.temperature` within 0.5 °C, and unparks the mount. | 20 min |
| `Abort` | Ends each exposure and stops the mount if it moves. | 2 min |
| `Secure` | Switches the flat panels off, closes the dust caps, parks the mount, and warms each cooled camera to 5 °C for up to 10 minutes before it switches the cooler off. | 20 min |
| `Disconnect` | Disconnects the devices in the reverse order of `Connect`. | 2 min |
| `StopDevices` | Deletes the device pods. | 2 min |
| `PowerOff` | Switches the outputs off, then stops the `Switch` pods and the telescope's server. | 5 min |
| `StopSite` | Parks the dome, disconnects the observatory's devices, and stops its server, unless a reservation of another telescope in the observatory is active. | 10 min |

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
disconnects in KStars stays disconnected. A device added to the
telescope's inventory restarts the server, which then links to it.

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

The scheduler places most pods. The camera of the `OpticalTrain` that
the telescope's `Guider` names has a required pod affinity to the
telescope's server, so it runs on the server's node and its guide
frames cross no link between nodes. A guide camera with a
`spec.claim` gets no affinity, because the node of its device decides
where it runs. A telescope with no `Guider` has no affinity on any
pod.

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
