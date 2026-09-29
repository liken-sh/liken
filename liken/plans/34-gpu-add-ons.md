# GPU add-ons

Milestone 34. Proposed. It would let one machine hold a GPU compute
stack as a second read-only image on its boot slot, declared on the
Machine.

The stock image ships no GPU compute stack, and this milestone would
keep it that way. It would give one machine a way to hold a compute
stack without a change to every machine: an add-on, a second read-only
image on the boot slot, declared on the Machine, and mounted over the
stock root at boot. The first add-on would be NVIDIA compute, because
CUDA on Kubernetes is the use case that needs this. The work waits
for experience with bare metal from milestone 32.

## Why an add-on and not a flavor

A flavor would be a second release: the same OS built again with more
software in it. A flavor conflicts with liken's own upgrade model. The
Cluster moves the whole fleet with one version field, so a flavor
makes every node hold what one node needs, and mixed fleets are the
normal case. A flavor also doubles the release matrix for each axis it
adds. An add-on follows the Machine instead, the way `spec.modules`
already does: one stock release for the fleet, plus a payload for the
machines that declare it.

## Design

An add-on would be a squashfs beside liken.sqfs on the slot. When the
Machine declares it, boot.cpio would mount it as an overlay under the
stock root. The artifact would use the same machinery as everything
else on a slot: digest-pinned in a release document, fetched from the
channel, staged and proven before a machine uses it. The other parts
that an add-on needs already exist. Cluster features (milestone 17)
turn the stack on, the k3s restart tier (milestone 20) picks up the
container runtime that k3s detects automatically, and the machine operator's DRA
driver (milestone 11) publishes the devices.

## What the NVIDIA add-on would hold

Four layers:

* The open kernel modules, built against liken's exact kernel pin.
  These are MIT/GPL dual-licensed, so liken can vendor them cleanly.
* The GSP firmware that the open modules require, which is the
  `nvidia/` directory that milestone 32 excludes: 154 MiB that
  compresses only to 101 MiB.
* The proprietary userspace driver (libcuda, nvidia-smi),
  redistributable under NVIDIA's own terms, which the licensing domain
  must read closely.
* The container toolkit, which k3s configures as a containerd runtime
  without help from liken.

CUDA stays out. A pod brings its own CUDA in its image, and the host
only needs a driver at least as new as the pod's CUDA requires.

The payload is not the difficult part. The module build is the
difficult part. It is an out-of-tree compile
against the kernel pin, in CI, and it must run again with every kernel
bump. This would be liken's first vendored
domain that builds source instead of one that verifies a download. A
wrong build does not affect the boot path: it breaks GPU workloads, and
the base OS still boots.

## Slot size budget

Measured at the 20260622 firmware pin, the NVIDIA stack comes to
approximately 380 to 530 MiB compressed: 101 for the GSP firmware, an
estimated 250 to 350 for the userspace driver, and tens of MiB for the
modules. A 1Gi slot holds about 520 MiB beyond the stock payload, so
the add-on fits only in the best case, and then tightly. The likely solution is that a
machine which declares a GPU add-on claims 2Gi slots. The bundle's budget
guard would then state which add-ons a slot size holds. Slot sizes are
grow-only and set at claim time, so this choice belongs to the
install, which is where the Machine already declares its storage.

## Display drivers that stay in the stock image

The stock image keeps every console driver: i915, xe, radeon, and
amdgpu. A measurement decided the question for amdgpu: its 108 MiB of firmware
compresses to 22 MiB in the squashfs. Removal would save almost
nothing, and it would remove the console on every machine with AMD
integrated graphics. The radeon driver does not cover those machines. radeon
serves discrete cards from before 2013, and every AMD APU since then
runs on amdgpu. An AMD compute add-on (ROCm) can follow the NVIDIA
design later, but the display firmware stays in the stock image.
