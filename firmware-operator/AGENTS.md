# Working on firmware-operator

This directory holds `firmware-operator`, the add-on that loads and
updates the firmware of the devices on `liken` machines. It defines
the cluster-scoped `Firmware` resource, and the operator that runs
each upload as a short pod on the device's machine. The manifests, the
source, and the tests are the documentation. `plans/README.md`
describes the component, and its plans hold the design.

@../brand/voice.md

The voice rules in that file govern all prose here, comments included.

The operator watches every `Firmware`, every `Catalog`, every
`ResourceSlice` of the driver `liken.sh`, every `Machine`, and its own
upload and scan pods through client-go's reflector, in the shared
`kubernetes` module. Load the
`operators` skill under `.agents/skills` at the top of the repository
before you change a watch, the pass, or a timer, and the `testing`
skill before you change a test.

`make test` runs every check CI runs.

## The boundary with the OS

The OS publishes each machine's devices and reports the firmware that
the kernel requested and did not find. Everything else about device
firmware is here: the `Firmware` resource, the pods that load and
flash a device, the proposals from the LVFS catalog, and the status of
each operation. Keep vendor firmware and vendor tools out of `liken/`.
When this operator needs a new fact about a device, the OS publishes
it as an attribute in the machine's `ResourceSlice`, and this operator
reads it there.
