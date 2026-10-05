# observatory-operator

`observatory-operator` will be the hardware control layer of an
observatory on a [`liken`](https://liken.sh/) cluster, under the API
group `observatory.liken.sh`. It will run each INDI device in its own
pod with its own DRA claim, serve every device on one INDI server, run
the PHD2 guider, and configure each device when it appears. KStars, or
`astrophotography-operator`, drives the observatory through that server.

The operator is not built yet. This directory holds the Go module
`github.com/liken-sh/liken/observatory-operator`, with one package:

- [`indi/`](indi/) is a client of the INDI protocol, version 1.7, in
  Go with no cgo. It keeps every device and property that a server
  defines, follows each update, sends changes that follow each
  property's definition, and waits for a device to answer. The
  operator uses it to connect and configure the devices on each
  server, and `astrophotography-operator` will import it to drive a
  session.

[`plans/00-design.md`](plans/00-design.md) is the design, and [root
plan 74](../plans/74-astrophotography.md) holds the architecture and
the tests behind it. `make test` runs every check CI runs.

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
