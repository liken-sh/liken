# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It also states how the work was proved, and a proof runs on
hardware.

The README states what the operator is. These documents state why it
is built the way it is, and what it still owes an answer to.
`completed/` holds the plans that are built.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem.

## The design

* [00, The equipment-operator design](00-design.md). What equipment
  is, what a `Receiver` holds, how the operator owns the room's level
  on the media bus, and what the Service front is for.
* [01, Prometheus metrics](completed/01-prometheus-metrics.md). Built and drilled on liken-1 on 2026-09-10. The
  operator serves Prometheus metrics on port 9200 under liken's shared
  contract: the receiver connected and claimed, power, volume, the
  selected input, and commands by status.
* [02, The Denon driver and the full receiver mirror](completed/02-denon-driver.md). Built and drilled on the house cluster on 2026-09-19. The protocol moved into `denon/` behind the `equipment.Driver` contract, the parser covers every line the house's AVR-X1700H emits, and the status carries both zones and the receiver's own settings.
* [04, Declarative settings and the bus controller](04-declarative-settings-and-bus.md). Every setting the driver reads becomes declarable in `spec.denon.settings` and settable over a bus settings topic, one-shot actions go over a bus commands topic, and each setting key has exactly one writer.
* [07, The receiver's HTTP interface and the TV wake](07-receiver-http-and-tv-wake.md). The receiver answers a second interface over HTTP, and it carries HDMI Control and the video controls. The receiver is a CEC responder, so it cannot wake the TV, and the wake needs a node-attached CEC adapter from plan 05. The design is not written.

## Open problems

* [WiiM settings that the amp does not report back](open-problems/the-wiim-settings-the-amp-will-not-confirm.md). The equalizer, the output mode, and the subwoofer have setters but no readable value, so they wait for a model whose reads answer. The alarm slots and the per-model command set sit beside them.

## Completed

* Plan 01, the `Receiver` with the `denon` protocol, the session, and
  the level path. Proved on a living room on 2026-09-07: the Denon's
  status followed its own remote, a `Play` selected the input, and
  the room's remote stepped the receiver under a declared ceiling.
  Releases 2026.09.07-001 to -003. Three things that day changed the
  design: the Denon's `MVMAX` line is not a limit, a session must
  adopt the receiver's position before it applies any level, and a
  press is a direction and not a level.

* Plan 02, the Denon driver and the full receiver mirror. Drilled on
  the house cluster on 2026-09-19: the live `living-room-denon` Receiver
  reported both zones (main on MPLAY at 65, zone2 on PHONO at 90) and
  every setting under `status.denon`, and a dimmer change made at the
  receiver itself moved `status.denon.system.dimmer` from bright to
  dark and back. Release 2026.09.19-001.

* Plan 05, the operator on the host network. Drilled on the house
  cluster on 2026-09-22: the operator runs on the host network, finds
  the amps by mDNS and SSDP, and resolves each identity to a current
  address without a declared address. The metrics listener moved to
  9260.

* Plan 06, the WiiM driver. Drilled on the house cluster on
  2026-09-22: three WiiM amps report `status.wiim` in full, discovery
  creates a Receiver for an unclaimed amp and steps aside when a person
  declares one, a declared setting reaches the device and returns in
  status, and a settings write over the media broker lands on the
  device and back in `spec.wiim.settings`.

* Plan 08, the WiiM's event path. Drilled on the house cluster on
  2026-09-22: the driver subscribed to three WiiM amps' UPnP events for
  volume, mute, and the transport state, and a volume change made on
  the master bedroom amp itself reached the room's topic in the same
  second while the poll stayed at ten seconds. Development build
  `2026.09.19-002-dev-028-ab218213`.
