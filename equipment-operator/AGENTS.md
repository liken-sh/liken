# Working on equipment-operator

This directory holds the `Receiver` resource and the operator that
drives it: the A/V receiver that a `liken` machine plays through,
controlled over the network for volume, power, and input. It also holds
the `CECBus` and `Television` resources, which reach the same
equipment over the HDMI-CEC wire. The manifests and the tests are the
documentation, and the comments teach how the system works.

The operator connects to no message bus. `media-operator` writes a
`Player`'s session into `Receiver.status.session`, with an ask for
each press of a volume, power, or home key, and `session_asks.go`
applies each ask once.

`plans/` holds the design documents. Code exists only where a plan calls
for it.

`make test` runs every check CI runs.

## Receiver settings

Build every receiver setting to these rules. Plan 14 brings the
existing blocks up to them, and plans 15 to 18 add families.

A setting that means the same thing on every brand goes in the
`Receiver`'s `spec.settings`, in one vocabulary with explicit units,
so a person learns one shape and a new receiver keeps the room's
setup. The protocol block, such as `spec.denon` or `spec.wiim`, holds
the connection and the settings only that brand has. Every `Receiver`
holds exactly one protocol block. Status mirrors the spec:
`status.settings` and `status.<protocol>`.

A plan names which families get built. Each field in a driver's table
states one tier, and the operator learns what a model supports from
the model, never from a table of models:

* **Confirmed**: the receiver reads the field back. The operator
  compares, sends a field that differs, and confirms it.
* **Sent once**: a documented setter with no read. The operator sends
  it once for each change of the family, and `status.settledSettings`
  keeps it from being sent again after a restart.
* **Reported**: a read with no setter, in status only.
* **Unsupported**: the model refused the read or the command, left it
  out of its capability document, or did not answer it during the
  first read.

The tier in the table is a ceiling: a Confirmed field that a unit does
not read back is sent once on that unit. A device's answer that means
"not in this state" never makes a field Unsupported.
`status.settingExceptions` lists each field whose state differs from
its tier, with the reason, and a declared field in that list makes
`SettingsConfirmed` False. The operator never changes an undeclared
field to make a declared one land.

An undocumented setter is declarable only after a drill shows that the
receiver reads it back. A command that acts once, such as a reboot or
an alarm, is not a setting. Plan 14 holds the reasons, the common
vocabulary, and the protocol references for a new receiver brand.

## The Denon driver

A protocol is a driver in its own directory. The first is `denon/`, and
its `AGENTS.md` holds the protocol references, the command families, and
the model notes. The package implements the `equipment.Driver` contract
in `equipment/`, which the controller imports and the driver never does.

## The CEC driver

`cec/` speaks HDMI-CEC through the Linux kernel's
[CEC API](https://docs.kernel.org/userspace-api/media/cec/cec-api.html)
on `golang.org/x/sys/unix`, with no libCEC and no cgo. Its `AGENTS.md`
holds the protocol references, the message families, what the kernel
answers by itself, and how to run the tests against the kernel's
`vivid` driver. The first adapter is the Pulse-Eight USB-CEC adapter
([product page](https://www.pulse-eight.com/p/104/usb-hdmi-cec-adapter)).

A CEC adapter is attached to one node, so the program has a second
mode, `cec`, which `deploy/cec.yaml` runs as a `DaemonSet` from the
same image. The `DaemonSet`'s pod claims the adapter's `-cec` device,
runs the adapter in its `CECBus`'s mode, and writes its own entry
under that `CECBus`'s `status.adapters`. The `Deployment`
derives `status.devices` and the conditions from those entries. The
node workload's own files are `cecnode*.go`, and the `Deployment`'s
own files are `cecbus_derive.go` and `cecbus_controller.go`. Both
workloads use the `CECBus` types in `cecbus.go` and the API calls in
`cecbus_client.go`.

The image holds two builds of the program. The `Deployment` runs
`/equipment-operator`, which holds the leader election in `leader.go`.
The `DaemonSet` runs `/equipment-operator-node`, built with the tag
`node`, which leaves the election and client-go's typed clientset out
(`leader_node.go`). `make test` vets both builds, and fails when the
node build links the election or the typed clientset.

A `Television` is the TV of one `CECBus`. The `Deployment` derives
its status from the bus, the `Display` objects, and the `Receiver`
objects in `television_derive.go`, and `television_controller.go`
runs that pass at the end of each `CECBus` pass. The node workload
whose adapter sends the bus's commands applies each generation of
`spec.power` once in `cecnode_power.go`, and reads the TV's power in
`cecnode_television.go`. Both use the types in `television.go` and
the API calls in `television_client.go`.

The TV wake crosses both workloads. A `Receiver` session tells its
room each wake and each sleep through `roomEvents` in `session.go`.
The `Deployment` matches the session's input to the `Television`
that lists the input's `Display`, and writes that `Television`'s
`status.session`, in `television_session.go`. The node workload whose
adapter speaks for that `Display` runs the wake in
`cecnode_wake.go`, and claims and guards the active source in
`cecnode_source.go`. The remote's power button turns the room off the
same way: a `powerAsk` reaches the session's `power` in
`session_power.go`, which asks the room for standby, the `Deployment`
writes `status.session.standbyAt`, and the node workload sends the TV
Standby in `cecnode_standby.go`. While the session holds the room
awake, the same adapter answers Request Active Source and Set Stream
Path in `cecnode_answer.go`. A home press writes a `show` in the
`Receiver`'s `status.session.inputAsk`: the `Deployment` writes
`status.session.showAt` in `television_show.go`, and the node workload
sends a TV that is on Image View On and Active Source in
`cecnode_show.go`.

The bus can also ask for the Player's screen. The TV's Set Stream Path
for the `Display` of a session that sleeps asks it to wake, and a
Standby while the session holds the room awake asks it to sleep. The
node workload writes the ask in the `Television`'s `status.screenAsk`
in `cecnode_screen.go`, and `media-operator` relays each new ask to
the Player's screen client. The screen then moves the session, and
the node workload follows the session: a wake that answers the TV's
pick sends Active Source alone. When the session sleeps with no
standby, the node workload sends the TV Inactive Source in
`cecnode_inactive.go`, and it does the same when it stops while no
session holds the room awake. It reports its own power as On only
while a session holds the room awake. Every adapter of a bus announces the bus's
`spec.osdName`, `liken` by default.

## Errors include their source's text

An error that wraps a tool, a daemon socket, a bus answer, or a provider
includes that source's own text word for word: its `stderr`, its
response body, or its error string. The wrapped error and the status
field or record that the failure writes both include it, so a person
reads the cause from the log or the status without opening a shell.

Each operation at human scale writes one log line that states its
trigger, the command, and the device's report, and a scan, a poll, or
a steady status write writes none. The CEC node workload also writes
one line for each message it hears that a person notices, such as
Active Source, Routing Change, Image View On, or Standby, with the
sender's logical address, name, and physical address, in
`cecnode_heard.go`.
