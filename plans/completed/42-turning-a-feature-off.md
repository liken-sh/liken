# Turning a feature off turns it off

Milestone 42. Completed. Retraction becomes an ordered act with an
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

## The partial apply is the problem

It is tempting to refuse the edit. The invariant that matters cannot
be read from the object, though. Whether a LoadBalancer Service exists
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
what the deployment declares. The status states why the fleet has not
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

## Retraction waits until the cluster is ready for it

The second fault is ordering, and the escape hatch found in the lab is
the proof. Re-declaring helm alone, after the traefik retraction had
stranded everything, was enough. The controller came back, ran its
remove handler, uninstalled the release, and the CRDs went with it.
liken performed no traefik-specific step, and needed none. The teardown
worked because it ran through the component that owned the objects,
while that component was alive.

liken does not have to perform that teardown, then. It has to order
it. So each pass, the machine operator derives the document it stages:
the desired document, with every feature added back whose retraction
the cluster is not ready for. That reduced document is what it hashes,
stages, and boots under. A machine that reboots in the middle of a
retraction therefore comes back running the held feature, which is
what the hold is for.

The order then falls out of the preconditions alone, with no rule
about the dependency graph anywhere. Retracting traefik leaves its
HelmCharts in place for a moment, so helm's precondition fails and
helm is held back while traefik stops. k3s deletes the charts, the
Helm controller uninstalls the release, the precondition passes, and
the next pass retracts helm. The automatic dependency stays as it is,
and the stranded release does not happen, because the order is right.

This is not a new idea in liken. `TeardownJanitor` already does it for
flux, where the objects had to go in a deliberate order with the
controllers killed first. It was written as the exception. It is the
general case, found early.

The barrier costs less than it first looked. An earlier sketch had the
cluster operator publish what was held and the machine operator honour
it, which needs a handshake between two programs, a new status field,
and a guard against acting on a stale answer. None of that is
necessary. The machine operator already talks to the API and already
selects what to stage, so it evaluates the preconditions itself and
the handshake disappears.

## The vocabulary states each feature's retraction contract

The contract is not a list of objects to delete. liken must never
hold a list of what a traefik release contains or what a klipper-lb
pod does,
because a list like that goes stale the first time k3s renames
something, and it goes stale silently.

Each feature answers one question instead: what must be true in the
cluster before this feature may stop. `FeatureTeardown` is the first form
of that field, with two values.

* `traefik` states nothing. Stopping it is what clears helm's
  precondition, because k3s deletes the HelmCharts when the component
  leaves the disable list.
* `helm` requires that no HelmChart remains.
* `servicelb` requires that no LoadBalancer Service remains. liken
  cannot drain it, because those Services belong to the deployment.
* `metrics-server` needs nothing once the manifests directory has one
  author. k3s's disable path is the teardown.
* `network-policy`, `iscsi`, and `nfs` leave kernel state and take the
  reboot class, below.
* `flux` keeps the ordered teardown it already has.

Where liken cannot clear a precondition, it refuses and explains. A
precondition that is not met holds the retraction as `Blocked`, with a
reason that names what the deployment must remove. This is a much
better outcome than an undeletable Service and a DaemonSet that
outlives it.

Every precondition here needs cluster state, so none of them can run
at admission. A CRD judges only what the object itself says, and no
feature's readiness to stop is visible in the document. There is also
nothing left for a schema rule to refuse: a requirement cannot be
retracted out from under the feature that needs it, because
`EnabledFeatures` keeps it enabled for as long as its dependent is
declared.

## Kernel state takes the reboot class

A boot is the one teardown liken already relies on. It clears netfilter
chains and ipsets, mounts, iSCSI sessions, and loaded modules, with no
per-feature teardown code and no reimplementation of another project's
cleanup. The drill confirmed the clearing: after a boot, the node had
no kube-router rules, no chains, and no ipsets.

This was not available before, because a reboot leaked the manifest
components. With one author for the manifests directory it becomes
available, and it is the right answer for the features that touch the
host.

It also does more than clean up afterwards. A machine on the reboot
class never stops the controller and then runs with its leftovers. It
runs the controller, waits for its turn, and boots without it. The
window in which stale rules enforce a dead policy does not get
shortened. It stops existing.

## What the lab measured before the work

One leader, 1 GB, release `v2026.07.24-004-2-g1bc9684-dirty`, k3s
`v1.36.2+k3s1`, with the lab storage fixture for the mount and session
half.

A manifest deleted from the auto-deploy directory while k3s ran left
its object and its Addon in place after six watcher cycles.

metrics-server retracted by a features-only edit left nothing:
Deployment, APIService, and all seven Addons gone. The same retraction
applied by a reboot left all of it, and `kubectl top node` still
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

## What the lab measured after it

The same drills, on a machine that upgraded from the release that
produced the numbers above, so the clusterState disk still held the
old layout.

The upgrade itself swept liken's six manifests off the top of the
auto-deploy directory on the first boot, and `coredns.yaml` came
through byte for byte.

metrics-server retracted across a reboot left no Deployment, no
APIService, and no addons, and `kubectl top node` answered "Metrics API
not available". k3s emptied its own `metrics-server` directory, which is
the teardown that the old wipe used to prevent.

Retracting traefik stopped traefik and held helm, naming both
HelmCharts. k3s deleted the charts, `helm-delete-traefik-crd` ran, and
helm stopped on a later pass. No object stayed in Terminating, and the
cluster ended with no traefik CRDs and no release secrets.

Retracting servicelb with a LoadBalancer Service present reported
`Blocked` with reason `RetractionBlocked`, naming `default/lbserver`.
The DaemonSet stayed healthy, the address kept answering, and the
Service deleted in under a second. The retraction then finished with no
further edit.

Retracting network-policy drained the node and rebooted it. Afterwards
the node had no kube-router rules, chains, or ipsets, and the
NetworkPolicy that remained declared was not enforced, which is
flannel's own behaviour without the controller.

Retracting iscsi and nfs also rebooted. Afterwards no iscsi or nfs
module was loaded, no iSCSI session remained, and no NFS mount
remained, against a live session and a live mount before the edit.

## The manual

Retraction is an operational flow, so the feature guide states what
retracting a feature does and what it waits for. The Cluster reference
regenerates from the schema, so the `features` description states the
preconditions, and the flux paragraph's existing statement that
retraction stops the sync without undeploying becomes one case of a
general rule rather than a special note.

## The barrier is per-feature

The open question was whether a hold covers one feature or one edit.
Per-feature is what the work settled on, and the traefik case is what
settles it. That edit retracts two features whose readiness differs:
traefik may stop at once, and helm may not stop until traefik's charts
are gone. A per-edit hold would keep both running until the whole edit
was ready, and nothing would ever make it ready, because stopping
traefik is what clears helm's precondition. Per-feature holds let the
edit converge one feature at a time, and the edit is done when its last
hold clears.
