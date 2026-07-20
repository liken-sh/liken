# Adopting an existing cluster

Milestone 16 — Done

liken can found a cluster. Adoption means joining a cluster that liken
did not found, such as a running k3s cluster on some other OS.
Adoption lets the cluster's machines be replaced with liken machines
one at a time, while the cluster keeps serving. At no point does the
process export or restore anything in etcd, such as Secrets, PVs, or
workloads.

Most of the mechanism follows from the identity design. The image
already carries the cluster's CAs and join token. So adopting a
cluster means importing its identity instead of minting a new one.
`liken adopt` takes the token and CA tree harvested from any of the
existing cluster's servers, and lays them into the same `dist/`
layout that minting produces. After that step, the build does not
care where the identity came from. An image built this way joins the
existing cluster directly. Follower machines need no code changes at
all.

The one real change is the founder's datastore decision. A new
Cluster field, `spec.origin` (`founded` | `adopted`), gates this
decision. An adopted cluster's datastore already exists, so every
leader joins that existing datastore. The founder joins through the
endpoint, never through `cluster-init`, and never through sqlite.
Initializing a second datastore beside a live one would split the
cluster into two.

The process never copies state. Each liken leader that joins becomes
an etcd member, and raft replicates the keyspace to it. The foreign
servers rotate out through ordinary member removal (`kubectl delete
node`, the same mechanism that demotion already uses). They rotate
out one at a time, so that quorum holds.

The origin field permits exactly one edit: `adopted` → `founded`. A
CEL transition rule enforces this, and a human makes the edit after
the last foreign member is gone. Promotion changes nothing on a
running fleet, because k3s ignores `cluster-init` when the datastore
already exists. Promotion matters only if someone rebuilds the
cluster from scratch later: a founded cluster's founder may create
the datastore again.

Mixed-cluster hygiene came out of this work and stands on its own.
Every liken node registers with a `liken.sh/machine=true` label. The
OS DaemonSets (the operator and the log relays) select on that label,
so Kubernetes never schedules them onto nodes that cannot run their
images. Otherwise, foreign nodes stay deliberately invisible to the
fleet controllers. Foreign nodes have no Machine resources. liken
manages Machines, while Kubernetes manages Nodes.

A real migration needs other things too: the CSI plugin's host
dependencies, workloads that rely on bundled components that liken
disables, and version skew between the two k3s builds. These are
deployment work, not OS work, and belong in the deployment's own
runbook.

1. [x] Prove it in the lab. A stock multi-server k3s cluster in QEMU
   guests plays the role of the foreign cluster. The plan harvests
   and adopts its identity, then runs the full rotation end to end:
   leader-first join, members rotated out one at a time, the endpoint
   re-pointed, and the cluster promoted. A marker Secret created
   before adoption must survive to the end. No OS DaemonSet pod may
   ever land on a foreign node.

   This was proven in the lab. Two Ubuntu 24.04 guests ran stock k3s
   with embedded etcd, at the same version liken pins. A marker
   Secret planted before adoption was still there after promotion,
   with its uid unchanged: the same etcd object, never recreated. The
   first liken machine joined through the endpoint wearing its node
   label. The OS DaemonSets scheduled no pods on the foreign nodes at
   any point. The endpoint re-point and the promotion each rolled
   through the fleet as ordinary staged cluster edits, one granted
   reboot at a time. The founder's post-promotion boot rendered
   `cluster-init: true` against the live datastore and rejoined as an
   ordinary member. This confirmed that k3s ignores `cluster-init`
   when a datastore already exists. The CEL rule refused the
   `founded → adopted` edit after the promotion.

   One finding is worth remembering: a server's disable list acts on
   the whole cluster. The moment the first liken server joined, it
   submitted the helm-delete job for the foreign cluster's packaged
   traefik, and removed traefik cluster-wide. A deployment that
   relies on the bundled components must have replacements running
   before the first liken server joins, not merely before the last
   foreign server leaves.
