# Optional features: network storage clients and bundled components

Milestone 17 — Completed. A Cluster declares optional features, and
liken ships the binaries, kernel modules, and k3s configuration that
each feature needs.

liken is a minimum viable, highly available cluster, and the aim is to
keep it that way. Capabilities that people do not need must not build
up in every image. But the survey of a real deployment's workloads
(milestone 16) showed that the OS must offer more than its minimum.
Network storage is the first case. Real workloads mount volumes
through CSI drivers, and a CSI node plugin depends on the host for the
clients. Without those clients, every network-backed PV on a liken node
stays in the Pending state.

This milestone builds the general mechanism for this. It also absorbs
milestone 19. The mechanism is a single vocabulary of optional features
that a cluster can opt into. The mechanism behind each feature is
liken's concern, not the user's.

The declaration lives at spec.features on the Cluster. A feature is a
fact that every node must agree on. A PersistentVolume can attach to
any node that the scheduler picks. Milestone 16 showed that even the
k3s disable list has an effect across the whole cluster: one joining
server's disable list removes the component from the whole fleet.
spec.modules (milestone 18) already covers machine-specific hardware
needs, one layer down. Features are what the fleet as a whole offers.

The field is an object keyed by feature slug, not a list of names. A
feature can then get parameters later with no break in the schema.

    spec:
      features:
        metrics-server: {}
        iscsi: {}

The presence of the key is the opt-in. A feature with no configuration
uses {}.

An explicit null value is an error. It is never a quiet way to say no.
Everywhere in Kubernetes, null means unset: server-side apply deletes a
field when it is set to null. So a vocabulary where a bare `iscsi:` in
hand-written YAML meant enabled would work against every tool in the
Kubernetes ecosystem. A vocabulary where it meant disabled would hide
the misspelled-manifest error that the strict parser exists to catch.

Both entry points refuse a null feature. The CRD carries a validation
rule that refuses a null feature and tells the user to write {}
instead. The file parser refuses it the same way.

The system refuses an unknown slug only at admission, and this is
deliberate. The fleet has exactly one vocabulary at its API: the newest
image's CRD. But each machine's parser recognizes only the vocabulary
that its own binary was built with. A fleet mid-upgrade holds several
of these vocabularies at the same time. So a document that declares a
feature older than a machine's binary must still parse on that machine.

A drill showed that the alternative fails. A machine downgraded below
its cluster document's vocabulary refused the staged document. It could
not read the document that it had already run. It stayed in the Blocked
state on a document that the rest of the fleet ran without problems.

So the file parser lets an unknown slug through. The feature pass
reports the problem instead: FeaturesReady goes False and names the
slug and the image's own vocabulary. This single message covers both
real causes: an image older than the feature, and a misspelling in a
hand-written seed. In both cases the machine is degraded, not down.

The shape of the CRD's schema matters here, and drills against the live
API server settled the design. The alternative, one named property for
each feature, cannot enforce either rule. apiextensions prunes unknown
fields and drops null values for non-nullable fields before validation
runs. A misspelled slug in a patch would vanish, at most with a
client-side warning, because only kubectl apply and kubectl create
refuse it, through strict decoding. Pruning a mistyped parameter can
even turn a feature on by accident: {replicas: 2} pruned down to {} is
an opt-in.

So spec.features is a map, with additionalProperties and nullable
values, plus two CEL rules. These CEL rules run against exactly what
the user sent, because map keys are never pruned. They refuse both
mistakes and name the fix in their message. The parameter case needs
one more pair of rules: a rule that preserves unknown fields inside
each value stops the pruning, and a maximum of zero properties then
refuses a guessed parameter and names the exact field, until the day a
feature gets one.

This repo curates the vocabulary. A deployment names a feature; it does
not compose one. There are two kinds of feature internally, and the
user never needs to know which kind a feature is.

The first kind is a k3s bundled component: traefik, servicelb, and
metrics-server. The static configuration disables all three today, as a
rule. An opt-in to one of these removes it from the disable list. The
disable list changes from a fixed value in the image into a value
computed at each boot. init computes the full disable list, the bundled
set minus the cluster's opt-ins, into the k3s boot drop-in. init does
this only on leaders, because disable is a server-side key.

A machine that boots with no cluster document disables all three
components, so today's behavior stays the default. init always renders
the complete list, and does not merge fragments from different sources,
because a computed value must have exactly one author.

