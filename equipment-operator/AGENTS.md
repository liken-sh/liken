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

## The CEC driver

`cec/` speaks HDMI-CEC through the Linux kernel's
[CEC API](https://docs.kernel.org/userspace-api/media/cec/cec-api.html)
on `golang.org/x/sys/unix`, with no libCEC and no cgo. Its `AGENTS.md`
holds the protocol references, the message families, what the kernel
answers by itself, and how to run the tests against the kernel's
`vivid` driver. The first adapter is the Pulse-Eight USB-CEC adapter
([product page](https://www.pulse-eight.com/p/104/usb-hdmi-cec-adapter)).

A CEC adapter is attached to one node, so the binary has a second
mode, `equipment-operator cec`, which `deploy/cec.yaml` runs as a
`DaemonSet` from the same image. Its pod claims the adapter's `-cec`
device, runs the adapter in its `CECBus`'s mode, and writes its own
entry under that `CECBus`'s `status.adapters`. The `Deployment`
derives `status.devices` and the conditions from those entries. The
node workload's own files are `cecnode*.go`, and the `Deployment`'s
own files are `cecbus_derive.go` and `cecbus_controller.go`. Both
workloads use the `CECBus` types in `cecbus.go` and the API calls in
`cecbus_client.go`.

## Errors include their source's text

An error that wraps a tool, a daemon socket, a bus answer, or a provider
includes that source's own text word for word: its `stderr`, its
response body, or its error string. The wrapped error and the status
field or record that the failure writes both include it, so a person
reads the cause from the log or the status without opening a shell.
