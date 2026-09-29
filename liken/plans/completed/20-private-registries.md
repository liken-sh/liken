# Private registries and the k3s restart tier

Milestone 20. Completed. A cluster declares registry mirrors and
credentials, and changes to k3s configuration apply with a process
restart, not a reboot.

This milestone covers how container images arrive on a fleet's
machines: mirror endpoints that containerd pulls through, credentials
that containerd presents, and k3s's embedded peer-to-peer registry
(Spegel). With Spegel, a fleet on a slow uplink pulls each image once
instead of once for every machine. Registry configuration is the first
cluster fact that actuates with a k3s process start and no reboot.
Other facts have the same property, so this milestone also built the
third convergence tier: the in-place k3s restart.

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
image arrives. This is closer to the network plan than to iscsi, and it
needs a parameterized shape, a map of hosts to endpoint lists. The
features map has a deliberately closed value schema that refuses such a
shape. spec.registries is on the Cluster because the scheduler may
ask any node to pull any image. It stays in the canonical staged
document, unlike spec.version and spec.releases, whose actuation is a
download. An edit therefore changes the document's hash and rolls the
fleet.

The CRD schema needed one defense more than the features drill showed.
The mirrors map takes the same nullable-plus-CEL defense that the
features map introduced, because the API server drops a null value for
a non-nullable field before validation runs. Without that defense, a
bare `docker.io:` in hand-written YAML declares nothing. This map's
values are arrays, and CEL types an array as list(string) and refuses
to compare it with null at compile time. The features map's
preserved-unknown-fields objects type as dyn, so its rule does not hit
this problem. The scratch-CRD drill showed the schema refusing to
install at all. The fix is a dyn() cast in the rule. The parity test in
machine/registries_test.go pins the pair.

## Credentials arrive in a Secret

Credentials do not travel inside the image, although the first sketch
of this plan put them there beside the join token, on the argument that
they are the same kind of material as the identity bundle. The
bootstrap argument gives the difference. The join token travels inside
the image because nothing can join a cluster without it. Registry
credentials gate nothing that the OS needs in order to become a
cluster. liken's own workload images travel inside the image as OCI
tarballs, so k3s starts, the operator runs, and the machine serves with
no registry reachable at all. Credentials gate user workloads, and
those rotate. Credentials inside the image would make every rotation an
image rebuild. So credentials arrive the way the ecosystem already
delivers them: a kubernetes.io/dockerconfigjson Secret named
`registry-credentials` in liken-system. This is the object that
`kubectl create secret docker-registry` produces and that
imagePullSecrets consume.

The credentials travel from the Secret to the machines as a liken
document. The machine operator reads the Secret on each pass, under a
namespaced Role that grants get on that one name. resourceNames makes
"one named Secret" enforceable, and the operator's ClusterRole keeps no
other Secret access. The operator renders the Secret into a canonical
RegistryCredentials document. It stages this document into a fourth
lifecycle store, registries/, beside manifests/, cluster/, and system/,
when its hash differs from what the machine last rendered. A deleted
Secret stages the empty document, which is a real rendering with a real
hash. Withdrawn credentials therefore use the same machinery as changed
credentials. A malformed Secret stages nothing: the machine keeps its
last good credentials and reports CredentialsInvalid with the phase
Blocked, and the message names the fix. The phase is Blocked because
time does not fix this, but a corrected Secret does.

Two smaller decisions are on the record. The operator is the document's
only author, with no image seed and no hand-written copy, so the
raw-bytes hash in the facts and the operator's rendering compare
directly, without the canonicalization pass that the cluster document
needs. Promotion also happens at actuation: init promotes staged
credentials when it writes registries.yaml, with no attempted marker
and no downstream proof, because the write is the whole actuation. A
wrong password shows as ImagePullBackOff in the cluster, and a Secret
edit fixes it. A fall back to older credentials on a later boot would
repair nothing and would hide the newest intent.

