# Private registries, and the k3s restart tier

Milestone 20 — Done

How container images arrive on a fleet's machines: mirror endpoints
containerd pulls through, credentials it presents, and k3s's embedded
peer-to-peer registry (Spegel), so a fleet on a slow uplink pulls
each image once instead of once per machine. Building it surfaced a
piece of machinery this plan didn't set out to build. Registry
configuration is the first cluster fact whose actuation never needed
a reboot, only a k3s process start, and it turned out not to be
alone, so this milestone also built the third convergence tier: the
in-place k3s restart.

## The declaration

`spec.registries` on the Cluster, a dedicated object beside the
feature vocabulary rather than a row in it:

    spec:
      registries:
        mirrors:
          docker.io:
            - http://10.10.0.100:5000
        embedded: true

Features are payload opt-ins from a curated vocabulary: capabilities
the fleet offers. Registries are configuration about how every image
arrives, closer kin to the network plan than to iscsi, and a
parameterized shape (a map of hosts to endpoint lists) that the
features map's deliberately closed value schema exists to refuse. It
lives on the Cluster because any node may be asked to pull any
image. It stays in the canonical staged document (unlike
spec.version and spec.releases, whose actuation is a download), so
an edit changes the document's hash and rolls the fleet.

The CRD schema needed one lesson the features drill hadn't already
taught. The mirrors map takes the same nullable-plus-CEL defense the
features map pioneered, because the API server drops a null value
for a non-nullable field before validation runs; without it, a bare
`docker.io:` in hand-written YAML would quietly declare nothing. But
this map's values are arrays, which CEL types as list(string) and
refuses to compare with null at compile time (the features map's
preserved-unknown-fields objects type as dyn, so its rule never hit
this). The scratch-CRD drill caught the schema refusing to install
at all, and the fix is a dyn() cast in the rule. The parity test in
machine/registries_test.go pins the pair.

## Credentials: a Secret, not the image

The original sketch of this plan had credentials riding the image
like the join token, on the argument that they are the same kind of
material as the identity bundle. Building it the other way was
deliberate, and the bootstrap argument is the difference. The join
token rides the image because nothing can join a cluster without it.
Registry credentials gate nothing the OS needs to become a cluster:
liken's own workload images ride the image as OCI tarballs, so k3s
starts, the operator runs, and the machine serves with no registry
reachable at all. What credentials gate is user workloads, and they
rotate; riding the image would make every rotation an image rebuild.
So they enter the way the ecosystem already delivers them: a
kubernetes.io/dockerconfigjson Secret named `registry-credentials`
in liken-system, the exact object `kubectl create secret
docker-registry` produces and imagePullSecrets consume.

From the Secret to the machines, the credentials become a liken
document. The machine operator reads the Secret each pass, under a
namespaced Role granting get on that one name (resourceNames is what
makes "one named Secret" enforceable, and the operator's ClusterRole
keeps zero Secret access). It renders the Secret into a canonical
RegistryCredentials document and stages it into a fourth lifecycle
store (registries/, beside manifests/, cluster/, and system/)
whenever its hash differs from what the machine last rendered. A
deleted Secret stages the *empty* document, a real rendering with a
real hash, which is what lets "credentials withdrawn" ride the same
machinery as "credentials changed". A malformed Secret stages
nothing: the machine keeps its last good credentials, reports
CredentialsInvalid (phase Blocked, because time won't fix it; a
corrected Secret will), and the message names the fix.

Two smaller choices are worth recording. The operator is the
document's only author, with no image seed and no hand-written copy,
so the raw-bytes hash in the facts and the operator's rendering
compare directly, with none of the canonicalization pass the cluster
document needs. And promotion happens at actuation: init promotes
staged credentials the moment registries.yaml is written, with no
attempted marker and no downstream proof, because the write is the
whole actuation and a wrong password's symptom (ImagePullBackOff) is
visible in the cluster and fixed by a Secret edit. Falling back to
older credentials on a later boot would repair nothing while hiding
the newest intent.