The second kind is a payload that liken vendors: iscsi and nfs. Each is
a top-level domain in this repo (open-iscsi, nfs-utils), and each
follows the same pattern as e2fsprogs and xtables: a pinned VERSION and
a fetch.sh script that produces sha256-verified static binaries. These
two domains differ from the existing vendored domains in one way. No
one publishes trustworthy prebuilt static builds of open-iscsi or
nfs-utils. So fetch.sh builds each one from a pinned source tarball,
inside a digest-pinned container, and records the output digests the
same way as every other vendored artifact.

Talos ships the same binaries through its iscsi-tools extension. This
gives an independent comparison when a build is audited.

Every image ships these payloads. They are small: a static open-iscsi
build is a few megabytes, next to a seventy-megabyte k3s binary.
Because every image ships them, the opt-in stays a runtime act: one
edit to the Cluster document, with no rebuild, no release to publish,
and no version to retarget.

init is the gate for these payloads. A payload is inert bytes until the
cluster document declares its feature. This is the same approach that
the disable list already takes to the components inside the k3s binary.
Each vendored feature stages its kernel half at
/etc/liken/features/<name>/modules.conf, with the same module pipeline
that milestone 18 built. init loads those modules only when the cluster
document declares the feature.

The presence of that file also tells init whether the booted image
carries the payload at all. No feature-to-modules mapping is hardcoded
anywhere. A missing file means the image predates the feature. This
happens when a cluster document declares a feature newer than the
release that a machine runs. The machine then reports the gap, and does
not quietly lack the capability.

A feature too large to ship in every image, a GPU toolkit for example,
is the point to introduce build-time conditioning. Until then, the
design does not need it.

On the liken side, the vocabulary lives in one table, in the cluster
package at cluster/features.go. The table lists a slug and a kind for
each feature. Everything that must agree on the vocabulary reads this
table. init validates the cluster document against the table and
renders the disable list from it. The operator judges each machine's
standing against the same table.

The CRD stays hand-written, so a reader can learn the API from its
schema. A parity test checks that the CRD's feature properties match
the table's slugs exactly, in both directions.

The table deliberately carries nothing else. Module lists live in the
feature files described above. Each domain's shipping steps are in
image/build.sh, because the build recipes differ from feature to
feature, and they read best in the open, not behind a table.

A feature may contribute any subset of six things: k3s configuration
rendered at boot, vendored binaries, kernel modules, workload manifests
seeded when the feature is declared, an init boot hook, and, in the
future, parameters. To add the next feature, add a table row and its
pieces. It never requires a redesign.

A slug enters the vocabulary in the same change that delivers its
payload. A feature that no image can honor would turn the report
described above into a permanent alarm.

A change to a feature converges the same way as any other cluster
change. Features stay inside the canonical rendered document.
spec.version and spec.releases are excluded from that document, because
to act on them means a download, not a boot. An edit to a feature
changes the document's hash and rolls through the fleet as staged
changes and conductor-granted reboots.

The whole-document hash proves that a boot ran the document. It does
not prove that the image could honor every part of the document. A
per-machine FeaturesReady condition carries this second claim, with the
same message discipline as ModulesLoaded: it names the problem and the
fix.

For iscsi, the host contributes binaries, modules, and identity. init
writes /etc/iscsi/initiatorname.iscsi with an IQN derived from the
machine name (iqn.2026-07.sh.liken:<name>). This value is the same on
every boot, so nothing must persist.

The daemon is deliberately not init's concern. The two-planes rule
admits a concern to the machine plane only when k3s depends on it to
exist. k3s does not depend on iscsid. Network-backed PVs depend on it,
and PVs are workloads. So iscsid runs in the workload plane, almost
certainly as a privileged hostNetwork DaemonSet.

hostNetwork is a requirement here, not a preference. With the in-kernel
initiator, iscsid opens the TCP connection to the target in userspace
and hands the socket to the kernel. The session then lives in the
network namespace that iscsid was in. iscsiadm reaches iscsid over an
abstract unix socket, which is also namespace-scoped. Sessions
themselves are kernel state. A restarted iscsid re-adopts its sessions
from sysfs, so a pod restart costs only a window without reconnect
handling, and it does not cost the attached disks.

The DaemonSet's image travels inside the liken image as a
hand-assembled OCI tarball, the same way as the operator and the log
relays. init seeds the DaemonSet's manifest into the auto-manifests
directory only when the cluster document declares the feature. A
retraction of the feature then removes the workload on the next roll.
Carrying the image this way, rather than a pull from a registry, also
closes a possible deadlock: an image registry hosted on iSCSI-backed
storage could otherwise need the daemon that mounts it, before that
daemon can start.

