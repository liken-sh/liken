# Working on equipment-operator

This repository holds the `Receiver` resource and the operator that
drives it: A/V equipment at the far end of a `liken` machine's cable,
reached over the network for volume, power, and input. The manifests and
the tests are the documentation, and the comments teach how the system
works.

`plans/README.md` indexes the plans. Code exists only where a plan calls
for it.

`make test` runs every check CI runs.

## The Denon driver

A protocol is a driver in its own directory. The first is `denon/`, and
its `AGENTS.md` holds the protocol references, the command families, and
the model notes. The package implements the `equipment.Driver` contract
in `equipment/`, which the controller imports and the driver never does.

## Errors include their source's text

An error that wraps a tool, a daemon socket, a bus answer, or a provider
includes that source's own text word for word: its `stderr`, its
response body, or its error string. The wrapped error and the status
field or record that the failure writes both include it, so a person
reads the cause from the log or the status without opening a shell.
