# Network discovery can be turned off

Plan 12. Built on 2026-09-28, and tested on the laptop against the fake
API server and a stubbed search. No drill has run on a cluster yet,
and the drill that is owed is at the end of this plan.

## The problem

The `Deployment`'s operator always starts network discovery. It
searches the LAN for WiiM amps with mDNS and SSDP for 4 seconds, 30
seconds apart, creates a
`Receiver` for each amp that no `Receiver` names, and then polls and
drives that amp. A cluster that shares its LAN with amps it must not
drive, such as a test cluster on the same network as a home's own
equipment, has no way to stop this. The operator adopts the home's
amps and opens a session on each one.

## The design

### One variable on the `Deployment`

`EQUIPMENT_NETWORK_DISCOVERY` takes `on` or `off`. The operator reads
its settings from environment variables that `deploy/operator.yaml`
states, so this setting is one more variable. The base manifest states
`"on"`, and a cluster owner changes it with a strategic merge patch on
the `operator` container, which the install guide shows. A kustomize
patch merges `env` entries by name, so the patch replaces the base's
value.

An unset variable reads as `on`, so a `Deployment` written before this
change behaves as before. Any other value stops the operator at start
with an error that names the value. A YAML 1.1 reader, such as
`kubectl`, reads an unquoted `off` as a boolean, and a person can
write `Off` or `false`. An operator that read every unknown value as
`on` would then search the LAN that the owner meant to protect.

### What off does

`controller.run` starts discovery's goroutine only when the setting is
on. With it off, the operator writes one line at the start of its
loop, and `discovery.run` never runs. So `wiim.Discover` never opens
its mDNS or SSDP socket, and `discovery.reconcile` never creates or
deletes a `Receiver`.

A `Receiver` that a person declares runs as before. A WiiM `Receiver`
gets its address from discovery when `spec.wiim.address` is empty, so
with discovery off it must state the address. The field descriptions
in `receivers-crd.yaml` say so.

A WiiM client with no address sends no request. Go dials an empty host
as the local machine, so a client that built `https://:443/...` would
poll port 443 of the operator's own node, which is on the host network.
With discovery off, a WiiM `Receiver` with no address and each
discovered `Receiver` that stays are both such clients, so `wiim`'s
`call` returns an error for them before it builds a request.

The `Receiver` objects that discovery created before the owner turned
it off stay. Discovery deletes one of its own only after three full
searches in a row missed the amp, and with no search there is no such
evidence. Deleting them all at start would also delete the objects a
person edited, because a person can keep the discovered label on an
object and add inputs to it. The same rule means that a discovered
`Receiver` does not give way to a person's `Receiver` for the same amp:
that delete is also a step of the search. The install guide says so,
and shows how to list the discovered objects by the
`equipment.liken.sh/discovered` label.

### The CEC paths stay out of the setting

Two other paths create objects that nobody declared, and neither opens
a session to a device on the LAN.

* The CEC node workload creates a `CECBus` in `Listen` for an adapter
  that no `CECBus` names. The pod runs only on a node where DRA
  allocated a `cec-adapter` device, so the wire is the HDMI cable of
  that node. In `Listen` the adapter claims no logical address and
  sends nothing.
* The `Deployment` creates a `Television` only for a `CECBus` in
  `Control`, and only a person sets a bus to `Control`. So a
  `Television` follows a person's consent to send on that wire.

A test cluster next to a home's equipment reaches the home's amps over
the shared LAN. It reaches a home's TV over CEC only through an
adapter on its own node's cable. So the setting covers the network
search only, and it is named for it.

## How it was proved

| Behavior | Test |
|---|---|
| Unset and `on` read as on, `off` reads as off, and any other value is an error | `TestNetworkDiscoveryTakesOnOrOff` |
| Through `serve`, with discovery off, no search runs, no `Receiver` is created, and a declared Denon `Receiver` connects; with it on, the search runs and creates the amp's `Receiver` | `TestNetworkDiscoveryOffSearchesForNothing` |
| With discovery off, the loop writes one line that names the variable | `TestNetworkDiscoveryOffWritesOneLine` |
| A WiiM client with no address sends no request and reports unreachable | `TestAClientWithNoAddressSendsNothing` |

The search in these tests is a stub that counts its calls and finds one
amp. `TestMain` makes any search a test did not stub fail the run, so no
test sends a real mDNS or SSDP query.

## Considered and set aside

* **A `ConfigMap`.** media-operator reads its container resource
  settings from a file that a `configMapGenerator` makes, because those
  settings are a structured block. This setting is one word, and every
  other setting of this operator is an environment variable.
* **Accepting `true` and `false`.** It would make an unquoted `on` or
  `off` work in a manifest that a YAML 1.1 reader converts. It also
  adds a second spelling for each value, and the error at start
  already names the value the operator read.

## The drill still owed

On liken-1, with the operator on a build of this change:

1. Patch `EQUIPMENT_NETWORK_DISCOVERY` to `"off"` and check that the
   operator's log has the line that says discovery is off, and no
   `discovery found` line after it.
2. Check that `kubectl --context liken-1 get receivers -l
   equipment.liken.sh/discovered` lists no object created after the
   rollout.
3. Patch the value to `"false"` and check that the pod stops at start
   with the error that names the value. Set it back to `"off"`.