Init renders /etc/rancher/k3s/registries.yaml from the two inputs,
mirrors and the embedded flag from the cluster document and auth
configs from the credentials document, as its sole author, 0600 like
the join token, on leaders and followers alike. Credentials without
mirrors still render: a configs-only file is how authenticated pulls
straight to Docker Hub beat its anonymous rate limits. Nothing
declared anywhere removes the file, so the minimum stays the
default. When embedded is on, the declared mirrors render verbatim
and a bare "*" entry joins them, because with Spegel a registry
participates in peer-to-peer sharing only if registries.yaml lists
it as a mirror, and the wildcard is k3s's own way to say all of
them. The embedded-registry flag itself is a server-side key,
rendered into the leaders' boot drop-in.

## The restart tier

Planning the convergence forced a question this repo had been
collapsing: what, exactly, does a change need a reboot *for*? The
answer is a taxonomy by where the configuration is read. Some facts
reconcile live (sysctls, node labels: read continuously, reasserted
by the operator). Some are read early in a boot and nowhere else
(the address plan, storage claiming, the time hierarchy): those need
the whole boot. And in between sits everything k3s reads only at
process start, the boot drop-in and registries.yaml, where the
honest disruption is restarting one process. A k3s restart does not
touch running containers (the containerd shims hold them); only the
control plane and kubelet blip. liken had been paying the reboot
price for this middle tier since milestone 17: toggling traefik
rolled the fleet through full reboots to change one line of a config
file one process reads.

So this milestone built the middle tier, and the feature toggles
ride it too:

- **The operator classifies.** machine/changes.go names the cluster
  document's actuation domains and compares two specs domain by
  domain, by their JSON renderings, the same bytes the document hash
  is built from, so the classifier and the hash can never disagree.
  When every differing domain is restart-class (features,
  registries), the operator requests a restart; anything else, a
  mixed edit, or an unreadable boot document falls to the reboot,
  the tier that always works. Machine-document changes (storage,
  modules) and system releases keep the reboot unconditionally.

- **The intent is a sibling file.** A restart intent lives beside
  the reboot intent in /run/liken/operator, deliberately not a field
  on it: init honors an *unreadable* reboot intent by rebooting
  anyway, so a new field would read as a surprise reboot to any init
  that predates it, while a sibling file is invisible to one. The
  lifecycles differ too. A reboot intent needs no consuming, since
  /run is a fresh tmpfs every boot, but a restart intent must be
  consumed, or the two-second poll would bounce k3s forever. Init
  clears the file *before* bouncing: a crash between the two loses
  one restart, and the operator's next pass re-requests it, where
  the reverse order would restart forever.

- **Restarts take the same turns as reboots.** A leader's k3s
  restart bounces embedded etcd, which is exactly the quorum
  exposure a reboot has, so restarts flow through the rollout
  conductor's turn-granting unchanged. They wait under the same
  AwaitingTurn reason, deliberately, so the conductor sequences both
  kinds without knowing the difference: one leader at a time,
  workers under the disruption budget. The drain is skipped by
  construction, not by a special case: draining is gated on a
  requested *reboot*, and a restart never requests one; pods
  survive, so there is nothing to move.

- **Init applies, while k3s still serves.** On a restart intent,
  init loads the staged documents and vets them exactly as a boot
  would: a document that won't parse is quarantined, and one whose
  changes are reboot-class is left standing for the reboot path
  (both programs consult the same classifier, so they can never
  disagree). It re-renders the boot drop-in and registries.yaml,
  re-runs feature actuation, republishes the facts, and only then
  bounces the k3s child. The supervisor starts it again immediately:
  a deliberate bounce skips the crash-loop backoff entirely, whose
  subject is k3s failures, not liken decisions.

- **Promotion needed nothing new.** The cluster document's proof was
  always the operator observing the machine serving under it. The
  restart path writes the attempted marker and republishes facts
  naming the staged document, exactly the state a proving boot
  leaves, and the operator's next pass promotes it. If k3s never
  comes back, the supervisor crash-loops (its existing domain), and
  the next real boot finds the attempted marker matching the staged
  document and rejects it with fallback: the one-trial rule,
  unchanged, now covering both tiers.

- **The observable is status.boot.restarts.** It counts the in-place
  restarts this boot has performed, and it lives in the boot record
  because it shares the boot's lifetime: a change that arrived by
  restart increments it without moving bootedAt, and a change that
  arrived by reboot moves bootedAt and returns it to zero. That
  asymmetry is what the drill asserts.

