# GPU add-ons

Milestone 34 — Not started; waits on bare-metal experience (32)

The stock image ships no GPU compute stack, and this milestone keeps
it that way. It gives one machine a way to carry a compute stack
without making every machine carry it: an add-on, a second read-only
image on the boot slot, declared on the Machine, mounted over the
stock root at boot. The first add-on is NVIDIA compute, because CUDA
on Kubernetes is the use case that asks for this.

## An add-on, not a flavor

A flavor would be a second release: the same OS built again with more
aboard. That shape fights liken's own upgrade model. The Cluster
moves the whole fleet with one version field, so a flavor makes every
node carry what one node needs, and mixed fleets are the normal case.
A flavor also doubles the release matrix for each axis it adds. An
add-on follows the Machine instead, the way spec.modules already
does: one stock release for the fleet, plus a payload for the
machines that declare it.

## The shape

An add-on is a squashfs beside liken.sqfs on the slot. When the
Machine declares it, boot.cpio mounts it as an overlay under the
stock root. The artifact travels the same machinery as everything
else on a slot: digest-pinned in a release document, fetched from
the channel, staged and proven before a machine trusts it. The
enabling pieces already exist: the feature vocabulary (milestone 17)
turns the stack on, the k3s restart tier (milestone 20) picks up the
container runtime that k3s detects on its own, and the machine
operator's DRA driver (milestone 11) publishes the devices.

## What the NVIDIA add-on carries

Four layers. The open kernel modules, built against liken's exact
kernel pin; these are MIT/GPL dual-licensed, so vendoring them is
clean. The GSP firmware that the open modules require, which is the
nvidia/ directory that milestone 32 excludes: 154 MiB that
compresses only to 101 MiB. The proprietary userspace driver
(libcuda, nvidia-smi), redistributable under NVIDIA's own terms,
which the licensing domain must read closely. And the container
toolkit, which k3s turns into a containerd runtime by itself. CUDA
stays out: a pod brings its own CUDA in its image, and the host only
needs a driver at least as new as the pod's CUDA asks for.

The hard part is none of the payload. It is the module build: an
out-of-tree compile against the kernel pin, in CI, re-run in
lockstep with every kernel bump. This is liken's first vendored
domain that builds source instead of verifying a download. The risk
stays off the boot path: a wrong build breaks GPU workloads, and the
base OS boots regardless.

## The budget

Measured at the 20260622 firmware pin, the NVIDIA stack comes to
roughly 380 to 530 MiB compressed: 101 for GSP firmware, an
estimated 250 to 350 for the userspace driver, and tens for the
modules. A 1Gi slot holds about 520 MiB beyond the stock payload, so
the add-on fits tightly at best. The likely answer is that machines
which declare a GPU add-on claim 2Gi slots, and the bundle's budget
guard learns to state which add-ons a slot size holds. Slot sizes
are grow-only and set at claim time, so this choice belongs to the
install, which is where the Machine already declares its storage.

## What stays aboard

The stock image keeps every console path: i915, xe, radeon, and
amdgpu. The measurement that settles amdgpu: its 108 MiB of firmware
compresses to 22 MiB in the squashfs, so evicting it saves almost
nothing, and it would cost the console on every machine with AMD
integrated graphics. The radeon driver does not cover those: radeon
serves discrete cards from before 2013, and every AMD APU since runs
on amdgpu. An AMD compute add-on (ROCm) can follow the NVIDIA shape
later, but the display firmware stays stock.
