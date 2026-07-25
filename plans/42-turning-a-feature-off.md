# Turning a feature off turns it off

Milestone 42 — Planned. Retraction becomes an ordered act with an
owner, so a feature that leaves `spec.features` stops running instead
of losing its controller and keeping its effects.

Declaring a feature is a runtime act. Init renders the boot
configuration, seeds the manifests, and loads the modules, and every
step has something that performs it. Retracting a feature is the
absence of that act. Init stops rendering the configuration, and
nothing performs the removal of what the previous document created.
The controller stops. What the controller programmed stays.

A lab drill measured this on one leader across the whole feature
vocabulary. Every feature leaves something behind, and three of them
leave the cluster in a worse condition than a leak.

`network-policy` keeps its packet filtering. The rules are not
conservative leftovers. Each per-pod chain is keyed on a pod address,
and the address outlives the pod, so the dead policy is enforced
against whatever workload receives that address next.

`servicelb` strands its DaemonSet, whose replacement pods the kubelet
then rejects forever, because the sysctl allowlist leaves with the
cloud controller. Its LoadBalancer Services keep a finalizer that no
controller will ever remove, so deleting one hangs until a person
strips the finalizer by hand.

`traefik` strands its whole Helm release. Nothing else requires helm,
so removing `traefik` from `spec.features` removes helm in the same
edit, and the deploy controller then deletes the HelmChart into a
cluster whose helm controller has already stopped. The HelmChart holds
on its own finalizer and the release keeps running.

## The edit is not the problem, the partial apply is

It is tempting to refuse the edit. The invariant that matters cannot
be seen from the object, though. Whether a LoadBalancer Service exists
is cluster state, and making that unrepresentable at admission means a
validating webhook. liken should not have one. A webhook that fails
closed blocks the edit that would repair the cluster, a webhook that
fails open is not a guarantee, and both break the rule that a machine
converges from documents on disk with no in-cluster helper.

The damage is not that the edit was accepted. The damage is that the
edit applied halfway, and left the cluster in a condition that neither
the old document nor the new document describes. That is the state to
make unrepresentable, and the property that removes it is atomicity.
Either the feature drains and stops, or nothing moves and a condition
states what is holding it.

Read that way, a retraction that waits is not an invalid state. It is
an unconverged one, and liken already models unconverged everywhere:
staged, `AwaitingTurn`, `Blocked`, `RejectedLastBoot`. The spec states
what the deployment wants. The status states why the fleet has not
reached it.

## The manifests directory has two authors

The first fault is liken's own, and it is a question of ownership.

k3s has a working teardown for the components it bundles. When the
disable list names a component whose manifest is on disk, the deploy
controller reads the file, deletes what the file declares, and removes
the file. The drill confirmed this: a retraction that converges by an
in-place restart removes every metrics-server object, the APIService
included.

`seedClusterState` wipes the whole manifests directory at every boot
and copies the image's seed back. The directory holds two authors'
files: liken's own manifests, and the components k3s stages there. The
wipe removes both, so a boot that disables a component finds no file
to act on, and the objects survive. The drill measured this too. After
a reboot-converged retraction, the metrics-server Deployment was still
running, the APIService was still available, and `kubectl top node`
still worked. The feature was not a leftover. It was still the running
feature.

liken refreshes the files liken authored, and leaves k3s's staged
files alone. This restores k3s's own machinery rather than duplicating
it, and it makes the two convergence paths agree. Today the same
retraction means two different things depending on what else the edit
touched, which is a fact no operator could reasonably predict.

Three comments in the repository state that k3s deletes an addon's
objects when its manifest file disappears while k3s runs. The drill
disproved this: a manifest was applied, its file was deleted, and six
watcher cycles later both the object and the Addon were still present.
The janitor still does its job, and it is the only thing that does.
Those comments are wrong and go with this change.

## Retraction drains before it stops

The second fault is ordering, and the escape hatch found in the lab is
the proof. Re-declaring helm alone, after the traefik retraction had
stranded everything, was enough. The controller came back, ran its
remove handler, uninstalled the release, and the CRDs went with it.
liken knew nothing about traefik and did not need to. The teardown
worked because it ran through the component that owned the objects,
while that component was alive.

So retraction becomes two phases. The cluster operator drains first,
in the cluster, while the owning controller still runs. Only when the
drain is complete may machines stage the retraction and take their
turns. Retracting traefik means deleting its HelmCharts while helm
still runs, letting helm uninstall the release, and stopping both
afterwards. The automatic dependency stays exactly as it is, and the
trap disappears because the order is right.