The DaemonSet image is built from the same vendored binaries as the
host's, so iscsid and the iscsiadm that talks to it over its socket are
always the same build. There is no version skew to manage. The
author's homelab runs synology-csi, which execs the host's iscsiadm
and expects a running iscsid. That deployment is why the host binaries
are necessary, and not a convenience.

A pure-Go initiator login exists: u-root's iscsinl, which speaks the
kernel's netlink interface directly. It could matter one day for
iSCSI-backed system storage at boot. It cannot satisfy this feature's
contract today, because the CSI drivers exec iscsiadm.

The nfs feature has the same shape and is smaller. It ships a static
mount.nfs (with libtirpc built in) and the nfsv4 kernel module, and no
daemon at all, because the feature covers NFSv4 only. NFSv3 would need
rpcbind and rpc.statd on the host, two daemons that k3s does not depend
on, and the two-planes rule refuses those. NFSv4 needs only one TCP
connection to port 2049, and the protocol's own leases carry the
locking. A deployment with a v3-only filer needs a future feature
discussion. It is not a silent gap in this design.

The lab proof ran as follows. A generic iSCSI target and an NFSv4
export were reachable from the guests. The cluster document declared
both features. The test exercised the host contract the same way that a
CSI node plugin does: the host's iscsiadm ran against its running
iscsid for discovery, login, and logout; raw bytes went to the LUN over
the wire and came back; and the host's mount.nfs4 mounted the export,
wrote to it, and survived unmount and remount cycles.

The milestone deliberately stops at that contract. It does not stand up
a CSI driver in the lab, because a driver exercises its own code on top
of the same calls. The proof against a real filer with synology-csi
belongs to a deployment that runs one.

The retraction drill ran the full round trip through the janitor,
described below. The vocabulary-skew failure mode was drilled directly:
a downgraded machine met a document that declared features that its
image predated. The leniency design described above is a result of that
drill.

The server side of that proof is dev-cluster/storage: a stock Debian
guest on the fleet's cluster segment, which serves one iSCSI LUN and
one NFSv4 export. It is deliberately not a liken machine, because the
drill tests liken's side of the wire. Its Makefile explains the whole
fixture.

In its first drill, the iSCSI half worked end to end on the first try:
discovery, login through the host's iscsid, and raw bytes written to
the LUN and read back.

The NFS half found a defect on the client side. mount(2) returned
success in milliseconds, but liken shipped no /etc/mtab. Without one,
mount.nfs's post-mount bookkeeping spun in userspace, and this blocked
every mount behind a helper process that never exited. The diagnosis
went down through the whole stack: wire captures showed only successful
compounds, nfs4 tracepoints showed the client finish its conversation,
and a syscall trace showed mount(2) succeed while the process burned
pure user time. The fix is one line in build.sh: make /etc/mtab a
symlink to /proc/self/mounts. This is the same compatibility link that
every mainstream distribution has shipped since about 2011, and it
tells mount.nfs that the kernel already keeps the mount table.

The drills exposed one gap that needed its own fix. k3s deletes an
auto-deploy addon's resources when its manifest file is removed while
k3s is running. But a retraction removes the file at boot, before k3s
starts, so k3s never sees the deletion. Without a fix, the retracted
feature's workload object would survive, with its pods in failure.

The retraction still disarms the feature, because init stops writing
its boot files, so the workload cannot function. But the clean removal
of the workload object belongs to the cluster operator's feature
janitor (cluster-operator/janitor.go). Each feature-seeded manifest
carries a liken.sh/feature annotation that names its owning feature.
Every sweep deletes any liken-system workload whose annotation names a
feature that the document no longer declares. The janitor tests only
one condition: whether the document still declares the feature. So it
acts as soon as the document is edited, and does not wait for the fleet
to roll. This is the timing that k3s itself would show, if k3s saw the
file removed.

Running the milestone settled three questions that the design had left
open.

The static build recipe lives in open-iscsi/fetch.sh. It pins alpine by
digest, pins three sources by sha256 (open-iscsi, plus kmod and
libeconf, whose static libraries alpine does not package), and applies
two one-line patches to open-iscsi's build definition, which never
aimed for a fully static link.

sd_mod is built into the vendored kernel (CONFIG_BLK_DEV_SD=y), so the
feature's module list needs only iscsi_tcp.

The iscsid DaemonSet's shape is defined in
open-iscsi/manifests/iscsid.yaml. It runs privileged and with
hostNetwork, with no API identity at all, and it shares /etc/iscsi,
/var/lib/iscsi, and the lock directory with the host's iscsiadm.
