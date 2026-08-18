# Node taints on the Machine

Milestone 52. Completed. A Machine declares its node taints beside its
node labels, init applies them when the node first registers, and the
operator reconciles them live.

## The problem

A label attracts workloads, and a taint repels them. A machine that is
dedicated to one job needs both halves: the GPU label draws the encode
jobs in, and a taint keeps everything else out. Today the taint comes
through kubectl, outside the Machine document, so a reinstalled machine
comes back untainted. Its first minutes accept exactly the pods the
taint exists to repel. Milestone 21 made this argument for labels:
scheduling identity belongs in the manifest. A taint is the other half
of that identity.

## The declaration

The declaration is spec.nodeTaints, a list of key, value, and effect,
in the same shape as the Node's own spec.taints. A map from key to
value, the shape nodeLabels uses, cannot hold a taint, because a
taint's identity is its key plus its effect, and one key may have two
effects at once. The list is a map-typed list on key and effect, so
the API server refuses a duplicate instead of trusting the caller, and
a merge on the pair behaves like the map that labels have.

The effect is an enum of the three values Kubernetes defines:
NoSchedule, PreferNoSchedule, and NoExecute. The value is optional,
because a taint often needs no value: the key and effect alone say
"stay off this machine".

## Two mechanisms, like labels

A taint reaches the Node the same two ways a label does. Init renders
the list into the k3s boot drop-in as node-taint entries, which k3s
writes into the registerWithTaints field of the kubelet configuration
it generates. Labels reach the kubelet on its command line; taints do not.
The operator then reconciles the list live, in the same pass that
reconciles labels.

Registration matters more for a taint than for a label. A blank node
attracts by accident; an untainted node accepts by accident, and the
pods it accepts in its first minutes are running workloads that a
later taint would have to evict. Registration taints close that
window: the node registers already repelling.

One asymmetry with labels: the kubelet applies registration taints
only when it creates the Node object, on a first boot or after a
reinstall. On every later boot the Node already exists and the
setting does nothing. So the drop-in covers exactly the fresh-node
window, and the operator covers everything after. The drop-in uses the
node-taint+ append form, like node-label+, so it can never claim the
whole list away from a static file. Each entry renders as
key=value:Effect, or key:Effect when the value is empty, which is the
kubelet's own taint grammar.

## Live reconciliation and ownership

The operator reconciles taints in the same pass as labels, with the
same ownership record: an annotation on the Node itself,
liken.sh/node-taints, naming the key:Effect pairs the operator
manages. A pair in the annotation but no longer in the spec gives the
operator permission to remove that taint. A taint applied by hand or
by another controller is in neither the spec nor the annotation, and
the operator never changes it.

The write differs from the labels write in one mechanical way. Labels
are a map, and a JSON merge patch on a map touches only the keys it
names. Taints are an array, and a merge patch replaces an array
whole. So the operator writes the full merged list: the spec's taints
upserted, the retracted owned pairs dropped, and every foreign taint
kept. A full-list write can race the node lifecycle controller, which
writes the same array when a node goes unready. The patch therefore
includes the resourceVersion the operator read, so a concurrent write
fails with a conflict instead of erasing the controller's taint. The
failed pass reports ApplyFailed, and the next pass reads the Node
again and patches again.

A NodeTaintsApplied condition reports the outcome. Taints never count
as spec drift: like labels, they reconcile live, so a reboot would
apply nothing that is not already applied.

## What admission refuses

The label rules protect the boot: under NodeRestriction, a kubelet
given a forbidden label refuses to start, so the schema refuses what
the kubelet would refuse. Registration taints have no such gate, so a
mistyped taint cannot stop a boot. The taint rules protect against a
different conflict: a controller that manages the same key live.

The node lifecycle controller owns the node.kubernetes.io taint keys.
It adds not-ready and unreachable when a node fails and removes them
when it recovers, and the scheduler adds and removes the pressure
taints beside them. A spec that declares one of these keys puts the
operator in a loop against a controller, each removing what the other
applies. So admission refuses the kubernetes.io and k8s.io
namespaces. Note the reversal from labels, which permit
node.kubernetes.io because the kubelet's own label allowlist does:
that namespace is safe for labels and owned by a controller for
taints.

One kubernetes.io key stays permitted: node-role.kubernetes.io. The
dedicated control-plane machine is the oldest use of a taint, no
controller manages that key, and the demotion cleanup reads only the
label of that name, never the taint.

Admission refuses the liken.sh namespace, because the operating
system owns its own vocabulary. Key and value take the same shape
rules as a label key and a label value.

NoExecute stays permitted, with its consequence stated in the schema
description: it evicts running pods the moment it lands, and a
DaemonSet pod is evicted with the rest unless it tolerates the taint,
because the DaemonSet controller adds tolerations only for the
lifecycle keys. The machine operator is such a DaemonSet. Its pod
tolerates every taint (operator: Exists in its manifest, which its
job as the machine's manager already requires), so it schedules onto
a tainted fresh node and NoExecute never evicts it. A retraction
therefore still works on a node that NoExecute has emptied, because
the one pod that removes taints is the one pod that stays.

## The lab

node-4 declares a standing example beside its drill label:
guid.foo/drill=node-taints with PreferNoSchedule, the effect that
biases the scheduler without ever blocking a drill pod.

The lab proved every path on a two-node fleet reinstalled from
working-tree media. The admission drills all refused with messages
that name the fix: node.kubernetes.io/unreachable, a liken.sh key, a
malformed key, and an effect outside the enum were each refused at the
API server, node-role.kubernetes.io/database with NoSchedule passed, and
a duplicate key-and-effect pair was refused by the map-typed list
itself. One detail: an effect outside the enum is a structural
error, and the API server skips the CEL rules once a structural error
exists, so that request reports the enum failure plus a note that the
other rules were not checked.

The live paths converged at two speeds, and the difference is the
trigger. A spec edit wakes the operator through its Machine watch, so
applying, retracting, and NoExecute cleanup each converged in under a
second. A hand edit on the Node wakes nothing, so a taint removed
with kubectl taint came back on the 10-second ticker, measured at
6.2, 9.9, and 9.9 seconds across three removals. A foreign taint
survived seven passes with the Node's resourceVersion pinned the
whole time: the operator wrote nothing at all. A retraction removed
the declared taint, kept the foreign one, and deleted the ownership
annotation key outright. No resourceVersion conflict was observed in
any window.

The NoExecute drill measured the eviction and the survivors. The
taint evicted every pod without a toleration in under a second,
including a DaemonSet pod, and the two liken DaemonSets (the machine
operator and machine-logs, each of which tolerates every taint) ran
on with zero restarts. The operator kept reconciling while the taint
stood: a hand-removed NoExecute taint came back in 2.0 seconds, and
in that two-second gap the scheduler placed fresh pods on the node
that the reassert then evicted, which is direct proof that the
DaemonSet controller adds no toleration for a custom key. The
retraction let the evicted workloads back onto the node in under a
second.

The registration drill deleted the Node object and rebooted the
guest. The fresh Node registered with the drill taint beside the
lifecycle controller's own not-ready taint, before the operator's
first pass and with no ownership annotation yet. The serial console
showed init render the node-taint+ block at 1.6 seconds, and the
generated kubelet configuration included the registerWithTaints field,
while the kubelet's command line included only the labels.