init renders /etc/rancher/k3s/registries.yaml from two inputs: the
mirrors and the embedded flag from the cluster document, and the auth
configurations from the credentials document. init is the file's only
author, and it writes the file with mode 0600, like the join token, on
leaders and followers alike. Credentials without mirrors still render.
A configs-only file is how authenticated pulls straight to Docker Hub
beat its anonymous rate limits. No declaration removes the file, so the
minimum stays the default. When embedded is on, the declared mirrors
render without change and a bare "*" entry joins them. With Spegel a
registry participates in peer-to-peer sharing only if registries.yaml
lists it as a mirror, and the wildcard is k3s's own way to name all of
them. The embedded-registry flag is a server-side key, rendered into
the leaders' boot drop-in.

## The restart tier

liken sorts a change by where its configuration is read. Some facts
reconcile live: sysctls and node labels are read continuously and the
operator reasserts them. Some are read early in a boot and nowhere
else: the address plan, storage claiming, and the time hierarchy need
the whole boot. Between them is everything that k3s reads only at
process start, the boot drop-in and registries.yaml. The disruption for
those is a restart of one process. A k3s restart leaves running
containers alone, because the containerd shims hold them. Only the control
plane and the kubelet stop briefly. Since milestone 17, liken used a
full reboot for this middle tier, at unnecessary cost: a traefik toggle
rolled the fleet through full reboots to change one line of a
configuration file that one process reads.

This milestone built the middle tier, and the feature toggles use it
too:

- **The operator classifies.** machine/changes.go names the cluster
  document's actuation domains and compares two specs domain by domain.
  It uses their JSON renderings, the same bytes that build the document
  hash, so the classifier and the hash cannot disagree. When every
  differing domain is restart-class (features, registries), the
  operator requests a restart. Anything else, a mixed edit, or an
  unreadable boot document falls to the reboot, the tier that always
  works. Machine-document changes (storage, modules) and system
  releases require a reboot in every case.

- **The intent is a sibling file.** A restart intent is beside the
  reboot intent in /run/liken/operator, deliberately not a field on it.
  init honors an unreadable reboot intent by rebooting anyway, so a new
  field there would cause a surprise reboot on any init that predates
  it. A sibling file stays invisible to such an init. The lifecycles
  also differ. A reboot intent needs no consumption, because /run is a
  fresh tmpfs every boot. A restart intent must be consumed, or the
  two-second poll bounces k3s forever. init clears the file before it
  bounces k3s. A crash between the two steps loses one restart, and the
  operator's next pass requests it again. The reverse order restarts
  forever.

- **Restarts take the same turns as reboots.** A leader's k3s restart
  bounces the embedded etcd, which creates the same quorum exposure
  that a reboot has. Restarts therefore flow through the rollout
  conductor's turn-granting without change. They wait under the same
  AwaitingTurn reason, so the conductor sequences both kinds without
  telling them apart: one leader at a time, and workers under the
  disruption budget. The drain is skipped by construction, not by a
  special case. The drain is gated on a requested reboot, and a restart
  never requests one, so the pods stay and there is nothing to move.

- **init applies changes while k3s still serves.** On a restart intent,
  init loads the staged documents and checks them exactly as a boot
  does. A document that fails to parse is quarantined, and one whose
  changes are reboot-class is left standing for the reboot path. Both
  programs use the same classifier, so they cannot disagree. init
  re-renders the boot drop-in and registries.yaml, runs the feature
  actuation again, publishes the facts again, and then bounces the k3s
  child process. The supervisor starts k3s again immediately. A
  deliberate bounce skips the crash-loop backoff, because that backoff
  is for k3s failures, not for liken's own decisions.

- **Promotion needed nothing new.** The cluster document's proof was
  always the operator observing the machine serving under it. The
  restart path writes the attempted marker and publishes facts that
  name the staged document, which is the state that a proving boot
  leaves. The operator's next pass promotes it. If k3s does not come
  back, the supervisor crash-loops in its existing domain, and the next
  real boot finds the attempted marker matching the staged document and
  rejects it with a fallback. The one-trial rule is unchanged and now
  covers both tiers.

- **The observable is status.boot.restarts.** It counts the in-place
  restarts of this boot, and it is in the boot record because it
  shares the boot's lifetime. A change that arrives by restart
  increases this count and does not move bootedAt. A change that
  arrives by reboot moves bootedAt and returns the count to zero. The
  drill asserts this asymmetry.

