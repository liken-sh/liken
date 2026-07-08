# The Cluster converges

Milestone 8 — Done

The Cluster converges: the in-cluster Cluster resource was
seed-only. Init read the image's cluster.yaml every boot, the
operator seeded the API copy once, and nothing ever compared
the two, so `kubectl edit cluster` changed a document no
machine consulted. The Machine already has the whole lifecycle
this needs (drift detection, durable staging on machineState,
proven fallback, SpecConverged), so the Cluster document uses
the same machinery, staged per machine and applied by the next
boot. The convergence machinery is per-machine but the Cluster
is cluster-scoped, so every machine stages its own copy,
machines can transiently disagree about which Cluster spec
they booted, and status makes that visible per machine.
Fetching cluster config live at boot was considered and
rejected: it's circular (the endpoint is inside the document
being fetched), followers hold no API credentials, and it
would make a leader outage block follower boots. Meanwhile the
operator pod on every node already is a live, credentialed
reader of the API; disk is just the crash-safe handoff from
the runtime read to the boot-time consumer. This deliberately
lands before HA leaders, because growing spec.leaders is
precisely a Cluster edit and the HA milestone needs edits that
converge. It also lands before GitOps, because git will own
the Cluster document, and a document git owns must actually
take effect.
1. [x] The staging store generalizes: machine/staging.go operates
   on a directory instead of the hardcoded manifests/ path, so
   machine manifests stay at machineState's manifests/ and
   cluster manifests land beside them at cluster/, with the
   same four files, the same hashing, and the same durable
   writes. A memory-backed machine stays seed-only for both
   kinds, exactly as today.
2. [x] Init selects the cluster manifest staged → proven → seed. A
   staged document that won't parse (or isn't kind: Cluster)
   is rejected at vetting. The boot record grows
   clusterManifestSource, clusterManifestHash, and
   clusterRejection next to the machine fields, through facts
   and the CRD schema as usual. This is deliberately simpler
   than the machine manifest's peek: the Cluster document
   doesn't drive storage, so by the time it's read,
   machineState is an ordinary mounted filesystem and no peek
   mount is needed.
3. [x] Promotion, the genuinely new mechanism: the join itself is
   the proof. A machine manifest is proven by storage
   reconciliation within the boot, but a cluster manifest's
   failure modes are downstream (a bad endpoint means the
   follower never joins), so init can't prove it at settle time.
   Init boots a staged cluster document tentatively and writes
   an attempted marker (the staged hash). The operator promotes
   the document on its first reconcile pass and clears the
   marker; its own existence as a running pod proves that
   containerd, the kubelet, and the join all worked under this
   config. A boot that finds the marker still matching the
   staged hash knows the last try never got promoted: reject,
   fall back to proven. One proving boot is enough, the design
   is crash-only, and no boot counters are needed.
4. [x] The operator's other half: read the Cluster resource every
   pass (RBAC already allows it; seeding stays create-only and
   the operator still never writes spec), render canonical
   bytes, compare against the boot record, and run the same
   decision table as the Machine: withdraw stale staged specs
   and clear rejections when current, hold on
   rejected-last-boot, stage drift and request a reboot per
   the Machine's spec.rebootPolicy (one knob governs both
   kinds of staging). A new ClusterConverged condition uses
   the same reason vocabulary; Ready rolls it up. There is
   deliberately NO fleet orchestration: a Cluster edit is
   drift on every machine at once, and with Auto everywhere
   that would be a simultaneous fleet reboot. Manual stays the
   default, pending reboots are visible per machine, and
   rolling coordination is milestone 13's job.
5. [x] Guardrails: the five network-plan fields (nodeCIDR,
   clusterCIDR, serviceCIDR, clusterDNS, clusterDomain) become
   immutable-once-set via CEL oldSelf rules. k3s can't re-plumb
   any of them in place, so an edit there could never take
   effect, and the mismatch would only surface at a reboot.
   (oldSelf is correct here,
   unlike the storage rules of milestone 5.7: these facts can
   never be edited "back to reality," because their reality
   never changes.) leaders, endpoint, and time stay freely
   editable.
6. [x] Drill it on the two-node lab: add a second NTP upstream via
   kubectl edit cluster and watch both machines stage, report
   RebootPending, apply on reboot, and show the new source in
   status.time with ClusterConverged True. Point the endpoint
   somewhere dead and reboot the follower: no join, no operator,
   no promotion, and the next boot rejects on the attempted
   marker and falls back to proven, rejoining the real cluster
   with the rejection visible in status. Then edit back to
   good and watch staged withdraw and the rejection clear with
   no reboot. All three drills ran as designed. The
   dead-endpoint boot was recovered by power cut, which is the
   only available recovery: a machine that never joined has no
   operator to request a reboot through.
