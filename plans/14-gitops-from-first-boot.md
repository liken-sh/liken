# GitOps from first boot

Milestone 14 — Not started

GitOps from first boot, without baking an engine into the OS.
This is now an exercise for the reader, to be covered by
documentation rather than code: git-driven delivery is one way
to feed this system, not its core mode. The Machine and
Cluster resources are the real interface, and everything
above works without a repo in the loop. If someone builds it,
the OS grows two generic primitives rather than Flux support. The
first is a seed channel: manifests delivered alongside the
Machine manifest land in k3s's auto-manifests directory,
applied at first boot and owned by the repo afterward, which
needs the same staged/promoted handling the Machine manifest
gets. The second is a minting primitive: the machine creates
an SSH keypair in a Secret if one is missing and publishes
the public half in status and on the console, so the user
registers a deploy key at the forge without ever handling
private material. (The key may be read-write, since
image-update automation will eventually commit tag bumps back
to the repo.) Flux itself is delivered content, not a
vendored domain: its install manifest and sync objects ride
the seed channel, that first apply does what `flux
bootstrap`'s CLI would do, and Flux self-manages from the
repo afterward. That is the standard pattern, and another
engine could ride the same channel. This is also where the
question of manifest authority resolves: git wins, and the
seeded Machine and Cluster copies are downstream of it.
