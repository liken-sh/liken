# GitOps from first boot

Milestone 14 — Not started

GitOps from first boot is an opt-in feature. An earlier version of
this plan refused to vendor an engine. That version sketched generic
primitives instead: a seed channel to deliver manifests alongside the
Machine manifest, and a key-minting primitive. In that design, Flux
would arrive as content over the seed channel.

Milestone 17 changed this plan. The OS now has a vocabulary of
optional features, and the feature mechanism *is* the seed channel.
Init already stages each feature's manifests in the image. Init seeds
these manifests into k3s's auto-deploy directory exactly when the
cluster document declares the feature. The retraction janitor already
removes a feature's manifests when its declaration goes away. This
milestone adds one row to that vocabulary:

    spec:
      features:
        flux:
          repository: ssh://git@forge.example/fleet.git
          path: clusters/lab
          branch: main

The slug names the project, not the capability. This is a deliberate
refinement of the vocabulary's naming rule. The rule exists so that
implementations can change behind a stable name. For `iscsi`, this
rule holds true: the kernel interface is the capability, and
`open-iscsi` can be swapped out behind it.

A GitOps engine is different. Its in-cluster CRDs (`GitRepository`,
`Kustomization`) and its repository conventions form the interface
that the user builds their whole repository against. So a generic
`gitops` slug would promise a swappability that the design could
never honor. Naming it `flux` states the contract honestly. If a
deployment needs a different engine, it needs a different feature.

This is the vocabulary's first feature with parameters. It becomes
the pathfinder for the empty slots that milestone 17 built into the
machinery. `FeatureConfig` stops being one shared empty struct.
Validation grows a shape for each slug: the `flux` feature requires
`repository`, and it defaults `path` and `branch`. The CRD's features
schema is a map with one shared value schema. This is the form that
the admission drills forced. This schema now needs parameter rules
for each slug, which will likely mean CEL rules over the map values.

This work needs the same kind of live drills against a scratch CRD
that settled the original schema. Those drills are necessary because
apiextensions' pruning has already surprised this plan family once
before.

Toggling or re-pointing `flux` converges like any other feature edit.
The parameters live in the canonical rendered document. So a changed
repository produces a changed hash, and the fleet stages and applies
that change by reboot. This process is heavyweight for what amounts
to re-rendering one object. It is also consistent with everything
else the document declares. The first live re-point will show whether
this approach stays in place.

What ships in the image stays small. This keeps the kitchen-sink rule
intact: the image carries manifests only. It carries Flux's install
manifests, pinned to a Flux version the way the project pins every
vendored input. It also carries a sync object rendered at boot from
the declared parameters: the `GitRepository` and `Kustomization`
objects that do what `flux bootstrap` would otherwise do.

The controller images are deliberately not baked into the OS image.
They are ordinary workload images pulled from a registry. This design
avoids the bootstrap deadlock that forced `iscsid` to be included in the
image. A fleet that cannot reach `ghcr.io` is the problem that
milestone 20's mirrors solve.

Init's part grows one new trick. Today, init seeds feature manifests
by copying them verbatim. Flux's sync object also needs the declared
parameters rendered into it.

Identity keeps the design that this plan settled on when the team
first argued it. The repository is private. The deployment mints a
deploy key into a `flux-system` Secret and publishes the public half
in status and on the console. This lets the user register the key at
the forge without ever handling private material. (The key is
read-write, because image-update automation will eventually commit
tag bumps back to the repository.)

The exact mechanism for minting the key is still open to change. Init
cannot write Secrets, because init runs before k3s. So the minting
will most likely belong to the machine operator instead. The public
key will surface through the same console-parity path that every
other boot fact takes.

Manifest authority resolves the way the earlier plan described: git
wins, and the seeded Machine and Cluster copies stay downstream of
it. That statement hides the milestone's sharpest question: Flux
syncs a repository that contains the Cluster document that declares
Flux itself. The answer to that question needs a drill, not an
argument. The dev repo for this drill already exists (`liken-dev`).
The lab proof is this loop: edit the repository, Flux applies the
Cluster, and the fleet stages and rolls the change.

Some questions stay deliberately unanswered here. Which controllers
ship in the install manifests? The source and kustomize controllers
are the floor; the helm and notification controllers are judgment
calls. How does per-slug parameter validation work in the CRD without
breaking the map schema's refusal properties? Is the deploy key
per-cluster or per-machine? This last question really asks what must
rotate when a machine is lost.
