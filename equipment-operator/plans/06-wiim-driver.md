# The WiiM driver

Plan 06. A stub. The protocol is collected and the design is not
written.

## Where the spec is

`wiim/AGENTS.md` holds everything gathered so far: the LAN-only
verdict, the models, the transport, the command families, the identity
and address rules, the findings from the live Amp in the studio, and
the references. `wiim/` is the driver's home once this plan calls for
code, and no code lives there yet.

## What is already known

The device answers over HTTPS on port 443 with no authentication, so
the operator reaches it with a GET and no token. Volume is an integer
from 0 to 100 in whole steps, so `VolumeResolution` is 1. It has one
zone. It has no standby command, so `SetPower` has no real command
behind it. Its identity is the LinkPlay UUID, and its address comes
from plan 05's host network or from a declared hint.

A `Receiver` would carry a `spec.wiim` block with the `uuid` and an
optional `address`. `wiim/AGENTS.md` holds the rest.

## What this plan owes

The sections every other plan carries: the design, what was considered
and set aside, the phases, and the verification.

Alongside them, these questions need another test or another model
before the design can promise an answer.

- **Power.** The API has no standby command, as the live test shows. A
  driver can pause and resume playback, or it can report power as
  always on and leave the standby to the device's own idle timer. The
  plan has to choose between those two.
- **Whether the local API answers when the WAN is down and the LAN is
  up.** One owner reports that local DLNA playback works in that state
  on a Pro Plus, and another reports that an Ultra asks for setup only
  when the whole router is off. Both reports test a different thing,
  and neither is proof for an Amp.
- **Which commands each model answers.** One owner found `reboot`
  works on a Mini and returns an error on an Amp, so a driver cannot
  treat the command set as fixed across the line.
- **AirPlay 2 support.** The model notes disagree, so a driver ignores
  AirPlay and does not report it.
- **The step size behind `vol++` and `vol--`.** WiiM's own list
  measures one percent, and the Arylic document for the same stack
  says six.
- **Whether the UUID survives a factory reset or a major firmware
  update.** It should, because the device advertises it as its UDN,
  but no reset has been run here.
- **Whether a cluster that spans network segments still reaches the
  amps.** The house cluster was measured on 2026-09-20: a normal
  flannel pod sees no LAN multicast, and a hostNetwork pod sees it.
  Plan 05 gives the operator host network for this reason.
