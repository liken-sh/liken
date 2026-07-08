# Opt-in features: network storage clients and the bundled components

Milestone 17 — Designed, not started

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
CRD refuses null for a non-nullable object at admission, and the file
parser refuses it with a message that says to write {}. Unknown slugs
are refused the same two ways. One mechanical note for the reader who
looks for additionalProperties: false in the CRD and doesn't find it:
apiextensions forbids it beside named properties in a structural
schema, but the named-properties-only shape gets the same result,
because kubectl's server-side field validation refuses unknown keys at
admission and the strict file parse refuses them at boot.

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
A feature's payload ships only when the deployment's cluster document
declares the feature; an image built without it carries nothing, which
is the point. Each shipped feature also stages its kernel half at
/etc/liken/features/<name>/modules.conf, riding the same module
pipeline milestone 18 built, and init learns a feature's modules from
that file alone: file present means payload shipped, so there is no
feature-to-modules mapping hardcoded in init, and a missing file is
how init knows to report that the booted image was built without a
feature the cluster now declares.

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
image can ride the liken image as a hand-assembled OCI tarball through
the auto-manifests directory, the way the operator and log relays
already deploy, which also closes a deadlock: an image registry hosted
on iSCSI-backed storage could otherwise be needed to start the very
daemon that mounts it. The deployment this repo serves runs
synology-csi, which execs the host's iscsiadm and expects a running
iscsid, so the host binaries are load-bearing, not a convenience.

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
an image built without a feature booted against a cluster document
declaring it reports FeaturesReady: False with the rebuild message
while ClusterConverged stays True, and killing iscsid's pod proves the
workload plane owns recovery. The synology-csi proof against the real
filer belongs to the deployment that runs one; the lab proves the host
contract.

Still open, to be settled when this milestone runs: the exact
digest-pinned container recipe for the static builds; whether sd_mod
is modular or built-in in the vendored kernel config (a logged-in LUN
still needs the SCSI disk driver to become /dev/sdX); and the precise
shape of the iscsid DaemonSet, which the lab decides against the real
driver.
