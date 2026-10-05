# observatory-operator

`observatory-operator` will be the hardware control layer of an
observatory on a [`liken`](https://liken.sh/) cluster, under the API
group `observatory.liken.sh`. It will run each INDI device in its own
pod with its own DRA claim, serve every device on one INDI server, run
the PHD2 guider, and configure each device when it appears. KStars, or
`astrophotography-operator`, drives the observatory through that server.

The operator is not built yet. This directory holds the resources of
the API group and the Go module
`github.com/liken-sh/liken/observatory-operator`, with two packages:

- [`observatory/`](observatory/) holds the 20 kinds of
  `observatory.liken.sh/v1alpha1` as Go types. The operator reads and
  writes them, and `astrophotography-operator` will import them to read
  a telescope's state and to create a `Reservation`.
- [`indi/`](indi/) is a client of the INDI protocol, version 1.7, in
  Go with no cgo. It keeps every device and property that a server
  defines, follows each update, sends changes that follow each
  property's definition, and waits for a device to answer. The
  operator uses it to connect and configure the devices on each
  server, and `astrophotography-operator` will import it to drive a
  session.

[`deploy/`](deploy/) holds the namespace and the CRDs, and
[`examples/simulators.yaml`](examples/simulators.yaml) is an
observatory of simulators with a device of every kind.
[`plans/00-design.md`](plans/00-design.md) is the design, and [root
plan 74](../plans/74-astrophotography.md) holds the architecture and
the tests behind it. `make test` runs every check CI runs.

## The resources

```sh
kubectl apply -k deploy/
kubectl apply -n observatory -f examples/simulators.yaml
kubectl get astro -n observatory
```

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
| `Mount` | `mnt` | `telescope` | RA, Dec, parked, tracking |
| `GPS` | none | `telescope` | fix, time |
| `PolarAligner` | `pac` | `telescope` | adjustment state |
| `Camera` | `cam` | `opticalTrain` | temperature, setpoint, cooler, exposure |
| `FilterWheel` | `fw` | `opticalTrain` | slot, filter |
| `Focuser` | `foc` | `opticalTrain` | position |
| `Rotator` | `rot` | `opticalTrain` | angle |
| `DustCap` | `cap` | `opticalTrain` | `Open`, `Closed`, or `Moving` |
| `FlatPanel` | `flat` | `opticalTrain` | light, brightness |
| `Dome` | none | `observatory` | azimuth, shutter, parked |
| `WeatherStation` | `weather` | `observatory` | safety |
| `SkyQualityMeter` | `sqm` | `telescope` or `observatory` | brightness in mag/arcsec² |
| `Switch` | `sw` | `telescope` or `observatory` | the outputs that are on |
| `Receiver` | `rx` | `telescope` or `observatory` | frequency in Hz |
| `Guider` | none | `telescope` | its `Ready` reason |
| `Reservation` | `rsv` | `telescope` | phase, step |

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
  until the driver sends it.
- `properties`: every INDI property that the device defines, with its
  label, group, type, permission, state, switch rule, and the value and
  limits of each member. A vendor's own properties are here. The list
  holds no BLOB data.

A `Telescope`'s status names its INDI server in `status.server`, its
active reservation, its tubes, its trains with their devices, its own
devices, and whether its guider is ready. An `Observatory`'s status
names its server, its telescopes, its devices, the active reservations,
and the worst verdict of its weather stations.

A `Reservation` moves through the phases `Scheduled`, `Activating`,
`Ready`, `Deactivating`, and `Released`, or `Failed`. `status.steps`
lists every step in order, with its state, start and finish times, and
a message that names the device a step waits for:

| Activation | Deactivation |
|---|---|
| `Wait`, `StartSite`, `PowerOn`, `StartDevices`, `Connect`, `Configure`, `Prepare` | `Abort`, `Secure`, `Disconnect`, `StopDevices`, `PowerOff`, `StopSite` |

Each step's state is `Pending`, `Running`, `Done`, `Failed`, or
`Skipped`, and `status.step` names the one that runs now. Plan 07
states what each step does.

```sh
kubectl get rsv -n observatory -w
kubectl wait --for=condition=Ready reservation/east-tonight -n observatory
kubectl get rsv east-tonight -n observatory \
  -o jsonpath='{.status.steps[?(@.name=="Connect")].message}'
```

The `Ready` condition is `True` while the phase is `Ready`, and
`status.endpoint` then holds the host and port for KStars.
`SafeToPowerOff` is `True` when the deactivation steps are done. The
`Guider` kind has its CRD, but its pod is [plan 09](plans/09-the-guider.md):
its `Ready` condition is `False` with the reason `NotImplemented`.

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
