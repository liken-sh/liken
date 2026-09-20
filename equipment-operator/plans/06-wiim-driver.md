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
and set aside, the phases, and the verification. The open items in
`wiim/AGENTS.md` are the first inputs to it.
