# Node labels on the Machine

Milestone 21 — Done

Workloads schedule on node labels: which machine has the GPU, which
one is on battery-backed power, which one may run the noisy batch
jobs. Before this milestone, the OS applied exactly one label
(liken.sh/machine, from the static k3s configuration). Any further
labeling happened through kubectl, outside the Machine document, so a
reinstalled machine came back without it. Labels are a machine's
scheduling identity, and identity belongs in the manifest.

The declaration is spec.nodeLabels on the Machine, a map of label key
to value. It reaches the Node through two mechanisms, following the
same pattern that sysctls already use: init applies it at boot, and
the operator reconciles it live afterward. At boot, init renders the
map into the k3s drop-in as node-label entries, so the node registers
with its labels already applied, and never spends its first minutes as
a blank node that the scheduler misreads. One mechanical detail
matters: the +
suffix (node-label+:), which is k3s's append syntax for list values. A
plain node-label key in a drop-in would replace the static file's list
and erase liken.sh/machine=true. Appending to what a person wrote is
the whole purpose of a drop-in.

Registration only adds labels. The kubelet applies its --node-labels
when it registers and never removes one. So a label retracted from
the spec would linger on the Node forever, and a stale "has the GPU"
label is exactly the scheduling error that this milestone exists to
prevent. Removal is the operator's job, in the same reconcile pass
that re-asserts sysctls, and it needs a record: nothing about a label
on a Node says who put it there, and the operator must never strip a
label that a person or another controller applied. The record is an
annotation on the Node itself (liken.sh/node-labels), naming exactly
the keys that the operator manages. A key present in the annotation
but no longer in the spec gives the operator permission to remove it.
This is the same method that the drain uses to tell its own cordon
apart from a human's. The record lives on the Node, rather than in
Machine status, so it can never drift apart from the labels it
describes. A NodeLabelsApplied condition reports the outcome. Labels
never count as spec drift: like sysctls, they reconcile live, so a
reboot would apply nothing that is not already applied.

Validation at admission is what keeps a label edit from taking a
machine down. Registration labels pass through the kubelet, and under
the NodeRestriction admission plugin, a kubelet handed a kubernetes.io
or k8s.io label outside its own allowlist refuses to start at all. A
mistyped label turning into a machine that will not boot is exactly
the failure that the CRD's rules exist to prevent. So admission
permits exactly what the kubelet permits itself: hostname, arch, os,
the topology.kubernetes.io pair, and the kubelet.kubernetes.io and
node.kubernetes.io namespaces. It refuses the rest of Kubernetes'
namespaces, including node-role.kubernetes.io, which the demotion
cleanup reads and which no spec should be able to imitate. Admission
also refuses the liken.sh namespace, because the OS owns it: the
static configuration stamps liken.sh/machine, and a spec that fights
the OS over its own vocabulary has no good outcome.

The lab proved every path on node-4, which now declares
guid.foo/drill: node-labels as its standing example, beside the dummy
module. The admission drills all refused with messages that name the
fix: node-role.kubernetes.io/database, liken.sh/machine, and a
malformed key each bounced at the API server, while
topology.kubernetes.io/zone passed, as the kubelet's own vocabulary
should. Declaring the label applied it to the Node within a reconcile
pass, with no reboot, and the ownership annotation recorded the key.
Rewriting the value by hand (kubectl label --overwrite) was
re-asserted a pass later. A hand-applied label that the spec never
named survived untouched. Retracting a declared label removed it from
the Node and shrank the annotation with it. The registration path
proved out on a staged reboot: the serial console showed init render
"node-label+: - guid.foo/drill=node-labels" into the drop-in, and the
kubelet's own command line carried
--node-labels=liken.sh/machine=true,guid.foo/drill=node-labels, the
static file's label and the spec's label, appended exactly as
designed.
