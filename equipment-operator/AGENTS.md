# Working on equipment-operator

This repository holds the `Receiver` resource and the operator that
drives it: A/V equipment on the far end of a `liken` machine's cable,
reached over the network for volume, power, and input.

Like the rest of the `liken` project, it is written to be read: the
manifests and the tests are the documentation.

`plans/README.md` indexes the plans. Code exists only where a plan
calls for it.

`make test` runs every check CI runs.
