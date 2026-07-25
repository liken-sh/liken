# Node labels on the Machine

Milestone 21 — Completed. A Machine declares its node labels, init
applies them when the node registers, and the operator reconciles them
live.

Workloads schedule on node labels: which machine has the GPU, which
one is on battery-backed power, which one may run the noisy batch
jobs. Before this milestone, the OS applied one label,
liken.sh/machine, from the static k3s configuration. Any further label
came through kubectl, outside the Machine document, so a reinstalled
machine came back without it. Labels are a machine's scheduling
identity, and identity belongs in the manifest.

The declaration is spec.nodeLabels on the Machine, a map of label key
to value. It reaches the Node through two mechanisms, with the same
pattern that sysctls already use: init applies it at boot, and the
operator reconciles it live afterward. At boot, init renders the map
into the k3s drop-in as node-label entries. The node then registers
with its labels already applied, and does not spend its first minutes
as a blank node that the scheduler misreads.

One mechanical detail matters: the + suffix (node-label+:), which is
k3s's append syntax for list values. A plain node-label key in a
drop-in would replace the static file's list and erase
liken.sh/machine=true. A drop-in must append to what a person wrote.

Registration only adds labels. The kubelet applies its --node-labels
when it registers, and never removes one. A label retracted from the
spec would stay on the Node, and a stale "has the GPU" label is the
scheduling error that this milestone prevents. Removal is the
operator's job, in the same reconcile pass that re-asserts sysctls.

Removal needs a record. Nothing about a label on a Node says who
applied it, and the operator must never strip a label that a person or
another controller applied. The record is an annotation on the Node
itself (liken.sh/node-labels), which names exactly the keys that the
operator manages. A key that is in the annotation but no longer in the
spec gives the operator permission to remove it. The drain uses the
same method to tell its own cordon from a human's. The record is on
the Node, not in Machine status, so it cannot drift from the labels
that it describes.

A NodeLabelsApplied condition reports the outcome. Labels never count
as spec drift: like sysctls, they reconcile live, so a reboot would
apply nothing that is not already applied.

Validation at admission keeps a label edit from taking a machine down.
Registration labels pass through the kubelet. Under the
NodeRestriction admission plugin, a kubelet that is given a
kubernetes.io or k8s.io label outside its own allowlist refuses to
start. A mistyped label that turns into a machine that will not boot is
the failure that the CRD's rules prevent. So admission permits exactly
what the kubelet permits itself: hostname, arch, os, the
topology.kubernetes.io pair, and the kubelet.kubernetes.io and
node.kubernetes.io namespaces.

Admission refuses the rest of Kubernetes' namespaces, including
node-role.kubernetes.io, which the demotion cleanup reads and which no
spec may imitate. Admission also refuses the liken.sh namespace,
because the OS owns it. The static configuration stamps
liken.sh/machine, and a spec that competes with the OS over its own
vocabulary has no good outcome.

The lab proved every path on node-4, which declares guid.foo/drill:
node-labels as its standing example, beside the dummy module. The
admission drills all refused with messages that name the fix:
node-role.kubernetes.io/database, liken.sh/machine, and a malformed key
each bounced at the API server, and topology.kubernetes.io/zone passed,
as the kubelet's own vocabulary permits. A declared label reached the
Node within one reconcile pass, with no reboot, and the ownership
annotation recorded the key. A value rewritten by hand (kubectl label
--overwrite) was re-asserted one pass later. A hand-applied label that
the spec never named stayed untouched. A retraction of a declared label
removed it from the Node and shrank the annotation with it.

The registration path proved out on a staged reboot. The serial console
showed init render "node-label+: - guid.foo/drill=node-labels" into the
drop-in, and the kubelet's own command line carried
--node-labels=liken.sh/machine=true,guid.foo/drill=node-labels: the
static file's label and the spec's label, appended as designed.
