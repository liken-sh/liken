# GitOps from first boot

Milestone 14. Completed. A cluster declares the `flux` feature, and
liken plants the Flux engine once, mints the deploy key, renders the
sync objects, and removes all of it when the declaration goes away.

GitOps from first boot is an opt-in feature. An earlier version of this
plan did not vendor an engine. It described two generic primitives
instead: a seed channel that delivers manifests next to the Machine
manifest, and a key-minting primitive. In that design, Flux arrived as
content over the seed channel.

Milestone 17 changed this plan. The OS now has a vocabulary of optional
features, and the feature mechanism is the seed channel. Init stages
each feature's manifests in the image. Init seeds these manifests into
k3s's auto-deploy directory when the cluster document declares the
feature. The retraction janitor removes a feature's manifests when its
declaration goes away. This milestone adds one row to that vocabulary:

    spec:
      features:
        flux:
          repository: ssh://git@forge.example/fleet.git
          path: clusters/lab
          branch: main

The slug names the project, not the capability. The vocabulary's naming
rule exists so that an implementation can change behind a stable name.
The rule holds for `iscsi`, because the kernel interface is the
capability and `open-iscsi` can be replaced behind it. A GitOps engine
is different. Its in-cluster CRDs (`GitRepository`, `Kustomization`) and
its repository conventions are the interface that the user builds the
whole repository against. A generic `gitops` slug would claim a
swappability that the design cannot give. The name `flux` states the
contract. If a deployment needs a different engine, it needs a
different feature.

`flux` is the vocabulary's first feature with parameters, and the
parameter machinery landed with it. `FeatureConfig` is a plain map, and
each file door parses it leniently, because a document from a newer
parameter vocabulary must still parse on an older binary. The refusals
are at the points where a verdict can be reported. The CRD types each
feature's configuration as a map of string parameters, because map keys
are never pruned. CEL rules hold the shapes: a parameterless feature
stays exactly `{}`, and `flux` requires `repository`, with `path` and
`branch` optional. The feature table in `cluster/features.go` declares
each feature's parameter names, and the parity test holds the CRD's
rules to that table.

A change to `flux` converges like any other feature edit. The
parameters are part of the canonical rendered document, so a changed
repository gives a changed hash, and the fleet stages that change.
Features are restart-class: k3s reads them only when the process
starts. The change applies when k3s restarts in place, machine by
machine, with no reboot. The drills confirmed this on live machines.

`liken new` does not ask about flux. The scaffold's feature question
covers the parameterless slugs. To enable flux, the user must also
register a key at the forge, and the cluster mints that key only after
boot. An interview cannot finish that step, so the manual's guide owns
the flow.

## The seed-once engine

Git owns the engine, and liken only plants it.

`flux bootstrap` commits the engine's own manifests into the
repository, so Flux manages itself from git and a Flux upgrade is a
commit. An earlier version of this plan rejected that shape and gave
liken the engine, pinned and released like every other vendored
component. That rejection depended on the trust chain: the engine would
update outside the release's digest chain. The argument does not hold.
A fleet that declares GitOps has already given the repository the power
to run any workload, including a privileged one, so repository access
is already cluster-root. The engine is cluster content, and the
repository owning cluster content is the purpose of the feature.
Keeping the engine on the release would also stop commit-speed engine
upgrades, and it would put a liken release between a deployment and
every Flux patch.

The engine therefore follows liken's own seed pattern, the same pattern
as a Machine manifest: the image holds a pinned copy, the system
plants it exactly once, and the live side owns it after that.

* The seed is `gotk-components.yaml` with the floor components, which
  are the source controller and the kustomize controller. The flux
  domain fetches and pins it (`flux/VERSION`, `fetch.sh`). The seed
  only has to be good enough to reach the first sync.
* The repository holds its own `gotk-components.yaml` inside the
  synced path. The first sync upgrades the engine to the version the
  repository pins, and every later engine change is a commit. Component
  choice belongs to the repository too: a deployment that needs the
  helm-controller commits it. The vocabulary never gets a component
  parameter.
* The cluster operator plants the seed on every sweep, with the same
  if-absent shape as the deploy key. The probe is one object: the
  kustomize-controller Deployment in flux-system. That Deployment
  applies everything else from git, so its absence means that nothing
  can heal, and liken plants the whole seed again. An engine that is
  present but broken stays git's problem. liken answers only for an
  engine that is gone. The probe runs every sweep, so a deleted engine
  comes back in seconds and does not wait for the next boot.
* The seed is embedded in the operator binary (`go:embed`), because the
  cluster operator has no hostPath mounts. Embedded bytes are in the
  binary's read-only data segment, demand-paged and evictable, so the
  seed costs no resident memory. The apply path parses the seed
  transiently, and the allocations are freed with the pass.
* Planting the seed creates CRDs, ClusterRoles, and bindings. RBAC's
  escalation rule requires the planter to hold what those roles grant.
  The feature's seeded manifests deliver that installer grant, on the
  same path as the minting Role, so the operator holds the grant only
  while the feature is declared. Declaring GitOps declares that this
  operator may install the engine.

The ownership boundary is then simple, and one rule keeps it simple.
liken owns the ground permanently: the flux-system namespace, the
minting Role, the deploy key, and the sync objects (`GitRepository`,
`Kustomization`), which init renders from the declared parameters so
that an edit to the Cluster document stays a real act. Git owns the
engine and everything above it. The repository must never hold the
sync objects, or git and liken would both write them. The manual owns
that warning. The `clusters/<cluster-name>` layout is a convention that
the manual explains, not a default that the system derives. `path`
defaults to the repository root, because a derived default would let a
rename change what a fleet syncs with no visible edit.

