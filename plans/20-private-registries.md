# Private registries, and the k3s restart tier

Milestone 20 — Done

This milestone covers how container images arrive on a fleet's
machines: mirror endpoints that containerd pulls through, credentials
that containerd presents, and k3s's embedded peer-to-peer registry
(Spegel), so that a fleet on a slow uplink pulls each image once
instead of once for every machine. Building this surfaced a piece of
machinery that this plan did not set out to build. Registry
configuration is the first cluster fact whose actuation never needs a
reboot, only a k3s process start. It turned out not to be alone, so
this milestone also built the third convergence tier: the in-place
k3s restart.

## The declaration

`spec.registries` on the Cluster is a dedicated object beside the
feature vocabulary, not a row inside it:

    spec:
      registries:
        mirrors:
          docker.io:
            - http://10.10.0.100:5000
        embedded: true

Features are payload opt-ins from a curated vocabulary: capabilities
that the fleet offers. Registries are configuration about how every
image arrives. This is closer kin to the network plan than to iscsi,
and it needs a parameterized shape (a map of hosts to endpoint lists)
that the features map's deliberately closed value schema exists to
refuse. spec.registries lives on the Cluster because the scheduler
may ask any node to pull any image. It stays in the canonical staged
document (unlike spec.version and spec.releases, whose actuation is a
download), so an edit changes the document's hash and rolls the
fleet.

The CRD schema needed one lesson that the features drill had not
already taught. The mirrors map takes the same nullable-plus-CEL
defense that the features map pioneered, because the API server drops
a null value for a non-nullable field before validation runs. Without
that defense, a bare `docker.io:` in hand-written YAML would quietly
declare nothing. But this map's values are arrays, and CEL types an
array as list(string) and refuses to compare it with null at compile
time. (The features map's preserved-unknown-fields objects type as
dyn, so its rule never hit this problem.) The scratch-CRD drill caught
the schema refusing to install at all, and the fix is a dyn() cast in
the rule. The parity test in machine/registries_test.go pins the pair.

## Credentials: a Secret, not the image

The original sketch of this plan had credentials travel inside the
image like the join token, on the argument that they are the same
kind of material as the identity bundle. Building it the other way
was deliberate, and the bootstrap argument explains the difference.
The join token travels inside the image because nothing can join a
cluster without it. Registry credentials gate nothing that the OS
needs in order to become a cluster: liken's own workload images
travel inside the image as OCI tarballs, so k3s starts, the operator
runs, and the machine serves with no registry reachable at all. What
credentials gate is user workloads, and those rotate. Carrying
credentials inside the image would make every rotation an image
rebuild. So credentials enter the way the ecosystem
already delivers them: a kubernetes.io/dockerconfigjson Secret named
`registry-credentials` in liken-system, the exact object that
`kubectl create secret docker-registry` produces and that
imagePullSecrets consume.

The credentials travel from the Secret to the machines as a liken
document. The machine operator reads the Secret on each pass, under a
namespaced Role that grants get on that one name. (resourceNames is
what makes "one named Secret" enforceable, and the operator's
ClusterRole keeps zero Secret access otherwise.) The operator renders
the Secret into a canonical RegistryCredentials document. It stages
this document into a fourth lifecycle store (registries/, beside
manifests/, cluster/, and system/) whenever its hash differs from what
the machine last rendered. A deleted Secret stages the empty document,
a real rendering with a real hash. This is what lets "credentials
withdrawn" use the same machinery as "credentials changed". A
malformed Secret stages nothing: the machine keeps its last good
credentials, reports CredentialsInvalid (phase Blocked, because time
will not fix this; a corrected Secret will), and the message names the
fix.

Two smaller choices are worth recording. The operator is the
document's only author, with no image seed and no hand-written copy,
so the raw-bytes hash in the facts and the operator's rendering
compare directly, with none of the canonicalization pass that the
cluster document needs. Promotion also happens at actuation: init
promotes staged credentials the moment it writes registries.yaml, with
no attempted marker and no downstream proof, because the write is the
whole actuation. A wrong password's symptom, ImagePullBackOff, is
visible in the cluster and fixed by a Secret edit. Falling back to
older credentials on a later boot would repair nothing while hiding
the newest intent.

