# Adopting an existing cluster

Milestone 16 — Done

liken can found a cluster; adoption is joining one it didn't found (a
running k3s cluster on some other OS) so that its machines can be
replaced with liken machines one at a time while the cluster keeps
serving. Nothing in etcd — Secrets, PVs, workloads — is exported or
restored at any point.

Most of the mechanism falls out of the identity design. The image
already carries the cluster's CAs and join token, so adopting a
cluster means importing its identity instead of minting one:
`liken adopt` takes the token and CA tree harvested from any of
the existing cluster's servers and lays them into the same dist/
layout minting produces. From there the build doesn't care where the
identity came from, and an image built this way joins the existing
cluster directly; followers need no code changes at all.

The one genuine change is the founder's datastore decision, gated by
a new Cluster field, spec.origin (founded | adopted). An adopted
cluster's datastore already exists, so every leader joins it: the
founder through the endpoint, never cluster-init, never sqlite,
because initializing a second datastore beside a live one would split
the cluster in two. The state is never copied: each liken leader that
joins becomes an etcd member and raft replicates the keyspace to it.
The foreign servers rotate out by ordinary member removal (kubectl
delete node, the same mechanism demotion already uses), one at a time
so quorum holds.

The origin field permits exactly one edit, adopted → founded,
enforced by a CEL transition rule and made by a human after the last
foreign member is gone. Promotion changes nothing on a running fleet,
since k3s ignores cluster-init when the datastore already exists. It
matters if the cluster is ever rebuilt from scratch: a founded
cluster's founder may create the datastore again.

Mixed-cluster hygiene came out of this work and stands on its own:
every liken node registers with a liken.sh/machine=true label, and
the OS DaemonSets (operator, log relays) select on it, so they are
never scheduled onto nodes that can't run their images. Foreign nodes
are otherwise deliberately invisible to the fleet controllers: they
have no Machine resources, and liken manages Machines while
Kubernetes manages Nodes.

Everything else a real migration needs (the CSI plugin's host
dependencies, workloads that rely on the bundled components liken
disables, version skew between the two k3s builds) is deployment
work, not OS work, and belongs in the deployment's own runbook.

1. [x] Prove it in the lab: a stock multi-server k3s cluster in QEMU
   guests plays the foreign cluster, its identity is harvested and
   adopted, and the full rotation runs end to end: leader-first join,
   members rotated out one at a time, the endpoint re-pointed, the
   cluster promoted. A marker Secret created before adoption must
   survive to the end, and no OS DaemonSet pod may ever land on a
   foreign node. Proven: two Ubuntu 24.04 guests ran stock k3s
   (embedded etcd, the same version liken pins), and a marker Secret
   planted before adoption was still there after promotion with its
   uid unchanged: the same etcd object, never recreated. The first
   liken machine joined through the endpoint wearing its node label,
   and the OS DaemonSets scheduled no pods on the foreign nodes at
   any point. The endpoint re-point and the promotion each rolled
   through the fleet as ordinary staged cluster edits, one granted
   reboot at a time. The founder's post-promotion boot rendered
   cluster-init: true against the live datastore and rejoined as an
   ordinary member, confirming that k3s ignores cluster-init when a
   datastore exists. The CEL rule refused founded → adopted after the
   promotion. One finding to remember: a server's disable list acts
   on the whole cluster. The moment the first liken server joined, it
   submitted the helm-delete job for the foreign cluster's packaged
   traefik and removed it cluster-wide. A deployment that relies on
   the bundled components must have replacements running before the
   first liken server joins, not merely before the last foreign one
   leaves.
