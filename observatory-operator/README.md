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

## The manual

The manual at [liken.sh/observatory](https://liken.sh/observatory/)
describes the operator for the people who run it: the guides to
install it, describe equipment, connect USB hardware, reserve a
telescope, write procedures, and guide with PHD2. The concepts explain
how a reservation runs, how procedures run, and how the locks and the
guider work. The reference lists what each status field means and
every field of the 20 resources. Its source is in [`docs/`](docs/), and
`make -C docs skills` emits each guide as an Agent Skill in
[`skills/`](skills/).

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