The controller images are not baked into the OS image. They are
ordinary workload images pulled from a registry. This design avoids the
bootstrap deadlock that forced `iscsid` into the image. A fleet that
cannot reach `ghcr.io` is the problem that milestone 20's mirrors
solve.

Init does one new thing for this feature. Every other feature's
manifests are seeded verbatim, but init renders Flux's sync objects
from the declared parameters.

Identity keeps the design this plan settled on first, and its open
questions are now closed. The repository is private. The cluster
operator mints one deploy key for the whole cluster into the
`flux-system` Secret, and publishes the public half in the Cluster's
status (`status.flux.publicKey`). The user registers that value at the
forge and never handles private material. The key is read-write,
because image-update automation will later commit tag changes back to
the repository.

The key is per-cluster, not per-machine. Per-machine keys would narrow
nothing, because every key would be in the same datastore that every
leader holds, so the datastore is the unit of exposure either way.
Rotation is one act: delete the Secret, and the next sweep mints a
fresh pair to register. The minting belongs to the cluster operator,
because the credential is cluster-scoped and the sweep is the one
writer of Cluster status. Init cannot do it, because init runs before
k3s. The console does not show the key: console parity covers the boot
facts that init discovers, and this key is a post-boot operator fact.
The permission to write the Secret travels with the feature itself, as
a Role in the feature's own seeded manifests, so the operator holds no
standing Secret access on a fleet that never declares GitOps.

Manifest authority resolves the way the earlier plan described: git
wins, and the seeded Machine and Cluster copies stay downstream of it.
Flux therefore syncs a repository that contains the Cluster document
that declares Flux itself. That loop needed a live proof, and the proof
ran on the GitOps lab (`gitops-cluster/`) against its fleet repository
(`github.com/liken-sh/liken-dev-cluster`). The loop held: a commit to
the repository moved the Cluster, and the fleet staged and applied each
change, feature edits by restart and version edits by sequenced reboot.

The seed holds the floor components only, and the repository
determines everything past the floor. The flux-operator, a
meta-controller that
manages the engine's lifecycle, was considered and rejected. Once git
owns the engine, the flux-operator manages something that already has
an owner, at the cost of an always-on controller and a second manifest
channel outside both git and the release.

Retraction is the reverse of planting. Removing the feature removes
everything: the sync objects, the engine, its CRDs and RBAC, the
namespace, and the deploy key. Off means off, and a re-enable mints a
fresh key. What the repository deployed keeps running as orphans,
because stopping the sync must not undeploy anything. The teardown
belongs to the cluster operator's janitor alone, in this order: stop
the controllers, confirm that their pods are gone, then strip the sync
objects' finalizers and delete the rest. The order is necessary because
the engine's deletion finalizer garbage-collects everything the
repository ever applied. k3s's addon machinery must never delete these
objects, so a flux retraction removes its seeded files only while k3s
is down. The Teardown field in the feature vocabulary records this
distinction. The janitor's rights are standing rights in the operator's
manifest, delete-only and held by name, because rights delivered by the
feature could not clean up after the feature that delivered them.

The failure drills ran on the GitOps lab, and each one recovered:

* A commit that cannot apply. The sync refuses loudly and the last good
  state stays.
* Poisoned `knownHosts`. The rescue is repo-first, then a live edit,
  and the operator heals the Secret in seconds.
* A bad live edit to the repository URL. The sync reverts it in
  seconds, because git wins.
* Key rotation. Delete the Secret, then register the fresh public half.
* A deleted engine. The operator planted it again in six seconds.
* The full off-and-on cycle.

The drills showed one behavior that the manual must state. Server-side
apply tracks field ownership, and a live `kubectl` edit makes the
person a co-owner of the fields they touched. Git cannot delete a field
a person co-owns. A later git-side retraction then projects a partial
object, and the CRD's own validation refuses it, which stops the sync
loudly instead of applying half a retraction. The recovery is to make
the same edit live, which gives the object back to git. The reverse
also holds: a feature enabled only by a live edit does not turn off,
because git cannot remove what it never owned. Every rescue in this
design is a live edit, so every rescue leaves fields that a person
owns, and the rescue guide must end with the step that gives those
fields back.

One risk stays with the user by design. The synced path holds the
Cluster document that declares Flux itself. A commit that drops that
path, while the feature stays declared and the engine stays alive, lets
the engine's garbage collector delete the Cluster and its Machines from
the live cluster. Retraction does not have this risk, because the
janitor stops the controllers before any sync object dies, so disabling
the feature never starts the prune. liken adds no code guard for the
commit case. A finalizer would stall deletion instead of refusing it,
and it would block every honest teardown. An operator-stamped
annotation was considered and set aside, because git owns these
documents and the mark belongs in the repository with them. The manual
tells the user to set the `kustomize.toolkit.fluxcd.io/prune: disabled`
annotation on the Cluster and Machine documents in the repository. The
garbage collector then leaves the marked documents in place, and
removal of the fleet stays a deliberate live act.

The manual's guide (`docs/content/docs/guides/gitops.md`) closes the
milestone. It gives the repository layout with the `prune: disabled`
mark in it, the rule that the repository never holds the sync
objects, the field-ownership rule for rescues, and the memory warning
that came with moving component choice to the repository: the
repository determines how many controllers run, and a 1 GB machine runs
the two floor controllers with little room past them.
