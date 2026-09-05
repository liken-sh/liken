# Version system images without blocking scheduling

Open design question. System pods use the node-local `installed` image
tag so each node runs the images imported by its own OS. Replacing that
tag with version-specific references needs a design that still works
when the fleet runs several releases.

An implementation using version-stamped references was reverted on
2026-08-26 after review identified three ways it could block a rollout.
The constraints below record that review; they do not select a replacement.

## The current image contract

On boot, `seedClusterState` supplies the running release's image tarballs
under `k3s/agent/images`. Importing them updates `installed` on that
node. A container restart resolves the tag against the local image store
without requiring a cluster-wide template update to name the new build.

The intended contract is that a node-bound pod follows its own OS build
by the next boot, without version-aware scheduling. This supports
mixed-version rollouts. There is still a startup window: a container
started before import finishes can resolve the previous build. The mutable tag in the pod
spec does not identify which build was selected. Runtime image IDs and
the version tags in the local store remain inspection evidence; the
problem is that the spec alone does not pin the running build.

## Why version references can block a rollout

With `image: liken.sh/<name>:<version>` and `imagePullPolicy: Never`,
a node cannot start a pod whose named image is absent. The earlier
review identified these paths:

- The `cluster-operator` `Deployment` uses `Recreate` and node selection
  without an OS-version constraint. After an upgraded leader applies the
  new template, its replacement pod can be scheduled on an older node
  and fail with `ErrImageNeverPull`. With no conductor running, other
  machines receive no new upgrade turns.
- [init/imports.go](../../init/imports.go) discards the container store
  after an unproven import boot. The rebuilt store may contain only the
  current release's images. An older machine-operator pod spec then
  cannot start, stopping its heartbeat, reconciliation, and import proof.
  The missing proof can cause another discard on the next boot.
- `OnDelete` preserves existing `DaemonSet` pods, but it does not preserve
  an older template for a node that needs a new pod. A machine joining
  from an older stick, or recreating an evicted pod, receives the current
  template even if its OS has only older images.

## Constraints on version labels and affinity

The earlier review considered a node label naming the booted release and
required node affinity on system pods. It recorded two upstream findings:

- `k3s` patches `--node-label` values onto the `Node` on each agent start.
  The review checked `pkg/agent/run.go` on release branches 1.31 through
  1.34. That behavior allows `init` to supply the label without waiting
  for the machine operator, despite the registration-only wording in
  the agent documentation examined at the time.
- The Kubernetes `DaemonSet` controller's `updateNode` path deletes pods
  when a node stops matching required affinity. After slot fallback, a
  version label can move backward and trigger deletion of log relays or
  `iscsid`. `OnDelete` does not prevent affinity-driven deletion.

Required affinity may suit the movable cluster-operator `Deployment`.
Applying it unchanged to every `DaemonSet` does not satisfy the existing
fallback requirements. These upstream observations should be rechecked
against the vendored versions before implementation.

## Candidate direction

An OS-authored kubelet static-pod manifest could keep the node-critical
machine operator's image and pod configuration matched to the booted OS.
That avoids selecting its image through a template updated by another
node. It is a candidate, not a requirement that excludes other designs.

Static pods cannot use the normal pod `ServiceAccount` projection path.
A client certificate is one candidate credential. Issuance, rotation,
revocation, and authorization would need to work before this operator
starts. The related [node-scoped credential problem](machine-operator-credentials-have-fleet-wide-access.md)
needs both a node identity and enforcement of that identity's authority;
issuing a different certificate alone does not establish isolation.

[image/oci.sh](../../image/oci.sh) already creates a versioned tag beside
`installed`. Until a replacement is designed, that tag supports
inspection without changing the running pod templates.

## Remedy scope

**Broader deployment, scheduling, and credential design.** The goal
remains that a node-critical operator can start from its own OS during
upgrade and fallback. A remedy must preserve that behavior while changing
how images and manifests are selected.

The decisions include which components should be static pods, how the
movable cluster operator finds compatible nodes, and how credentials are
bootstrapped and scoped. These are not resolved by replacing a tag string
or adding required affinity.

## Verification needed

Any candidate must cover the three failure paths above, fallback to an
older slot, and container startup before import completes. Test loss and
rotation of the proposed credentials as well. A solution must not depend
on the machine operator already running to make its own image or initial
credential available.
