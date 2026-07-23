# GitOps from first boot

Milestone 14 — In progress: the feature parameters, the flux
vocabulary row, and the deploy key landed; the sync engine and the
end-to-end loop remain

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

This is the vocabulary's first feature with parameters, and the
parameter machinery landed with it. `FeatureConfig` is a plain map,
parsed leniently at every file door, because a document from a newer
parameter vocabulary must still parse on an older binary; the
refusals live where a verdict can be reported. The CRD types each
feature's configuration as a map of string parameters, because map
keys are never pruned, and CEL rules hold the shapes: parameterless
features stay exactly `{}`, and `flux` requires `repository` with
`path` and `branch` optional. The feature table in
`cluster/features.go` declares each feature's parameter names, and
the parity test holds the CRD's rules to that table.

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
first argued it, with its open questions now closed. The repository
is private. The cluster operator mints one deploy key for the whole
cluster into the `flux-system` Secret, and publishes the public half
in the Cluster's status (`status.flux.publicKey`). The user registers
that value at the forge without ever handling private material. (The
key is read-write, because image-update automation will eventually
commit tag bumps back to the repository.)

The key is per-cluster, not per-machine, because per-machine keys
would narrow nothing: every key would live in the same datastore
that every leader carries, so the datastore is the unit of exposure
either way. Rotation is one act: delete the Secret, and the next
sweep mints a fresh pair to register. The minting belongs to the
cluster operator because the credential is cluster-scoped, and the
sweep is the one writer of Cluster status. Init cannot do it anyway,
because init runs before k3s. The console does not show the key:
console parity covers the boot facts that init discovers, and this
key is a post-boot operator fact. The permission to write the Secret
travels with the feature itself, as a Role in the feature's own
seeded manifests, so the operator holds no standing Secret access on
a fleet that never declares GitOps.

Manifest authority resolves the way the earlier plan described: git
wins, and the seeded Machine and Cluster copies stay downstream of
it. That statement hides the milestone's sharpest question: Flux
syncs a repository that contains the Cluster document that declares
Flux itself. The answer to that question needs a live proof, not an
argument. The fleet repository for it exists
(`github.com/liken-sh/liken-dev-cluster`), and the GitOps lab
(`gitops-cluster/`) is the deployment that declares it. The proof is
this loop: edit the repository, Flux applies the Cluster, and the
fleet stages and rolls the change.

Two questions stay open. Which controllers ship in the install
manifests? The source and kustomize controllers are the floor; the
helm and notification controllers are judgment calls. And does liken
carry Flux's own install manifests at a pinned version, or carry the
flux-operator and render one FluxInstance? The operator decouples
Flux's version from liken's releases, at the cost of one more
always-on controller inside a tight memory envelope.
