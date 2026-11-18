# observatory-operator

`observatory-operator` controls the equipment of an observatory on a
[`liken`](https://liken.sh/) cluster, under the API group
`observatory.liken.sh`. You describe the observatory's hardware as
resources, and a `Reservation` gives one holder the use of one
`Telescope`. While a reservation is active, the operator runs each of
the telescope's INDI devices in its own pod and serves them on one INDI
server. It connects and configures each device in a fixed order, and
runs the activation procedures that each resource declares. It starts
PHD2 for the telescope's `Guider` and connects it to the guide camera
and the mount. When the reservation ends, it runs each resource's
deactivation procedures and reports that the devices are safe to power
off. KStars or `astrophotography-operator` drives the telescope through
its INDI server and guides through PHD2's event server.

The Go module `github.com/liken-sh/liken/observatory-operator` contains
the operator and these packages:

- [`observatory/`](observatory/) defines the kinds of
  `observatory.liken.sh/v1alpha1` as Go types. The operator reads and
  writes them, and `astrophotography-operator` imports them to read a
  reservation's session and a telescope's state.
- [`indi/`](indi/) is a client of INDI protocol version 1.7, written in
  Go with no cgo. It keeps every device and property that a server
  defines, applies each update, validates each change against the
  property's definition before it sends the change, and waits for a
  device to respond.
- [`phd2/`](phd2/) is a client of PHD2's event server. It reads
  PHD2's state, calibration, equipment, pixel scale, and guide steps
  from the event stream, and sends `set_connected` and `stop_capture`.
  `phd2/phd2test` is a fake event server for tests.
- [`drivers/`](drivers/) maps each INDI driver to the image of the
  `indi` build that contains it, and names the images of the guider's
  pod.

The module also builds the `kubectl liken observatory` plugin from
[`cli/`](cli/), into the image `observatory-operator-cli` that
`liken plugins sync` installs. With the plugin you confirm that you set
a telescope up for the night.

[`deploy/`](deploy/) contains the CRDs, the RBAC, and the operator.
[`examples/simulators.yaml`](examples/simulators.yaml) is an
observatory of simulators with one device of each kind.
[`plans/00-design.md`](plans/00-design.md) is the design, and [root
plan 74](../plans/completed/74-astrophotography.md) describes the two
components and the contract between them. `make test` runs every check
that CI runs.

## Running the operator

```sh
kubectl apply -k deploy/
kubectl create namespace observatory
kubectl apply -n observatory -f examples/simulators.yaml
kubectl get astro -n observatory
```

`deploy/` creates the CRDs and the operator: one `Deployment` with one
replica in `liken-system`, which watches the resources of every
namespace. Every resource of one observatory goes in one namespace,
and the operator runs the observatory's pods and `Job`s in that
namespace. CI publishes the image
`ghcr.io/liken-sh/observatory-operator`, and the kustomize base as the
OCI artifact `observatory-operator-deploy` with the image tag set to
the release.

The equipment resources alone start nothing. The
example ends with the `Reservation` `east-tonight`, which starts the
`east` telescope at once and keeps it running until you delete the
reservation.

## The manual

The manual at [liken.sh/observatory](https://liken.sh/observatory/)
is for the people who run the operator: its guides, concept pages, and
the reference of every field. The source is in [`docs/`](docs/), and
`make -C docs skills` writes each guide as an Agent Skill in
[`skills/`](skills/).

## The INDI client

A `Client` opens one connection to one `indiserver` for each call of
`Run`. The client sends `getProperties` and stores each property that
the server defines, and applies each later update to the store. When
the connection ends, the store is emptied, and the next
`Run` fills it again from a new `getProperties`. When the server
restarts, its drivers restart too and lose their settings, so nothing
from the old connection is still valid.

```go
c := indi.NewClient("indiserver.observatory:7624")
go c.Run(ctx)

// Connect the focuser once its driver defines CONNECTION.
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

A caller follows changes in one of two ways, and neither polls on a
timer:

- `Subscribe` returns a channel of events: `Connected`,
  `Disconnected`, `Defined`, `Updated`, `Deleted`, `Message`, and
  `Invalid`. An event identifies what changed, and the values are in
  the store.
- `WaitFor` waits until a condition on the store is true, such as "the
  device defines `CONNECTION`" or "the member equals 52000". It checks
  the condition after each change.

`SetNumbers`, `SetTexts`, and `SetSwitches` send every member of the
property, and refuse a change that the definition forbids: a read-only
property, the wrong type, a member the property does not have, or
switches that break the switch rule. `Settle` waits until the device
reports the result of a change: Busy and then Ok, or Ok at once with
the values sent. It returns an Alert state as an `AlertError`.

The client sends no `enableBLOB` unless a caller calls `EnableBLOB`,
so by default it receives no frame data. The store records the format
and size of each BLOB but not its data. A caller that enabled BLOBs
receives the data in the update's event.

`ParseNumber` reads a decimal or sexagesimal number, such as
`12:30:00`, and `FormatNumber` prints a value in its member's format,
including INDI's `%m` formats.
