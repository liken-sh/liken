# GitOps from first boot

Milestone 14 — Not started

GitOps from first boot, as an opt-in feature. An earlier version of
this plan refused to vendor an engine and sketched generic
primitives instead — a seed channel for delivering manifests
alongside the Machine manifest, and a key-minting primitive — with
Flux arriving as content over that channel. Milestone 17 changed the
calculus: the OS now has a vocabulary of optional features, and the
feature mechanism *is* the seed channel. Init already stages each
feature's manifests in the image and seeds them into k3s's
auto-deploy directory exactly when the cluster document declares the
feature, and the retraction janitor already cleans up when a
declaration goes away. What this milestone adds is one row in that
vocabulary:

    spec:
      features:
        flux:
          repository: ssh://git@forge.example/fleet.git
          path: clusters/lab
          branch: main

The slug names the project, not the capability, and that is a
deliberate refinement of the vocabulary's naming rule. The rule
exists so implementations can change behind a stable name, and for
iscsi that holds: the kernel interface is the capability and
open-iscsi is swappable behind it. A GitOps engine is different: its
in-cluster CRDs (GitRepository, Kustomization) and its repository
conventions are the interface the user builds their whole repo
against, so a generic gitops slug would promise a swappability that
could never be honored. Naming it flux is the honest contract. A
deployment that wants a different engine wants a different feature.

This is the vocabulary's first feature with parameters, which makes
it the pathfinder for machinery milestone 17 built empty slots for.
FeatureConfig stops being one shared empty struct: validation grows a
per-slug shape (repository required; path and branch defaulted), and
the CRD's features schema — a map with one shared value schema, the
form the admission drills forced — needs per-slug parameter rules,
which likely means CEL over the map values. That wants the same live
drills against a scratch CRD that settled the original schema,
because apiextensions' pruning has already surprised this plan family
once. Toggling or re-pointing flux converges like any feature edit:
the parameters live in the canonical rendered document, so a changed
repository is a changed hash, staged and applied by reboot. That is
heavyweight for what amounts to re-rendering one object, and it is
also consistent with everything else the document declares; the
first live re-point will say whether it stays that way.

What ships in the image is small, which keeps the kitchen-sink rule
intact: manifests only. Flux's install manifests (pinned to a flux
version the way every vendored input is pinned) and a sync object
rendered at boot from the declared parameters — the GitRepository
and Kustomization that do what `flux bootstrap` would have done.
The controller images are deliberately not baked into the OS image:
they are ordinary workload images pulled from a registry, with none
of the bootstrap deadlock that forced iscsid to ride the image, and
a fleet that can't reach ghcr.io is what milestone 20's mirrors are
for. Init's part grows one new trick: today it seeds feature
manifests by copying them verbatim, and flux's sync object needs the
declared parameters rendered in.

Identity keeps the design settled when this plan was first argued: a
private repository, with the deployment minting a deploy key into a
flux-system Secret and publishing the public half in status and on
the console, so the user registers it at the forge without ever
handling private material. (Read-write, since image-update
automation eventually commits tag bumps back.) The mechanism moves
with the times: init cannot write Secrets (it runs before k3s), so
the minting most likely belongs to the machine operator, with the
public key surfacing through the same console-parity path every
other boot fact takes.

Manifest authority resolves the way the old plan said: git wins, and
the seeded Machine and Cluster copies are downstream of it. That
sentence hides the milestone's sharpest question — flux syncing a
repo that contains the Cluster document that declares flux — and the
answer needs a drill, not an argument: the dev repo for this already
exists (liken-dev), and the loop (edit the repo, flux applies the
Cluster, the fleet stages and rolls) is the lab proof.

Open questions, deliberately unanswered here: which controllers ship
in the install manifests (source and kustomize are the floor; helm
and notification are judgment calls); exactly how per-slug parameter
validation reads in the CRD without wrecking the map schema's
refusal properties; and whether the deploy key is per-cluster or
per-machine, which is really a question about what gets rotated when
a machine is lost.