One pleasant surprise: live retraction works better on the restart
tier than at boot. k3s deletes an auto-deploy addon when its
manifest file is removed while k3s runs, which is exactly the
behavior the boot path can't have (the file vanishes while k3s is
down), and why the cluster operator's janitor exists. The restart
path removes a retracted feature's manifests while k3s watches, so
k3s deletes the workload natively. The janitor stays, for exactly
the boot path.

Inside init, the restart path made the facts file a shared resource
for the first time: the clock loop had been its sole owner after
boot, and the restart path is a second writer, so the facts got a
small guarded owner (a mutex around mutate-and-rewrite) rather than
an ownership rule a comment has to enforce.

## The fixture and the drill

The lab's storage guest grew the third fixture service: Debian's
docker-registry (CNCF distribution, the reference registry) as an
authenticated pull-through mirror of Docker Hub. htpasswd auth with
a committed lab credential, plain HTTP on 5000 (the machines trust
only the Mozilla roots, and containerd treats an http:// endpoint as
plain by its URL scheme alone), storage on the guest's root disk.
One fixture answers for iscsi on 3260, nfs on 2049, and registries
on 5000.

The drill ran against a fresh five-node fleet, and the restart tier
behaved as designed four rolls in a row. Toggling traefik rolled all
five machines through k3s restarts: AwaitingTurn, one granted turn
at a time, workers first, then leaders one by one with etcd quorum
held throughout. Afterward every bootedAt was byte-identical to its
baseline, every boot.restarts read 1, no node was ever cordoned,
every pod predated the roll, and traefik was serving. Declaring the
mirror and creating the Secret rolled the fleet again, both
documents applying in a single restart per machine, with
status.registries reporting mirrors ["*", "docker.io"], the
credentialed host, and embedded: true, and each console printing the
render and the promotion ("the staged credentials are now proven").

The registry proofs came with numbers. The fixture refused an
unauthenticated /v2/ with a 401 and served an authenticated one a
200, which settled the open question about htpasswd over plain HTTP.
A pod pinned to one node pulled an image no machine held, and the
mirror's catalog listed it afterward: the pull provably went through
the authenticated mirror, fetched from the Hub once. Then, with the
registry stopped, the same image pulled on a second node in 443ms.
Only a peer could have served it that fast, and the contrast
experiment made the attribution solid: a genuinely fresh image under
the same dead mirror took 2.5s coming from the Hub, which also
proved the fallback (a dead mirror degrades to direct pulls, never
to a broken fleet).

Retraction taught the drill's one nuance. Dropping iscsi deleted the
DaemonSet before any machine restarted, because the janitor acts on
the document edit, exactly as milestone 17 built it; each machine's
restart then removed the seeded manifest from its own auto-deploy
directory ("liken: restart: retracted iscsid.yaml"). On a live fleet
the janitor wins that race, and the restart path's native removal is
the half that keeps the auto-deploy directory truthful. The two are
complementary, not redundant. Deleting the Secret rolled the fleet
once more, credentialedHosts emptying and registries.yaml dropping
its configs section.

The failure drills held their lines. A syntactically-invalid
dockerconfigjson never reached the operator at all: the API server
validates typed Secrets, so `kubectl` refused it at admission. A
wrong-typed Secret (Opaque, under the right name) did reach the
operator, and every machine went Blocked with CredentialsInvalid,
nothing staged, restart counters frozen, and a message quoting the
exact `kubectl create secret docker-registry` command that fixes it;
deleting the bad Secret returned the fleet to Ready with no
disruption having happened. And the reboot class survived intact: a
module declared on one Machine drained the node (which no restart
ever did), rebooted it for real, moved its bootedAt, and reset its
restart counter to zero — the exact inverse of the restart
signature.

## Open questions

- TLS to a private registry with a private CA (registries.yaml's
  tls block: ca_file, insecure_skip_verify) is deliberately out of
  scope. The credential Secret's dockerconfigjson shape has nowhere
  to carry a CA, and the right delivery for that material is a
  question for the deployment that first needs it.
- The restart tier stops at k3s's own config. The auto-deploy
  manifests k3s watches live suggest an even lighter tier (no
  process bounce at all) for changes that are pure manifest content;
  flux's re-point (milestone 14) is the first candidate, and the
  decision belongs there.