init renders /etc/rancher/k3s/registries.yaml from two inputs: mirrors
and the embedded flag from the cluster document, and auth
configurations from the credentials document. init is the file's sole
author, and it writes the file mode 0600, like the join token, on
leaders and followers alike. Credentials without mirrors still render:
a configs-only file is how authenticated pulls straight to Docker Hub
beat its anonymous rate limits. Nothing declared anywhere removes the
file, so the minimum stays the default. When embedded is on, the
declared mirrors render verbatim and a bare "*" entry joins them,
because with Spegel a registry participates in peer-to-peer sharing
only if registries.yaml lists it as a mirror, and the wildcard is
k3s's own way to say all of them. The embedded-registry flag itself is
a server-side key, rendered into the leaders' boot drop-in.

## The restart tier

Planning the convergence forced a question that this repo had been
collapsing: what, exactly, does a change need a reboot for? The answer
is a taxonomy sorted by where the configuration is read. Some facts
reconcile live: sysctls and node labels are read continuously and
reasserted by the operator. Some are read early in a boot and nowhere
else: the address plan, storage claiming, and the time hierarchy need
the whole boot. In between sits everything that k3s reads only at
process start, the boot drop-in and registries.yaml, where the actual
disruption is restarting one process. A k3s restart does not touch
running containers, because the containerd shims hold them; only the
control plane and kubelet stop briefly. liken had been using a full
reboot for this middle tier since milestone 17, at unnecessary cost:
toggling traefik rolled the
fleet through full reboots, to change one line of a config file that
one process reads.

So this milestone built the middle tier, and the feature toggles use
it too:

- **The operator classifies.** machine/changes.go names the cluster
  document's actuation domains and compares two specs domain by
  domain, using their JSON renderings, the same bytes the document
  hash is built from. So the classifier and the hash can never
  disagree. When every differing domain is restart-class (features,
  registries), the operator requests a restart. Anything else, a
  mixed edit, or an unreadable boot document falls to the reboot, the
  tier that always works. Machine-document changes (storage, modules)
  and system releases still require a reboot in every case.

- **The intent is a sibling file.** A restart intent lives beside the
  reboot intent in /run/liken/operator, deliberately not a field on
  it. init honors an unreadable reboot intent by rebooting anyway, so
  adding a new field there would read as a surprise reboot to any init
  that predates it, while a sibling file stays invisible to one. The
  lifecycles differ too. A reboot intent needs no consuming, since
  /run is a fresh tmpfs every boot. A restart intent must be consumed,
  or the two-second poll would bounce k3s forever. init clears the
  file before bouncing k3s: a crash between the two steps loses one
  restart, and the operator's next pass re-requests it. The reverse
  order would restart forever.

- **Restarts take the same turns as reboots.** A leader's k3s restart
  bounces embedded etcd, which creates the same quorum exposure that a
  reboot has. So restarts flow through the rollout conductor's
  turn-granting unchanged. They wait under the same AwaitingTurn
  reason, deliberately, so the conductor sequences both kinds without
  telling them apart: one leader at a time, and workers under the
  disruption budget. The drain is skipped by construction, not by a
  special case: draining is gated on a requested reboot, and a restart
  never requests one, so pods survive and there is nothing to move.

- **init applies changes while k3s still serves.** On a restart
  intent, init loads the staged documents and checks them exactly as
  a boot would: a document that fails to parse is quarantined, and one
  whose changes are reboot-class is left standing for the reboot path.
  (Both programs consult the same classifier, so they can never
  disagree.) init re-renders the boot drop-in and registries.yaml,
  re-runs feature actuation, republishes the facts, and only then
  bounces the k3s child process. The supervisor starts k3s again
  immediately: a deliberate bounce skips the crash-loop backoff
  entirely, because that backoff exists for k3s failures, not for
  liken's own decisions.

- **Promotion needed nothing new.** The cluster document's proof was
  always the operator observing the machine serving under it. The
  restart path writes the attempted marker and republishes facts
  naming the staged document, exactly the state that a proving boot
  leaves. The operator's next pass promotes it. If k3s never comes
  back, the supervisor crash-loops, in its existing domain, and the
  next real boot finds the attempted marker matching the staged
  document and rejects it with fallback. The one-trial rule is
  unchanged, and now covers both tiers.

- **The observable is status.boot.restarts.** It counts the in-place
  restarts that this boot has performed, and it lives in the boot
  record because it shares the boot's lifetime. A change that arrives
  by restart increments this count without moving bootedAt. A change
  that arrives by reboot moves bootedAt and returns the count to zero.
  The drill asserts this exact asymmetry.

