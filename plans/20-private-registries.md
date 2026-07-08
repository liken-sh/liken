# Private registries

Milestone 20 — Not started

Two related capabilities at the containerd level.

First, mirrors and credentials: k3s's registries.yaml teaches
containerd to pull through a private mirror and to authenticate. A
homelab running its own registry, or anyone with Docker Hub
credentials, needs both on every node. The mirror endpoints are
cluster-scoped facts; the credentials are secrets, the same kind of
material as the identity bundle, so they should ride the image the
way the join token does rather than pass through the API.

Second, k3s's embedded registry (Spegel): nodes serve each other's
already-pulled images peer-to-peer, which matters for a fleet on a
slow uplink.

Both are declarations about how images arrive, and neither should
require editing the OS.
