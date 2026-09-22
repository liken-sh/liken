# The WiiM driver

Plan 06. Built and drilled on the house cluster on 2026-09-22. The
driver, its status, discovery, the spec settings, and the message bus
are built. The settings families the Amp will not confirm are [an open
problem](../open-problems/the-wiim-settings-the-amp-will-not-confirm.md).

## The problem

A Receiver reaches one piece of A/V equipment over the network. The
first protocol is the Denon. A WiiM is the same equipment through a
different protocol: it has power, volume, mute, and inputs, and it
sits at the far end of the same cable. It belongs in the same resource
and the same `Receiver` kind. It has no driver, so a cluster with WiiM
amps cannot see them.

The WiiM also reports far more than the Denon does. A Denon status
carries the zones and a settings snapshot. A WiiM reports its own
identity, network, playback, now-playing, audio output, equalizer,
timers, Bluetooth, presets, and controls. All of that is observable,
and phase 1 is to observe it and report it.

## The design

### A second protocol block

`spec` names the protocol by which block it carries. `spec.denon` and
`spec.wiim` are siblings, and the schema holds a Receiver to exactly
one of them. The shared fields do not change: `spec.volume`,
`spec.inputs`, `spec.zones`, the two bus topics, and `spec.power` are
the same for both protocols.

`spec.wiim` carries the identity and the address:

* `uuid` is the LinkPlay UUID, twelve bytes of hex. It is the identity
  and the only key the operator trusts.
* `address` is a hint, optional. The driver reads the device's own
  `uuid` from `getStatusEx` and compares it before it sends any
  command, so an address that moved never drives the wrong amp. A later
  plan resolves the UUID to a current address by mDNS or SSDP
  discovery, and the address hint becomes unnecessary.

The connection and the settings travel together, as they do for the
Denon. The WiiM's settings surface is a later phase; phase 1 declares
only the identity.

### The driver

`wiim/` implements `equipment.Driver`, the same contract `denon/`
implements. The controller imports `equipment/` and neither driver.
The unit builds the driver its protocol block names, and nothing in the
shared controller names a protocol type beyond that branch.

The transport is unauthenticated HTTPS GET to
`https://<address>/httpapi.asp?command=<name>[:<args>]`. Every request
is a GET, including the setters. The API is stateless, so a driver
holds no session. The TLS certificate is self-signed, so the client
skips verification.

The driver polls. Each poll reads the command set, folds the answers
into its own state, and reports an event. A retrofit to the device's
UPnP GENA event path is a later phase; polling is enough for a status.

### The status

`status.wiim` is a strongly typed snapshot in the device's own units,
the way `status.denon` is the Denon's. It is grouped: the device and
its identity, the network, the playback, the current track, the audio
output, the equalizer, the timers, Bluetooth, presets, and the
controls. `wiim/AGENTS.md` records the command behind every field and
names the source that documents it.

The common `status.zones` block still carries the normalized contract:
one `main` zone with power, input, mute, and volume.

Power is always `on`. A WiiM has no standby command over the API; its
only power control is an idle timer at the device, and a driver cannot
reach it. The alternative, reading pause as standby, was set aside. A
driver that mapped power onto play and pause would claim a standby the
device does not have, and it would start or stop playback, which is the
media session's job and not the receiver's.

The driver reports volume as a whole step from 0 to 100, so
`VolumeResolution` is 1. The input is the `mode` the player reports,
mapped to the input names the amp carries.

### What phase 1 leaves out

* Control declared in the spec. Volume, input, mute, and sleep are on
  the driver's `equipment.Driver` methods already, but no `spec.wiim`
  settings block and no declarative field beyond neither is built. The
  driver answers its setters for the phases that follow.
* The message bus. The settings and commands topics are on the
  Receiver already; the WiiM handlers are a later phase.
* Discovery. Host network and the UUID-to-address lookup are plan 05,
  and phase 1 declares the address hint instead.

## Phases

1. The driver and its status. `spec.wiim` with the identity, the
   polling driver, `status.wiim`, the CRD, and three amps declared on
   the house cluster from a development build.
2. The driver as a full Receiver. The inputs bind a Player to the amp
   the way a Denon's do, so the media operator holds the session and
   the player's level drives the WiiM. A session uses the amp's own
   reported ceiling when `spec.volume.max` is not declared, because a
   WiiM's top is fixed where a Denon's limit wanders. Network
   discovery finds an amp and resolves its identity to an address, so
   `spec.wiim.address` is a hint rather than a requirement, and the
   operator creates a Receiver for a discovered amp no Receiver
   claims. This is the built phase.
3. Stop for review.
4. Control declared in the spec. The `device` and `audio` settings
   families are built; the equalizer, the output mode, and the
   subwoofer wait for a model whose reads confirm them. Power stays
   always on, so `spec.power` stays meaningless for a WiiM.
5. Control over the message bus. The settings and commands topics now
   drive the same families and the one-shot commands, and a settings
   write is returned to `spec.wiim.settings` at the leaf, so the
   resource stays true.

### Discovered Receivers

The operator finds the amps itself, so a cluster owner does not have to
write an address by hand. A run browses mDNS and sends an SSDP search,
merges the answers by UUID, and repeats for a window, because each path
misses a device. The address that comes back resolves the identity to a
current one for every Receiver, and no `spec.wiim.address` is needed.

A discovered amp no Receiver claims gets a Receiver of its own, named
by its lowercased UUID and marked with the `equipment.liken.sh/discovered`
label. The marker is what lets the operator prune only its own objects.
A person's Receiver that names the same UUID always wins: the operator
creates nothing beside it and deletes its own copy, so the person's
room-named object stands. An amp that stops answering takes the
operator's copy with it.

### The input binding

The inputs are the Receiver's common field, and the media operator
matches a unit's machine and monitor id to an input with no knowledge
of the protocol. A WiiM's input names are its own (`optical`, `line-in`,
`hdmi`, `bluetooth`, `wifi`, `usb`, `udisk`), and the driver maps each
onto the device's `switchmode` value. A session then selects the input
and owns the level, which is how the player's volume drives the amp.

## Verification

Phase 1 is proved on the house cluster, which has three WiiM amps. The
operator runs from a development build at a pinned commit, and the
three amps are declared as Receivers with their UUIDs and addresses.
Each receiver reports `Reachable: True`, a `status.zones.main` entry,
and a populated `status.wiim`. The three devices were read directly
first, so the parser is written against what the firmware answers and
not against the collected notes alone.

The fake-device tests pin the parse of every command family. A
transcript from a model that answers differently turns a family
unproven and marks it in `wiim/AGENTS.md`.

## What this leaves for later

* The settings surface as a control vocabulary: the equalizer, the
  balance, the output mode, the subwoofer, the device name, the lights,
  the buttons, and the alarms.
* The realtime path: UPnP GENA eventing in place of polling, and the
  media session's use of the bus topics.
* Discovery, and the host network plan 05 gives it.
* Which commands each model answers. One owner found `reboot` works on
  a Mini and fails on an Amp, so the command set is not fixed across
  the line.