One pleasant surprise: live retraction works better on the restart
tier than at boot. k3s deletes an auto-deploy addon when its manifest
file is removed while k3s runs. The boot path cannot show this
behavior, because the file vanishes while k3s is down, and this is why
the cluster operator's janitor exists. The restart path removes a
retracted feature's manifests while k3s watches, so k3s deletes the
workload on its own. The janitor stays in place, for exactly the boot
path.

Inside init, the restart path made the facts file a shared resource
for the first time. The clock loop had been its sole owner after boot,
and the restart path is a second writer. So the facts file got a
small guarded owner (a mutex around mutate-and-rewrite), rather than
an ownership rule that only a comment would enforce.

## The fixture and the drill

The lab's storage guest grew a third fixture service: Debian's
docker-registry (CNCF distribution, the reference registry), set up as
an authenticated pull-through mirror of Docker Hub. It uses htpasswd
authentication with a committed lab credential, plain HTTP on port
5000 (the machines trust only the Mozilla roots, and containerd treats
an http:// endpoint as plain by its URL scheme alone), with storage on
the guest's root disk. One fixture now serves iscsi on port 3260, nfs
on port 2049, and registries on port 5000.

The drill ran against a fresh five-node fleet, and the restart tier
behaved as designed on four rolls in a row. Toggling traefik rolled
all five machines through k3s restarts: AwaitingTurn, one granted turn
at a time, workers first, then leaders one by one, with etcd quorum
held throughout. Afterward, every bootedAt was byte-identical to its
baseline, every boot.restarts read 1, no node was ever cordoned, every
pod predated the roll, and traefik was serving. Declaring the mirror
and creating the Secret rolled the fleet again, with both documents
applying in a single restart on each machine. status.registries
reported mirrors ["*", "docker.io"], the credentialed host, and
embedded: true, and each console printed the render and the promotion
("the staged credentials are now proven").

The registry proofs came with numbers. The fixture refused an
unauthenticated /v2/ request with a 401 and served an authenticated
one with a 200, which settled the open question about htpasswd over
plain HTTP. A pod pinned to one node pulled an image that no machine
held, and the mirror's catalog listed the image afterward: the pull
provably went through the authenticated mirror and fetched from the
Hub once. Then, with the registry stopped, the same image pulled on a
second node in 443ms. Only a peer could have served it that fast, and
a contrast experiment confirmed the attribution: a genuinely fresh
image under the same dead mirror took 2.5s, coming from the Hub. This
also proved the fallback: a dead mirror degrades to direct pulls,
never to a broken fleet.

Retraction taught the drill's one nuance. Dropping iscsi deleted the
DaemonSet before any machine restarted, because the janitor acts on
the document edit, exactly as milestone 17 built it. Each machine's
restart then removed the seeded manifest from its own auto-deploy
directory ("liken: restart: retracted iscsid.yaml"). On a live fleet
the janitor wins that race, and the restart path's own removal is the
half that keeps the auto-deploy directory correct. The two mechanisms
work together; neither is redundant. Deleting the Secret rolled the
fleet once more, and credentialedHosts emptied while registries.yaml
dropped its configs section.

The failure drills held their lines. A syntactically invalid
dockerconfigjson never reached the operator at all: the API server
validates typed Secrets, so `kubectl` refused it at admission. A
wrong-typed Secret (Opaque, under the right name) did reach the
operator, and every machine went Blocked with CredentialsInvalid,
nothing staged, restart counters frozen, and a message quoting the
exact `kubectl create secret docker-registry` command that fixes it.
Deleting the bad Secret returned the fleet to Ready with no disruption
having happened. The reboot class survived intact too: a module
declared on one Machine drained the node (which no restart ever did),
rebooted it for real, moved its bootedAt, and reset its restart
counter to zero, the exact inverse of the restart signature.

## Open questions

- TLS to a private registry with a private CA (registries.yaml's tls
  block: ca_file, insecure_skip_verify) is deliberately out of scope.
  The credential Secret's dockerconfigjson shape has nowhere to carry
  a CA, and the right way to deliver that material is a question for
  the deployment that first needs it.
- The restart tier stops at k3s's own configuration. The auto-deploy
  manifests that k3s watches suggest an even lighter tier (no process
  bounce at all) for changes that are pure manifest content. flux's
  re-point (milestone 14) is the first candidate, and the decision
  belongs there.
