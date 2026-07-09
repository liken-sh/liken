# Node labels on the Machine

Milestone 21 — Done

Workloads schedule on node labels: which machine has the GPU, which
one is on battery-backed power, which may run the noisy batch jobs.
Before this milestone the OS applied exactly one label
(liken.sh/machine, from the static k3s config), and any further
labeling happened through kubectl, outside the Machine document, so a
reinstalled machine came back without it. Labels are a machine's
scheduling identity, and identity belongs in the manifest.

The declaration is spec.nodeLabels on the Machine, a map of label key
to value, and it reaches the Node through two doors on the same
pattern sysctls set: init applies it at boot, the operator reconciles
it live afterward. At boot, init renders the map into the k3s drop-in
as node-label entries, so the node registers already wearing them and
never spends its first minutes as a blank node the scheduler
misreads. The one mechanical subtlety is the + suffix (node-label+:),
k3s's append syntax for list values: a plain node-label key in a
drop-in would replace the static file's list and erase
liken.sh/machine=true, and appending to what a person wrote is the
whole point of a drop-in.

Registration only adds. The kubelet applies its --node-labels when it
registers and never removes one, so a label retracted from the spec
would linger on the Node forever, and a stale "has the GPU" label is
exactly the scheduling lie this milestone exists to prevent. Removal
is the operator's job, in the same reconcile pass that re-asserts
sysctls, and it needs memory: nothing about a label on a Node says
who put it there, and the operator must never strip one a person or
another controller applied. The memory is an annotation on the Node
itself (liken.sh/node-labels) recording exactly the keys the operator
manages; a key in the annotation but no longer in the spec is the
license to remove it. That is the same trick the drain uses to tell
its own cordon from a human's, and the record lives on the Node
rather than in Machine status so it can never drift apart from the
labels it describes. A NodeLabelsApplied condition reports the
outcome, and labels never count as spec drift: like sysctls, they
reconcile live, so a reboot would apply nothing that isn't already
applied.

Validation at admission is what keeps a label edit from taking a
machine down. Registration labels pass through the kubelet, and under
the NodeRestriction admission plugin a kubelet handed a kubernetes.io
or k8s.io label outside its own allowlist refuses to start at all —
a mistyped label becoming a machine that won't boot is the failure
the CRD's rules exist to prevent. So admission permits exactly what
the kubelet permits itself (hostname, arch, os, the
topology.kubernetes.io pair, and the kubelet.kubernetes.io and
node.kubernetes.io namespaces) and refuses the rest of Kubernetes'
namespaces, including node-role.kubernetes.io, which demotion cleanup
reads and no spec should be able to counterfeit. The liken.sh
namespace is refused as OS-owned: the static config stamps
liken.sh/machine, and a spec fighting the OS over its own vocabulary
has no good outcome.

The lab proved every path on node-4, which now declares
guid.foo/drill: node-labels as its standing example beside the dummy
module. The admission drills all refused with messages that name the
fix: node-role.kubernetes.io/database, liken.sh/machine, and a
malformed key each bounced at the API server, while
topology.kubernetes.io/zone passed as the kubelet's own vocabulary
should. Declaring the label applied it to the Node within a reconcile
pass, no reboot, with the ownership annotation recording the key.
Rewriting the value by hand (kubectl label --overwrite) was
re-asserted a pass later; a hand-applied label the spec never named
survived untouched; and retracting a declared label removed it from
the Node and shrank the annotation with it. The registration path
proved out on a staged reboot: the serial console showed init render
"node-label+: - guid.foo/drill=node-labels" into the drop-in, and the
kubelet's own command line carried
--node-labels=liken.sh/machine=true,guid.foo/drill=node-labels — the
static file's label and the spec's, appended exactly as designed.
