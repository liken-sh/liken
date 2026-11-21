# Working on equipment-operator

`equipment-operator` is a Go Kubernetes operator. `README.md` says
what it is for and describes its kinds.

## How it runs

The image contains two builds of the program. The node build is
compiled with the build tag `node`. Each workload runs one of them.

- **The `Deployment`** runs on the host network. It has to, because a
  pod on the overlay network gets no reply to an mDNS or SSDP search.
  Only one copy is active at a time, under a Lease. It keeps a
  connection open to each receiver in the receiver's own protocol,
  such as TCP to a Denon or HTTPS with UPnP events to a WiiM. It
  writes the `Receiver` status. It also writes the rest of the
  `CECBus` status and the `Television` status, which it works out
  from the adapters' reports, the `Display` objects, and the
  `Receiver` objects.
- **The `cec` `DaemonSet`** runs a pod on each node, and that pod claims
  the node's USB-CEC adapter. When no `CECBus` names the pod's machine,
  the pod creates one in `Listen` mode. The pod has the adapter open and
  sends every CEC message. It writes its adapter's report into the
  `CECBus` status. It writes the result of each command it sends into
  the `Television` status.

A request from a `Player` goes through the workloads in this order:

1. `media-operator` writes the `Player`'s session and each key press
   into the `Receiver`'s status.
2. The `Deployment` sends the key presses to the receiver. It copies
   the session to the `Television` whose picture comes from the
   session's `Display`.
3. The node workload whose adapter announces that `Display` wakes the
   TV or puts it in standby.

In the other direction, when a person selects the machine's input on
the TV, the node workload writes a request into the `Television`
status. `media-operator` then passes it on to the `Player`'s screen.

Both workloads use each `Display`'s physical address.
`display-operator` reads that address from the monitor's EDID.

## Design

The operator turns each request into one command, sends it once, and
reports what the equipment says back. It does not store a state for
the equipment and enforce it. So when a person selects another input
or turns the TV off with its own remote, that choice stays. After a
restart, the operator does not act on the state it finds.

A receiver counts as reachable only when it replied recently. A
router reboot can leave a socket half open, and that socket still
accepts writes but delivers nothing.

The CEC workload scans the bus once when its adapter joins. After
that, it follows the messages it receives. It does not scan on a
timer, because a TV switched its own input while an adapter scanned
the bus on a timer.

`media-operator` owns the `Player`, its remote, and the message bus.
It decides when to wake the equipment in a room. This operator does
not connect to the message bus. It owns the connections to the
equipment and every fact that the equipment reports. `liken`
publishes the adapter as a device, and `display-operator` owns the
`Display`.

## Finding things

- The protocol packages `cec/`, `denon/`, and `wiim/` each have an
  `AGENTS.md` with the protocol reference and the notes on real
  devices. It loads when you work in that directory. `upnp/` has
  none.
- Load the `receiver-drivers` skill under `.agents/skills` before you
  add a receiver setting or a receiver brand.
- `transect/` contains the kind knowledge that `transect` reads for the
  `Receiver`. A change to a condition, a printer column, or a status
  field that the file names also changes the file.