Live retraction works better on the restart tier than at boot. k3s
deletes an auto-deploy addon when its manifest file is removed while
k3s runs. The boot path cannot show this behavior, because the file
vanishes while k3s is down, and this is why the cluster operator's
janitor exists. The restart path removes a retracted feature's
manifests while k3s watches, so k3s deletes the workload itself. The
janitor stays in place for the boot path.

Inside init, the restart path made the facts file a shared resource for
the first time. The clock loop was its only owner after boot, and the
restart path is a second writer. The facts file therefore got a small
guarded owner, a mutex around mutate-and-rewrite, instead of an
ownership rule that only a comment enforces.

## The fixture and the drill

The lab's storage guest got a third fixture service: Debian's
docker-registry (CNCF distribution, the reference registry), set up as
an authenticated pull-through mirror of Docker Hub. It uses htpasswd
authentication with a committed lab credential, and plain HTTP on port
5000, because the machines trust only the Mozilla roots and containerd
treats an http:// endpoint as plain by its URL scheme alone. Its
storage is on the guest's root disk. One fixture now serves iscsi on
port 3260, nfs on port 2049, and registries on port 5000.

The drill ran against a fresh five-node fleet, and the restart tier
behaved as designed on four rolls in a row. A traefik toggle rolled all
five machines through k3s restarts: AwaitingTurn, one granted turn at a
time, workers first, then leaders one by one, with etcd quorum held
throughout. Afterward every bootedAt was byte-identical to its
baseline, every boot.restarts read 1, no node was cordoned, every pod
predated the roll, and traefik was serving. A declared mirror and a new
Secret rolled the fleet again, and both documents applied in a single
restart on each machine. status.registries reported mirrors ["*",
"docker.io"], the credentialed host, and embedded: true. Each console
printed the render and the promotion ("the staged credentials are now
proven").

The registry proofs came with numbers. The fixture refused an
unauthenticated /v2/ request with a 401 and served an authenticated one
with a 200, which settled the open question about htpasswd over plain
HTTP. A pod pinned to one node pulled an image that no machine held,
and the mirror's catalog listed the image afterward. The pull went
through the authenticated mirror and fetched from the Hub once. With
the registry stopped, the same image pulled on a second node in 443ms.
Only a peer can serve at that speed, and a contrast experiment
confirmed the attribution: a fresh image under the same dead mirror
took 2.5s, from the Hub. This also proved the fallback: a dead mirror
degrades to direct pulls, not to a broken fleet.

Retraction showed one nuance. A dropped iscsi feature deleted the
DaemonSet before any machine restarted, because the janitor acts on the
document edit, exactly as milestone 17 built it. Each machine's restart
then removed the seeded manifest from its own auto-deploy directory
("liken: restart: retracted iscsid.yaml"). On a live fleet the janitor
wins that race, and the restart path's own removal is the half that
keeps the auto-deploy directory correct. The two mechanisms work
together, and neither is redundant. A deleted Secret rolled the fleet
once more: credentialedHosts emptied and registries.yaml dropped its
configs section.

The failure drills held their lines. A syntactically invalid
dockerconfigjson never reached the operator, because the API server
validates typed Secrets and `kubectl` refused it at admission. A
wrong-typed Secret (Opaque, under the right name) did reach the
operator. Every machine went Blocked with CredentialsInvalid, nothing
staged, the restart counters frozen, and a message that quoted the
exact `kubectl create secret docker-registry` command that fixes it. A
delete of the bad Secret returned the fleet to Ready with no disruption
having happened. The reboot class stayed intact: a module declared on
one Machine drained the node, which no restart does, rebooted it for
real, moved its bootedAt, and reset its restart counter to zero. This
is the inverse of the restart signature.

## Open questions

- TLS to a private registry with a private CA (registries.yaml's tls
  block: ca_file, insecure_skip_verify) is deliberately out of scope.
  The credential Secret's dockerconfigjson shape has nowhere to hold a
  CA, and the correct way to deliver that material is a question for
  the deployment that first needs it.
- The restart tier stops at k3s's own configuration. The auto-deploy
  manifests that k3s watches make a lighter tier possible, with no
  process bounce, for changes that are pure manifest content. flux's
  re-point (milestone 14) is the first candidate, and the decision
  belongs there.
