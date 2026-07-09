# Opt-in features: network storage clients and the bundled components

Milestone 17 — In progress

liken today is a minimum viable highly-available cluster, and the aim
is to keep it one: capabilities people may not need should not
accumulate in every image. But the survey of a real deployment's
workloads (milestone 16) showed the OS must be able to offer more than
its minimum, starting with network storage: real workloads mount
volumes through CSI drivers, and a CSI node plugin depends on the host
for the actual clients. Without them, every network-backed PV on a
liken node stays Pending. This milestone is the general mechanism, one
that also absorbs milestone 19: a single vocabulary of optional
features a cluster opts into, where the mechanism behind each feature
is liken's business and not the user's.

The declaration is spec.features on the Cluster, because a feature is
a fact every node must agree on: a PersistentVolume can attach to any
node the scheduler picks, and milestone 16 showed that even the k3s
disable list is cluster-wide in effect (one joining server's disable
list removes the component from the whole fleet). Machine-specific
hardware needs are already covered one layer down by spec.modules
(milestone 18); features are what the fleet as a whole offers.

The field is an object keyed by feature slug, not a list of names,
so a feature can grow parameters without a schema break:

    spec:
      features:
        metrics-server: {}
        iscsi: {}

Presence of the key is the opt-in, and a feature's zero configuration
is {}. Explicit null is a loud error, never a quiet no: everywhere in
Kubernetes, null means unset (server-side apply deletes a field set to
null), so a vocabulary where a bare `iscsi:` in hand-written YAML
meant *enabled* would fight every tool in the ecosystem, and one where
it silently meant *disabled* is exactly the misspelled-manifest
failure the strict parser exists to prevent. Both doors reject it: the
CRD carries a validation rule that refuses a null feature with a
message saying to write {}, and the file parser refuses it the same
way. Unknown slugs are refused at admission only, deliberately. The
fleet has exactly one vocabulary at its API (the newest image's CRD),
but each machine's parser knows only the vocabulary its binary was
built with, and a fleet mid-upgrade holds several of those at once,
so a document declaring a feature a binary predates must still parse.
The lab proved the alternative the hard way: a machine downgraded
below its cluster document's vocabulary rejected the staged document,
could not read even its proven one, and sat Blocked on a document the
rest of the fleet was happily running. So the file parser lets an
unknown slug through, and the feature pass reports it instead:
FeaturesReady goes False naming the slug and the image's own
vocabulary, which covers both real causes (an image that predates the
feature, and a misspelling in a hand-written seed) with the machine
degraded rather than down. The CRD's schema
shape is load-bearing here, and drilling against the live API server
is what settled it: the natural-looking alternative, one named
property per feature, cannot enforce either rule, because
apiextensions prunes unknown fields and drops nulls for non-nullable
values before validation ever runs. A misspelled slug in a patch
would vanish with at most a client-side warning (only kubectl apply
and create refuse it, via strict decoding), and pruning a mistyped
parameter can even flip a feature on: {replicas: 2} pruned to {} is
an opt-in. So spec.features is a map (additionalProperties, with
nullable values) plus two CEL rules, which run against exactly what
was sent, because map keys are never pruned, and refuse both mistakes
with messages that name the fix. The parameter case takes one more
pair: preserving unknown fields inside each value stops the pruning,
and a maximum of zero properties then refuses a guessed parameter
with the exact field named, until the day a feature actually grows
one.

The vocabulary is curated here, in this repo, and deployments name
features rather than composing them. Behind the curtain there are two
kinds, and the user never needs to know which is which. The first kind
is k3s's bundled components: traefik, servicelb, and metrics-server,
which the static config disables on principle today. Opting in removes
one from the disable list, which stops being an image hardcode and
becomes a per-boot rendering: init computes the full disable list
(the bundled set minus the cluster's opt-ins) into the k3s boot
drop-in, on leaders only since disable is a server-side key, and a
machine booting with no cluster document disables all three, so
today's behavior remains the default. Init always renders the complete
list rather than merging fragments, because a computed value should
have exactly one author.

The second kind is payloads liken vendors: iscsi and nfs, each a
top-level domain in this repo (open-iscsi, nfs-utils) following the
same pattern as e2fsprogs and xtables, a pinned VERSION and a fetch.sh
that produces sha256-verified static binaries. These differ from the
existing vendored domains in one way: nobody publishes trustworthy
prebuilt static builds of open-iscsi or nfs-utils, so fetch.sh builds
from a pinned source tarball inside a digest-pinned container, and
records the output digests the way every other vendored artifact does.
(Talos ships the same binaries through its iscsi-tools extension,
which makes a useful independent comparison when auditing a build.)
Payloads ship in every image, because they are small (static
open-iscsi is a few megabytes beside a seventy-megabyte k3s binary)
and because shipping them unconditionally keeps opting in a purely
runtime act: one Cluster edit, no rebuild, no release to publish, no
version to retarget. Init is the gate. A payload is inert bytes until
the cluster document declares its feature, the same posture the
disable list already takes toward the components inside the k3s
binary. Each vendored feature stages its kernel half at
/etc/liken/features/<name>/modules.conf, riding the same module
pipeline milestone 18 built, and init loads those modules only when
the feature is declared. That file's presence is also how init knows
the booted image carries the payload at all, with no feature-to-
modules mapping hardcoded anywhere: a missing file means the image
predates the feature, which can happen when a cluster document
declares a feature newer than the release a machine is running, and
the machine reports the gap instead of silently lacking the
capability. A feature too large to ship in every image (a GPU
toolkit, say) would be the moment to introduce build-time
conditioning, and not before.

On the liken side, the vocabulary is one table in the machine package
(machine/features.go): a slug and a kind per feature, consulted by
everything that must agree on it. Init validates the cluster document
against the table and renders the disable list from it, and the
operator judges each machine's standing against it. The CRD stays
hand-written so its schema can teach the API, and a parity test holds
its feature properties to exactly the table's slugs, in both
directions. The table deliberately carries nothing else: module lists
live in the feature files above, and each domain's shipping steps are
spelled out in image/build.sh, where the recipes are genuinely
different from feature to feature and read best in the open. What a
feature may contribute is any subset of six things: k3s configuration
rendered at boot, vendored binaries, kernel modules, workload
manifests seeded when declared, an init boot hook, and one day
parameters. The next feature is a table row and its pieces, never a
redesign. A slug enters the vocabulary in the same change that
delivers its payload, because offering a feature no image can honor
would make the reporting below a permanent alarm.

Toggling a feature converges like any other cluster change: features
stay in the canonical rendered document (unlike spec.version and
spec.releases, which are excluded because their actuation is a
download, not a boot), so an edit changes the document's hash and
rolls through the fleet as staged changes and conductor-granted
reboots. The whole-document hash proves a boot ran the document, not
that the image could honor it, so a per-machine FeaturesReady
condition carries the second claim, with the same fix-naming message
discipline as ModulesLoaded.

For iscsi specifically, the host's whole contribution is binaries,
modules, and identity. Init writes /etc/iscsi/initiatorname.iscsi with
an IQN derived from the machine name (iqn.2026-07.sh.liken:<name>),
deterministic on every boot, nothing to persist. The daemon is
deliberately not init's problem: the two-planes rule admits a concern
to the machine plane only when k3s depends on it to exist, and k3s
does not depend on iscsid; network-backed PVs do, and PVs are
workloads. So iscsid runs in the workload plane, almost certainly as a
privileged hostNetwork DaemonSet, and hostNetwork is not a preference
but a requirement: with the in-kernel initiator, iscsid opens the TCP
connection to the target in userspace and hands the socket to the
kernel, so the session lives in whatever network namespace iscsid was
in, and iscsiadm reaches iscsid over an abstract unix socket, which is
namespace-scoped too. Sessions themselves are kernel state; a
restarting iscsid re-adopts them from sysfs, so a pod restart costs a
window without reconnect handling, not attached disks. The DaemonSet's
image rides the liken image as a hand-assembled OCI tarball, the way
the operator and log relays already deploy, and init seeds its
manifest into the auto-manifests directory only when the cluster
document declares the feature, so retracting the feature removes the
workload on the next roll. Riding the image rather than a registry
also closes a deadlock: an image registry hosted on iSCSI-backed
storage could otherwise be needed to start the very daemon that
mounts it. Building the DaemonSet image from the same vendored
binaries as the host's means iscsid and the iscsiadm that talks to it
over its socket are always the same build, with no version skew to
manage. The deployment this repo serves runs synology-csi, which
execs the host's iscsiadm and expects a running iscsid, so the host
binaries are load-bearing, not a convenience. (A pure-Go initiator
login exists, u-root's iscsinl, speaking the kernel's netlink
interface directly; it could one day matter for iSCSI-backed system
storage at boot, but it cannot satisfy this feature's contract,
because the CSI drivers exec iscsiadm.)

The nfs feature is the same shape and smaller: a static mount.nfs
(with libtirpc built into it), the nfsv4 module, and no daemon at all,
because the feature means NFSv4 only. NFSv3 would drag rpcbind and
rpc.statd onto the host, two daemons k3s does not depend on, which the
two-planes rule refuses; v4 is one TCP connection to port 2049 with
locking carried by the protocol's own leases. A deployment with a
v3-only filer is a future feature discussion, not a silent gap.

The lab proof: a generic iSCSI target and an NFSv4 export reachable
from the guests, the cluster document declaring both features, and a
real CSI node plugin mounting one block volume and one file volume
into pods, with a write surviving a node reboot. The failure drills:
an image predating a feature booted against a cluster document
declaring it reports FeaturesReady: False with the upgrade message
while ClusterConverged stays True, and killing iscsid's pod proves the
workload plane owns recovery. The synology-csi proof against the real
filer belongs to the deployment that runs one; the lab proves the host
contract.

Three questions this design left open were settled when the milestone
ran. The static build recipe lives in open-iscsi/fetch.sh: alpine
pinned by digest, three sources pinned by sha256 (open-iscsi, plus
kmod and libeconf, whose static libraries alpine doesn't package),
and two one-line patches to open-iscsi's build definition, which
never aimed for a fully static link. sd_mod is built into the
vendored kernel (CONFIG_BLK_DEV_SD=y), so the feature's module list
is iscsi_tcp alone. And the iscsid DaemonSet's shape is
open-iscsi/manifests/iscsid.yaml: privileged and hostNetwork, no API
identity at all, sharing /etc/iscsi, /var/lib/iscsi, and the lock
directory with the host's iscsiadm.
