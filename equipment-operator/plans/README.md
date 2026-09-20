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
  is, what a `Receiver` holds, how the level rides the media bus, and
  what the Service front is for.
* [01, Prometheus metrics](completed/01-prometheus-metrics.md). Built and drilled on liken-1 on 2026-09-10. The
  operator serves Prometheus metrics on port 9200 under liken's shared
  contract: the receiver connected and claimed, power, volume, the
  selected input, and commands by status.
* [02, The Denon driver and the full receiver mirror](completed/02-denon-driver.md). Built and drilled on the house cluster on 2026-09-19. The protocol moved into `denon/` behind the `equipment.Driver` contract, the parser covers every line the house's AVR-X1700H emits, and the status carries both zones and the receiver's own settings.
* [04, Declarative settings and the bus controller](04-declarative-settings-and-bus.md). Every setting the driver reads becomes declarable in `spec.denon.settings` and settable over a bus settings topic, one-shot actions go over a bus commands topic, and each setting key has exactly one writer.
* [05, The operator on the host network](05-the-operator-on-the-host-network.md). The operator stays a Deployment and takes host network, so SSDP and mDNS discovery reach the LAN in process, and 9200 moves to a free host port. Equipment that attaches to a node (CEC, serial, IR) becomes a component on that node. This plan is ordered before the WiiM driver, plan 06.

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
