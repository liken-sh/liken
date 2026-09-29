# The Cluster converges

Milestone 08. Completed. An edit to the in-cluster Cluster resource
reaches every machine and takes effect on the next boot.

Before this milestone, the in-cluster Cluster resource was seed-only.
Init read the image's cluster.yaml on every boot. The operator seeded
the API copy once. Nothing compared the two, so `kubectl edit cluster`
changed a document that no machine read.

The Machine resource already has the full lifecycle that this needs:
drift detection, durable staging on machineState, proven fallback, and
SpecConverged. The Cluster document now uses the same machinery. Each
machine stages its own copy, and applies it on the next boot.

The convergence machinery works per machine, but the Cluster resource
is cluster-scoped. So every machine stages its own copy, machines can
disagree for a time about which Cluster spec they booted, and status
shows that disagreement for each machine.

The design considered a live fetch of the cluster config at boot, and
rejected it. That approach is circular, because the endpoint is inside
the document to fetch. Followers also hold no API credentials, and a
leader outage would block follower boots. The operator pod on every
node is already a live, credentialed reader of the API. Disk storage is
only the crash-safe handoff from that runtime read to the boot-time
consumer.

This milestone comes before HA leaders, because growth of spec.leaders
is a Cluster edit, and the HA milestone needs edits that converge. It
also comes before GitOps, because git will own the Cluster document,
and a document that git owns must take effect.

1. [x] The staging store becomes general-purpose: machine/staging.go
   now operates on any directory, instead of the hardcoded manifests/
   path. Machine manifests stay at machineState's manifests/ path, and
   cluster manifests go next to them at cluster/, with the same four
   files, the same hashing, and the same durable writes. A
   memory-backed machine stays seed-only for both kinds, as before.
2. [x] Init selects the cluster manifest in this order: staged, then
   proven, then seed. Init rejects a staged document at vetting if the
   document does not parse, or if it is not kind: Cluster. The boot
   record gains clusterManifestSource, clusterManifestHash, and
   clusterRejection, next to the machine fields, through facts and the
   CRD schema as usual. This process is simpler than the machine
   manifest's peek. The Cluster document does not drive storage, so
   when init reads it, machineState is an ordinary mounted filesystem,
   and init needs no peek mount.
3. [x] Promotion is the new mechanism, and the join supplies the proof.
   Storage reconciliation proves a machine manifest within the boot,
   but a cluster manifest's failure modes appear later in the process.
   For example, a bad endpoint means the follower never joins. So init
   cannot prove the cluster manifest at settle time. Init boots a
   staged cluster document tentatively and writes an attempted marker,
   which holds the staged hash. The operator promotes the document on
   its first reconcile pass and clears the marker. The operator runs as
   a pod, and that fact alone proves that containerd, the kubelet, and
   the join all worked under this config. If a boot finds the marker
   still matching the staged hash, the last try never got promoted.
   Init then rejects the staged document and falls back to the proven
   one. One proving boot is enough. The design is crash-only, and it
   needs no boot counters.
4. [x] The operator gains a second part of its job: it reads the
   Cluster resource on every pass. RBAC already permits this. Seeding
   stays create-only, and the operator still never writes spec. The
   operator renders canonical bytes, compares them against the boot
   record, and runs the same decision table as the Machine. It
   withdraws stale staged specs and clears rejections when the spec is
   current. It holds when the last boot rejected the spec. It stages
   drift and requests a reboot, under the Machine's spec.rebootPolicy,
   and one setting governs both kinds of staging. A new
   ClusterConverged condition uses the same reason vocabulary as the
   Machine, and Ready rolls it up. This design has no fleet
   orchestration. A Cluster edit is drift on every machine at once, and
   if every machine used Auto policy, that would cause a simultaneous
   fleet reboot. Manual stays the default policy. Pending reboots stay
   visible for each machine, and rolling coordination is milestone 13's
   job.
5. [x] Immutability rules: the five network-plan fields (nodeCIDR,
   clusterCIDR, serviceCIDR, clusterDNS, clusterDomain) become
   immutable once set, through CEL oldSelf rules. k3s cannot change any
   of these values on a running cluster, so an edit to them could never
   take effect, and the mismatch would appear only at the next reboot.
   The oldSelf rule is correct here, unlike in the storage rules of
   milestone 5.7. Nobody needs to edit these facts back to match
   reality, because their reality never changes. leaders, endpoint, and
   time stay freely editable.
6. [x] Drill it on the two-node lab. Add a second NTP upstream through
   kubectl edit cluster. Both machines stage the change, report
   RebootPending, apply it on reboot, and show the new source in
   status.time with ClusterConverged True. Point the endpoint at a dead
   address and reboot the follower: it does not join, so it runs no
   operator and gets no promotion. The next boot rejects the staged
   document on the attempted marker, falls back to the proven one, and
   rejoins the real cluster, with the rejection visible in status. Then
   edit the endpoint back to a good address: the staged document
   withdraws and the rejection clears, with no reboot necessary. All
   three drills ran as designed. A power cut recovered the
   dead-endpoint boot. This is the only available recovery, because a
   machine that never joined has no operator to request a reboot
   through.
