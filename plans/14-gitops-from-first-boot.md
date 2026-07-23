# GitOps from first boot

Milestone 14 — Complete: the flux feature, the seed-once engine,
the deploy key, retraction, and the failure drills all landed, and
the manual teaches the loop

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

Toggling or re-pointing `flux` converges like any other feature
edit. The parameters live in the canonical rendered document, so a
changed repository produces a changed hash, and the fleet stages
that change. Features are restart-class: k3s reads them only at
process start, so the change applies by restarting k3s in place,
machine by machine, with no reboot. The drills proved this live.

`liken new` does not ask about flux, and this is a decision, not an
omission. The scaffold's feature question covers the parameterless
slugs. Enabling flux is not an answer; it is a handshake with the
forge, finished only when the forge holds the key the cluster mints
after boot. No interview can finish that, so the manual's guide
owns the flow instead.

## The seed-once engine

The engine is git's to own, and liken only plants it. This settles
the question of who owns the Flux installation, and the argument is
worth recording whole.

`flux bootstrap` commits the engine's own manifests into the
repository, so Flux manages itself from git and a Flux upgrade is a
commit. An earlier version of this plan rejected that shape and had
liken own the engine, pinned and released like every vendored
component. The rejection rested on the trust chain: the engine
would update outside the release's digest chain. That argument does
not survive inspection. A fleet that declares GitOps has handed the
repository the power to run any workload, including privileged
ones, so repository access is already cluster-root. The engine is
cluster content, and the repository owning cluster content is the
feature's entire point. Keeping the engine on the release would
only make Flux worse at being Flux: no commit-speed engine
upgrades, and a liken release between a deployment and every Flux
patch.

So the engine follows liken's own seed pattern, the way a Machine
manifest does: the image carries a pinned copy, the system plants
it exactly once, and the live side owns it from then on.

* The seed is `gotk-components.yaml` for the floor components, the
  source and kustomize controllers, fetched and pinned by the flux
  domain (`flux/VERSION`, `fetch.sh`). It only has to be good
  enough to reach the first sync.
* The repository carries its own `gotk-components.yaml` inside the
  synced path, so the first sync upgrades the engine to what the
  repository pins, and every later engine change is a commit.
  Component choice belongs to the repository too: a deployment that
  wants the helm-controller commits it. The vocabulary never grows
  a component parameter.
* The cluster operator plants the seed, with the same
  if-absent shape as the deploy key, run on every sweep. The probe
  is one object: the kustomize-controller Deployment in
  flux-system. That Deployment is the applier that heals everything
  else from git, so its absence means nothing can heal, and liken
  re-plants the whole seed. Present but broken stays git's problem
  on purpose; liken answers only for gone. Because the probe runs
  every sweep, a deleted engine heals in seconds, not at the next
  boot.
* The seed travels embedded in the operator binary (`go:embed`),
  because the cluster operator deliberately has no hostPath mounts.
  Embedded bytes live in the binary's read-only data segment,
  demand-paged and evictable, so the seed costs nothing resident;
  the apply path parses it transiently and lets the allocations
  die with the pass.
* Planting the seed creates CRDs, ClusterRoles, and bindings, and
  RBAC's escalation rule means the planter must be granted what
  those roles grant. The feature's seeded manifests deliver that
  installer grant, the same path as the minting Role, so the
  operator holds it only while the feature is declared. The grant
  is the feature: declaring GitOps declares that this operator may
  install the engine.

The ownership line is then clean, and one rule keeps it clean.
liken owns the ground forever: the flux-system namespace, the
minting Role, the deploy key, and the sync objects
(`GitRepository`, `Kustomization`), which init renders from the
declared parameters so that editing the Cluster document stays a
real act. Git owns the engine and everything above it. The
repository must never carry the sync objects, or git and liken
would fight over them; the manual owns that warning. The
`clusters/<cluster-name>` layout is a convention the manual
teaches, not a default the system derives: `path` defaults to the
repository root, because a derived default would let a rename
silently change what a fleet syncs.

