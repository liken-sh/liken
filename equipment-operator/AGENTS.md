# Working on equipment-operator

This repository holds the `Receiver` resource and the operator that
drives it: A/V equipment on the far end of a `liken` machine's cable,
reached over the network for volume, power, and input.

Like the rest of the `liken` project, it is written to be read: the
manifests and the tests are the documentation.

`plans/README.md` indexes the plans. Code exists only where a plan
calls for it.

`make test` runs every check CI runs.

## The Denon driver

A protocol is a driver in its own directory. The first is
`denon/`, and its `AGENTS.md` holds the protocol references, the
command families, and the model notes. The package implements the
`equipment.Driver` contract in `equipment/`, which the controller
imports and the driver never does.

## Errors carry their source's words

An error that wraps a tool, a daemon socket, a bus answer, or a
provider carries that source's own text: the stderr, the body, or the
error string, verbatim. It goes in the wrapped error and in whatever
status field or record the failure writes, so a person reads the cause
from the log or the status and never needs a shell to find it.
