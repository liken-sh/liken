# Working on firmware-operator

`firmware-operator` is a Go Kubernetes operator. `README.md` says what
it is for and describes its kinds.

## How it runs

The operator is one unprivileged `Deployment` in `liken-system`, with
leader election. It watches every `Firmware`, every `FirmwareCatalog`,
every `ResourceSlice` of the driver `liken.sh`, every `Machine`, the
`Disruption`s that it created, and its own pods. For each catalog, it
downloads the jcat, which lists the catalog's file and its SHA-256,
and then the file itself, both over HTTPS. It writes the status of
each `Firmware` and `FirmwareCatalog`.

The operator's process never opens a device. Each operation on a
device runs in a short-lived pod that the operator creates on the
device's machine:

- **An upload pod** runs an upload rule's image and command, with a
  `ResourceClaim` on the one bare device.
- **A scan pod** runs fwupd against a catalog's file. It is privileged
  and has no claim, because fwupd reads many devices. It only reads.
- **A rehearsal pod** runs an approved install against copies of the
  machine's firmware state, and records what the install would write.
- **An apply pod** prepares before the machine drains. When the pod is
  ready, the operator asks `machine-operator` for a held reboot turn
  through a `Disruption`. A held turn drains the machine and then
  waits for the operator before the reboot. While the machine is
  drained, the operator signals the pod to write. Then the operator
  lets the reboot go ahead.

The scan, rehearsal, and apply programs print their result as one
line of JSON at the end of the pod's log, and the operator reads it
from there. An upload has no such line. Its result is the exit code of
the loader, and whether the bare device then disappears from the
machine's `ResourceSlice`.

## Design

A scan installs nothing. An install needs three things, in this
order: a person approves one release and pins it to its cabinet by
digest, a rehearsal on that machine passes, and the machine gets its
held reboot turn. A Secure Boot database update is written while no
workload runs on the machine, and it takes effect at the next boot.

After the kubelet ends an upload pod or an apply pod, the operator
does not run it again. The kubelet can end such a pod at a node
shutdown. A second pod would then write to the device on a different
boot from the one that the attempt was made for. So a person decides
whether to try again. A scan or a rehearsal writes nothing to a
device. When the kubelet ends one of them for a reason other than a
node shutdown, the operator runs the same attempt in a new pod.

The OS publishes each machine's devices, and it ships the firmware
that the kernel's drivers load. Everything else about device firmware
belongs to this operator, including vendor firmware and vendor tools.
When this operator needs a new fact about a device, the OS publishes
the fact as an attribute in the machine's `ResourceSlice`, and this
operator reads it from there.

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.

## Finding things

`plans/README.md` lists each plan with its status. Root plan 81 says
which component owns each kind of firmware.