The controller images are deliberately not baked into the OS image.
They are ordinary workload images pulled from a registry. This design
avoids the bootstrap deadlock that forced `iscsid` to be included in the
image. A fleet that cannot reach `ghcr.io` is the problem that
milestone 20's mirrors solve.

Init's part is one new trick: where every other feature's manifests
are seeded verbatim, init renders Flux's sync objects from the
declared parameters.

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
Flux itself. That question needed a live proof, not an argument,
and the proof ran on the GitOps lab (`gitops-cluster/`), against
its fleet repository (`github.com/liken-sh/liken-dev-cluster`).
The loop held: a commit to the repository moved the Cluster, and
the fleet staged and applied each change, feature edits by restart
and version edits by sequenced reboot.

The engine questions an earlier draft left open are settled above:
the seed carries the floor components only, and the repository
decides everything past the floor. (The flux-operator, a
meta-controller that manages the engine's lifecycle, was considered
and rejected: once git owns the engine, the operator manages
something that already has an owner, at the cost of an always-on
controller and a second manifest channel outside both git and the
release.)

Retraction is settled, and its design is the mirror of the
planter's. Removing the feature removes everything: the sync
objects, the engine, its CRDs and RBAC, and the namespace, the
deploy key included, so off means off and a re-enable mints a fresh
key. What the repository deployed stays running as orphans, because
stopping the sync must not undeploy anything. The teardown belongs
to the cluster operator's janitor alone, in a deliberate order:
kill the controllers, prove their pods are gone, then strip the
sync objects' finalizers and delete the rest. The order exists
because the engine's deletion finalizer garbage-collects everything
the repository ever applied; k3s's addon machinery must never
delete these objects, so a flux retraction removes its seeded files
only while k3s is down (the Teardown field in the feature
vocabulary carries this distinction). The janitor's rights are
standing in the operator's manifest, delete-only and name-held,
because rights delivered by the feature could never clean up after
the feature that delivered them.

The failure drills ran on the GitOps lab, and each one recovered:
a commit that cannot apply (the sync refuses loudly and the last
good state persists), poisoned `knownHosts` (the rescue is
repo-first, then a live edit; the operator heals the Secret in
seconds), a bad live edit to the repository URL (the sync reverts
it in seconds; git wins), key rotation (delete the Secret, register
the fresh half), a deleted engine (re-planted in six seconds), and
the full off-and-on cycle.

The drills surfaced one truth the manual must teach: server-side
apply tracks field ownership, and a live `kubectl` edit makes the
person a co-owner of the fields they touched. Git cannot delete a
field a person co-owns. A later git-side retraction then projects a
partial object, and the CRD's own validation refuses it, which
stops the sync loudly instead of applying half a retraction. The
recovery is to make the same edit live, which hands the object back
to git. The mirror also holds: a feature enabled only by live edit
does not flap off, because git cannot remove what it never owned.
Every rescue in this design is a live edit, so every rescue leaves
fingerprints, and the rescue guide must end with the step that
removes them.

The prune bomb has one last edge, and it stays with the user by
design. The synced path carries the Cluster document that declares
Flux itself. So a commit that drops that path, while the feature
stays declared and the engine stays alive, would let the engine's
garbage collector delete the Cluster and its Machines from the
live cluster. Retraction does not have this edge: the janitor
kills the controllers before any sync object dies, so disabling
the feature never fires the prune. liken adds no code guard for
the commit case. A finalizer
would stall deletion instead of refusing it, and it would wedge
every honest teardown. An operator-stamped annotation was
considered and set aside: git owns these documents, so the mark
belongs in the repository with them. The manual tells the user to
set the `kustomize.toolkit.fluxcd.io/prune: disabled` annotation on
the Cluster and Machine documents in the repository. The garbage
collector then leaves the marked documents in place, and removing
the fleet stays a deliberate live act.

The manual's guide (`docs/content/docs/guides/gitops.md`) closes
the milestone. It teaches the repository layout with the
`prune: disabled` mark in it, the never-commit-the-sync-objects
rule, the field-ownership rule for rescues, and the memory warning
that came with moving component choice to the repository: the
repository decides how many controllers run, and a 1 GB machine
carries the two floor controllers with little room past them.
