# Working on firmware-operator

This directory holds `firmware-operator`, the add-on that loads and
updates the firmware of the devices on `liken` machines. It has no
code yet. `plans/README.md` describes the component, and its plans
hold the design.

@../brand/voice.md

The voice rules in that file govern all prose here, comments included.

## The boundary with the OS

The OS publishes each machine's devices and reports the firmware that
the kernel requested and did not find. Everything else about device
firmware is here: the `Firmware` resource, the pods that load and
flash a device, the proposals from the LVFS catalog, and the status of
each operation. Keep vendor firmware and vendor tools out of `liken/`.
When this operator needs a new fact about a device, the OS publishes
it as an attribute in the machine's `ResourceSlice`, and this operator
reads it there.