This is not a new idea in liken. `TeardownJanitor` already does it for
flux, where the objects had to go in a deliberate order with the
controllers killed first. It was written as the exception. It is the
general case, found early.

The barrier is the cost. Draining happens in the cluster and stopping
happens on each machine, so no machine may apply a retraction until
the drain is done. The rollout conductor already sequences fleet
disruption and is where the barrier belongs.

## The vocabulary states each feature's retraction contract

The contract is not a list of objects to delete. liken must never
learn what a traefik release contains or what a klipper-lb pod does,
because a list like that goes stale the first time k3s renames
something, and it goes stale silently.

Each feature answers two questions instead. What must be true in the
cluster before this feature may stop, and who performs the drain.
`FeatureTeardown` is that field in embryo, with two values.

* `traefik` drains by deleting its HelmCharts. helm decides what that
  means.
* `helm` requires that no HelmChart remains. traefik's drain is what
  satisfies it.
* `servicelb` requires that no LoadBalancer Service remains. liken
  cannot drain it, because those Services belong to the deployment.
* `metrics-server` needs nothing once the manifests directory has one
  author. k3s's disable path is the teardown.
* `network-policy`, `iscsi`, and `nfs` leave kernel state and take the
  reboot class, below.
* `flux` keeps the ordered teardown it already has.

Where liken cannot drain, it refuses and explains. A precondition that
is not met holds the retraction as `Blocked`, with a reason that names
what the deployment must remove. This is a much better outcome than an
undeletable Service and a DaemonSet that outlives it.

Invariants that the document proves about itself stay at admission,
where they can be truly unrepresentable. Retracting helm while traefik
is declared is visible in the object, so CEL refuses it and there is
no state to reconcile.

## Kernel state takes the reboot class

A boot is the one teardown liken already trusts. It clears netfilter
chains and ipsets, mounts, iSCSI sessions, and loaded modules, with no
per-feature teardown code and no reimplementation of another project's
cleanup. The drill confirmed the clearing: after a boot, the node had
no kube-router rules, no chains, and no ipsets.

This was not available before, because a reboot leaked the manifest
components. With one author for the manifests directory it becomes
available, and it is the right answer for the features that touch the
host.

It also does more than clean up afterwards. A machine on the reboot
class never stops the controller and then lives with its leftovers. It
runs the controller, waits for its turn, and boots without it. The
window in which stale rules enforce a dead policy does not get
shortened. It stops existing.

## What the lab measured

One leader, 1 GB, release `v2026.07.24-004-2-g1bc9684-dirty`, k3s
`v1.36.2+k3s1`, with the lab storage fixture for the mount and session
half.

A manifest deleted from the auto-deploy directory while k3s ran left
its object and its Addon in place after six watcher cycles.

metrics-server retracted by a features-only edit left nothing:
Deployment, APIService, and all seven Addons gone. The same retraction
carried on a reboot left all of it, and `kubectl top node` still
answered.

traefik retracted the ordinary way left two HelmCharts terminating on
`wrangler.cattle.io/on-helm-chart-remove`, the Deployment running, its
Service, three ServiceAccounts, two ConfigMaps, two Helm release
Secrets, and twenty-five CRDs. Re-declaring helm unwound all of it.

servicelb retracted left its DaemonSet, whose pods the kubelet
rejected with `forbidden sysctl: "net.ipv4.ip_forward" not
allowlisted`. `kubectl delete svc` timed out and left the Service
terminating on `service.kubernetes.io/load-balancer-cleanup`. The
LoadBalancer address kept answering through kube-proxy, maintained by
nothing.

network-policy retracted left 134 filter rules, 13 chains, and one
ipset, and traffic stayed blocked. A new pod that no policy selected,
given a deleted pod's address, was unreachable through a chain named
for the deleted pod. A boot left none of it.

iscsi retracted left a session `LOGGED_IN` in the kernel with no
iscsid process on the host, reporting `Internal iscsid Session State:
Unknown`. nfs retracted left the mount working, and a new pod mounted
the same export afterwards, because the module stays loaded and
`mount.nfs` always ships in the image.

## The manual

Retraction is an operational flow, so the feature guide states what
retracting a feature does and what it waits for. The Cluster reference
regenerates from the schema, so the `features` description carries the
preconditions, and the flux paragraph's existing statement that
retraction stops the sync without undeploying becomes one case of a
general rule rather than a special note.

## Open question

Whether the barrier is per-feature or per-edit. Per-feature looks
right, with an edit converging when all of its retractions have. An
edit that retracts two features whose drains interact is the case that
decides it, and it is better found now than in the rollout code.
