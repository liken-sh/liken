# Version-pinned system images need a scheduling design

Open problem. The system pods reference their images by the per-node
tag `installed`, which resolves to whatever build the node's own OS
imported. That tag is mutable, so "which binary is this pod running"
has no durable answer. An attempt to replace it with version-stamped
references was built and then reverted on 2026-08-26, because
adversarial review found three fleet-deadlock paths that the pin
opens. This file records what was learned, so the next attempt starts
from the evidence instead of rediscovering it.

## The contract the mutable tag provides

Every boot, `seedClusterState` replaces `k3s/agent/images` with the
running release's tarballs, and the import re-points `installed` at
them. A container restart resolves the tag again. So a node-bound pod
always converges to the build its own OS carries, one reboot at the
latest, with no scheduler involvement. The pods run where they run,
and the tag is what makes them self-healing there.

The cost is a stale-binary window: a pod that starts before the
import completes runs the previous release's build under the same
name, and nothing in the pod spec records which build it got.

## Why the version pin deadlocks

With `image: liken.sh/<name>:<version>` and `imagePullPolicy: Never`,
a pod spec can name an image the target node does not hold, and such
a pod never starts. Review found three paths from there to a stuck
fleet:

- The `cluster-operator` Deployment uses `strategy: Recreate` and
  plain node selection. When the first upgraded leader applies the
  new manifests, the replacement pod can bind to a not-yet-upgraded
  node and sit in `ErrImageNeverPull`. The conductor is the only
  writer of reboot grants, so no other machine ever takes its turn.
- `init/imports.go` discards the containerd store when the previous
  boot died unproven, and the store then holds only the new release's
  images. A pod spec still naming the old version cannot start, and
  on the `machine-operator` that removes the node's heartbeat, its
  reboot handling, and the import proof that would stop the next
  discard.
- `OnDelete` protects only existing pods. The DaemonSet controller
  always creates a pod on a node that has none, from the current
  template. A machine that joins from an older stick, or loses a pod
  to eviction, gets a template its OS cannot run.

## What the review established about a release label

A node label carrying the booted release, with a required node
affinity on the system pods, was designed and reviewed as the repair
for those paths. Two verified facts decided against it:

- k3s PATCHes every `--node-label` onto the `Node` on each agent
  start (`pkg/agent/run.go`, verified against release-1.31 through
  1.34), so `init` can write a truthful label with no operator
  involved. The k3s agent documentation says registration-only and is
  wrong for labels.
- The DaemonSet controller deletes a running pod when its node stops
  matching a required affinity
  (`pkg/controller/daemon/daemon_controller.go`, `updateNode`). A
  label moving backward after a slot fallback would delete the log
  relay and `iscsid` pods on that node immediately. `OnDelete` does
  not prevent this.

So a required affinity on a DaemonSet converts label motion into pod
deletion, and the label must move on every upgrade and every
fallback. The affinity fits the one pod that reschedules across
nodes, the `cluster-operator` Deployment, and fits none of the
DaemonSets.

## The direction that looks right

A node-critical agent whose binary must always match the node's own
OS is what a kubelet static pod is for. The OS image would carry the
`machine-operator`'s pod manifest in the kubelet's manifest
directory, so the pod exists exactly where and how the OS says, with
no scheduler, no template lag, and no image resolution across
versions. The open design question is credentials: a static pod
cannot use a `ServiceAccount`, so the node needs a minted client
certificate with the operator's permissions. That is a milestone, not
a patch.

Until then, the mutable tag stays, documented as the contract above,
and the versioned tag that `image/oci.sh` writes beside it is the
inspection path: the store always answers which builds a node holds,
even though the pod spec does not.
