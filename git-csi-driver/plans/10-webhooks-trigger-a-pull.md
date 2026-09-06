# 10, Webhooks trigger a pull

## The problem

A read-only volume finds that its ref moved when its `pull` timer
fires. `pull` defaults to `5m`, so a person who pushes to the forge
waits up to five minutes for the change to reach the pods. Shortening
`pull` costs a fetch per volume per interval on every node, whether or
not anything was pushed.

The forge already reports the push. GitHub, GitLab, Gitea, and Forgejo
all send an HTTP request on every push, and the payload names the
repository and the ref. The driver should accept that request and fetch
at once, so a push reaches the pods in seconds. `pull` stays, as the
backstop for a webhook that never arrives.

This plan is about read-only volumes. It leaves writeable volumes as
they are, and the section "What stays out" says why.

## The design

### The listener is in the controller plugin

The webhook listener runs in the controller `Deployment`. That
`Deployment` is one pod and it holds no volume. A `Service` and an
`Ingress` give a forge outside the cluster one address to post to.

The node plugins do the fetches, and no forge can reach them. The node
plugin is a `DaemonSet` on the pod network with no ingress of its own.
Two ways to reach the node plugins were weighed and both lose.

- **A listener on every node plugin, behind one `Service`.** A `Service`
  sends each request to one pod. That pod would fetch for its own node
  alone, and the other nodes would hear nothing. Fixing that needs the
  node that received the request to call the others, which is
  node-to-node HTTP the project does not build.
- **A per-node `Service` and a per-node `Ingress`.** Every node needs
  its own route, its own hostname, and its own entry in the forge's
  webhook list. Adding a node means editing the forge. The forge would
  also send one request per node for one push.

The controller reaches the node plugins through the API server
instead.

### The channel is an annotation on the `PersistentVolume`

The controller does not call the node plugins. It writes an annotation
on each `PersistentVolume` the webhook matches:

    git.liken.sh/pushed-at: "2026-09-06T14:31:07Z"

The `PersistentVolume` is the right object. It is the object that
carries `url` and `ref`, in `spec.csi.volumeAttributes`, so the
controller matches against the same fields the node plugin staged
from. It is cluster-scoped, so the driver writes nothing into a
tenant's namespace. Its `spec.csi` block is immutable after creation,
and its metadata is not, so an annotation is a legal write.

The node plugin holds one watch on `PersistentVolumes` for the whole
node. On each event it reads `spec.csi.volumeHandle` and finds the
staged volume with that handle. It fetches that volume's repository at
once when `git.liken.sh/pushed-at` differs from the value it last
acted on. The watch restarts when it closes, and it reads the list
again on a resync, the way `arming.pass` does today for a claim. Its
context descends from the driver's run, so the pod's stop ends it.

Two facts make this cheap. The node plugin already holds `get`,
`list`, and `watch` on `persistentvolumes` in `deploy/rbac.yaml`, and
the controller already holds `patch` on the same resource. This plan
adds no RBAC rule. One watch per node covers every volume the node
stages, rather than one watch per volume.

### The fetch goes through the follower

`follow.go` holds one `follower` per repository per node, shared by
every volume of that URL. `follower.nudge` resets the interval timer
and does not fetch. This plan adds a second wake that runs
`follower.tick` at once.

`tick` walks every volume of the repository, so one wake moves every
volume of that URL on the node, whatever object named it. An inline
volume of the same URL on the same node moves with the claim that was
annotated.

A volume with `pull: never` joins no follower, and a webhook does not
reach it. `never` says the volume follows nothing after its stage, and
a webhook does not change that.

### Matching a webhook to volumes

A push payload names a ref and a repository. Both need a rule, because
neither is written the way a volume writes it.

**The ref.** Every payload carries the full ref, such as
`refs/heads/main`. A volume's `ref` attribute is a branch or a tag
name, such as `main`. The payload's ref matches the attribute when it
equals the attribute, or equals `refs/heads/<attribute>`, or equals
`refs/tags/<attribute>`.

**The repository.** The payload carries several URLs and the volume
carries one, and the forms differ: `https://code.example.com/data/x.git`,
`ssh://git@code.example.com/data/x.git`, and
`git@code.example.com:data/x.git` are one repository. The proposed rule
reduces both sides to a comparison key: the lowercased host and the
path. The scheme, the userinfo, the port, a leading `/`, a trailing
`/`, and a trailing `.git` are removed. All three example URLs above
reduce to `code.example.com/data/x`. The controller builds that key
from each URL the payload carries and from each volume's `url`
attribute. A volume matches when any pair of keys is equal.

The rule drops the port because a volume often clones over one port
while the forge advertises another. The lab is one such case: the
volume clones over `git://` on port 9418. The cost is that two
repositories on one host and one path, on two ports, would match each
other. See the open questions.

### Authentication

Each forge signs or tokens its request differently. Every row below was
read on 2026-09-06.

| Forge | Header | Value | Source |
|---|---|---|---|
| GitHub | `X-Hub-Signature-256` | `sha256=` and the hex HMAC-SHA256 of the raw body | [Validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries) |
| GitLab | `X-Gitlab-Token` | The configured secret, as plain text | [Webhooks](https://docs.gitlab.com/user/project/integrations/webhooks/) |
| GitLab | `webhook-signature` | `v1,` and the base64 HMAC-SHA256 of `{webhook-id}.{webhook-timestamp}.{body}` | [Webhooks](https://docs.gitlab.com/user/project/integrations/webhooks/) |
| Gitea | `X-Gitea-Signature` | The hex HMAC-SHA256 of the raw body, with no prefix | [Webhooks](https://docs.gitea.com/usage/webhooks) |
| Forgejo | `X-Forgejo-Signature` | The hex HMAC-SHA256 of the raw body, with no prefix | [Webhooks](https://forgejo.org/docs/latest/user/webhooks/) |

GitLab's own documentation names the plain-text `X-Gitlab-Token` the
weaker of its two, and recommends the signing token for a new webhook.
The signing token signs more than the body, and its timestamp asks for
replay handling. The first cut verifies `X-Gitlab-Token` alone, and
the open questions carry the rest.

**Where the secret lives.** One `Secret` in the driver's namespace,
named by a flag, with one key per entry a person creates:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: git-csi-driver-webhooks
  namespace: liken-system
stringData:
  code.example.com: <the secret configured on the forge>
```

The request names the entry in its path: a forge posts to
`/webhook/code.example.com`. The controller reads that one key and
verifies the request against it. Nothing is chosen from the payload,
because the payload is what the verification is for, and one leaked
secret reaches one entry. A person picks the key names, so one entry
per forge and one entry per repository are both possible.

The controller reads the `Secret` on each request, under a deadline
and behind a short cache. A person then adds an entry with no restart
of the `Deployment`.

**What each answer means.** The controller matches the headers above to
choose the verification, and compares every HMAC in constant time.

| Answer | When |
|---|---|
| `401` | The path names no entry, no header the controller verifies is present, or the verification failed. The body is empty. |
| `400` | The body is not the JSON of a push, or it is over the size limit. |
| `202` | The request verified. The body names how many volumes the controller marked, so a person configuring a webhook reads `0` when the URL matches nothing. |

The listener bounds the body it reads and the time it holds a
connection, so an unverified caller cannot make the controller buffer
or wait. The webhook names no URL of its own, and it moves only
volumes that already exist. It is never a way to make the driver fetch
a repository nobody deployed.

The listener speaks plain HTTP, and the `Ingress` terminates TLS. That
is where the cluster already holds its certificates.

### The manual trigger

The channel is an annotation, so a person triggers a pull with no
forge at all:

```console
kubectl annotate pv franchises git.liken.sh/pushed-at="$(date -u +%FT%TZ)" --overwrite
```

The node plugin acts when the value differs from the one it last acted
on. Any new value works, and the timestamp is a convention. This is
the whole answer for a forge the driver does not verify. It is also
what a person runs to test the channel before the forge is
configured.

### Rate and safety

A forge that sends twenty webhooks in a burst must not make a node
fetch twenty times. The follower coalesces, because the follower is
the one fetch per repository per node. A wake fetches at once unless
the follower fetched less than `--webhook-min-interval` ago. In that
case the follower schedules one fetch at the end of that interval. It
drops every further wake until that fetch runs.

The default is a guess and the drill measures it. `10s` is the
starting value. It reaches the pods in seconds, and it bounds a burst
to six fetches a minute per repository per node.

A fetch a webhook started reports exactly as a fetch the timer started,
because it is the same code. `follower.trouble` posts one
`GitFetchFailed` on the first failure after a success. The volume's
report carries the failure until a fetch works, and
`git_csi_volume_abnormal` is one while it does. This plan adds no
failure path of its own.

### What the driver reports

**The controller's log** writes one line per request: the entry, the
forge the headers named, the ref, and the count of volumes marked. A
refused request writes one line with the reason and never the secret.

**The controller's metrics** already have a listener. `newServer`
builds the registry and opens `--metrics` for both plugins, and the
controller's registry is empty today. This plan puts two counters on
it:

| Metric | Meaning |
|---|---|
| `git_csi_webhook_requests_total`, labeled `result` | Requests by what they answered: `accepted`, `unauthenticated`, `malformed`. |
| `git_csi_webhook_marked_total` | `PersistentVolume`s the controller annotated. |

`deploy/controller.yaml` declares no container port for the metrics
listener, and this plan adds one, beside the port the webhook listener
takes.

**The node's metrics** take one counter,
`git_csi_webhook_fetches_total`, labeled `namespace` and `volume`, the
labels `git_csi_volume_abnormal` already takes.

**No new `Event`.** A fetch that moves the tree posts no `Event`
today, in `follower.refresh`, and a fetch a webhook started posts none
either. A repository pushed twenty times an hour would put twenty
`Event`s on the claim. An `Event` is for a state change a person has
to see.

### What stays out

**Writeable volumes.** Invariant 2 of the design is that while a pod
holds the tree, only the application changes it, and a rejected push.
Upstream reaches a writeable tree at stage, before the pod starts, and
through the rebase plan 09 describes. A webhook that fetched into a
mounted writeable tree would rewrite the pod's files at a moment
nobody chose. The code already states the same rule: a writeable stage
refuses `pull`, `depth`, and `offline` in `parseStageAttributes`. A
webhook that matches a writeable volume marks nothing, and the count
in the `202` does not include it.

**An inline volume the node reaches no other way.** An inline volume
has no `PersistentVolume` and no claim, so the controller has no
object to annotate for it. The follower is per repository, so an
inline volume moves on a webhook when a read-only claim on the same
node names the same URL. Every other inline volume waits for its
`pull`. The guide has to say so, and the open questions carry the
options.

## Considered and set aside

- **A `ConfigMap` per repository, in the driver's namespace, that
  every node plugin watches.** It reaches inline volumes and claims
  alike, and it costs one watch per node, the same as the annotation.
  It loses because the driver would create and delete objects of its
  own, and would need a sweep for the ones no volume follows. The
  driver provisions nothing today, and the annotation needs no object
  that does not already exist.
- **A path that names the volume, such as `/push/<PersistentVolume>`.**
  It removes the URL matching rule and every guess in it. It loses
  because one repository backs many volumes. A person would configure
  one webhook per volume, on a forge that sends one webhook per
  repository.

## Open questions

- **The comparison key drops the port.** Two repositories on one host
  and one path, on two ports, would match each other. Nothing in the
  lab or in a homelab has that shape. What would settle it: whether
  any real deployment serves two repositories that way. If one does,
  the answer is an attribute that states the key.
- **An attribute that states the match.** A volume could carry
  `webhook: code.example.com/data/x` and skip the rule. It is exact
  and it is one more attribute a person has to set. The question is
  whether the rule fails often enough to be worth it.
- **GitLab's signing token.** Its signature covers
  `{webhook-id}.{webhook-timestamp}.{body}` and its timestamp asks for
  a replay window. What would settle it: whether a GitLab deployment
  needs it before the plain `X-Gitlab-Token` is enough.
- **Reaching an inline volume.** The `ConfigMap` above is one option.
  An annotation on the `CSIDriver` object is another, because that
  object exists, is cluster-scoped, and is the driver's own. It holds
  one value for the whole cluster, so every push rewrites one object
  and the value needs pruning. A third is to leave inline volumes on
  `pull` forever. What would settle it: whether a person deploys an
  inline volume that needs seconds.
- **Whether `deploy/` carries the listener.** The flag defaulting to
  empty keeps the endpoint off until a person turns it on, which is
  right for an endpoint a forge reaches. The question is whether the
  base should still carry the `Service`, with the flag off, or whether
  the guide's kustomize patch adds both.
- **`--webhook-min-interval` at `10s`.** A guess. The drill measures
  what a burst costs.
- **A webhook that arrives while the controller restarts is lost.**
  The `Deployment` is one replica. `pull` is the backstop, and the
  loss is bounded by `pull`. The question is whether that is enough,
  or whether the controller should run two replicas.

## The drill

Drill 10, `lab/drills/10.sh`. The lab's forge is `git daemon`, which
sends no webhook, so the drill posts the request itself. The payload
and its HMAC-SHA256 are computed on the host with `openssl`. A
`debian:12-slim` pod then posts them to the controller's `Service`
inside the cluster with `curl`. The lab runs no ingress controller, so
the `Service` is the endpoint the drill reaches, and the guide covers
the `Ingress`.

1. Apply the webhook `Secret` in `liken-system`, and patch the
   controller `Deployment` with the listener flag and a `Service`.
2. Apply a `ReadOnlyMany` `PersistentVolume` and claim on the forge's
   repository with `pull: 30m`, and a reader pod. The pod reads the
   greeting. `pull` is long, so nothing but a webhook can move the
   tree inside the drill.
3. Push a new greeting to the forge. Post a signed GitHub-shaped push
   payload naming the repository and `refs/heads/main`. The answer is
   `202` and names one volume. The pod reads the new greeting within
   30 seconds.
4. Post the same payload with a wrong signature. The answer is `401`,
   and the tree does not move.
5. Post a signed payload naming a repository no volume follows. The
   answer is `202` and names zero volumes.
6. Push again, then post twenty signed payloads at once. The driver's
   log shows one fetch, or two, inside `--webhook-min-interval`.
7. Push again and annotate the `PersistentVolume` by hand. The pod
   reads the third greeting.
8. Run an inline volume of the same repository in another namespace on
   the node, and one of a second repository. The first moves with the
   claim. The second does not, which is the gap on record.

Drill 03 and drill 07 still pass, because the timer path is unchanged.

## What is done when

- The controller serves a webhook listener on the address
  `--webhook` names, and serves none when the flag is empty.
- A verified push annotates every `PersistentVolume` of this driver
  whose `url` and `ref` match, and no writeable volume.
- An unverified, an unparsed, and an unmatched request each answer as
  the table above says, and the answer body never carries a secret.
- The node plugin holds one watch on `PersistentVolumes`, wakes the
  follower of a volume whose `git.liken.sh/pushed-at` changed, and
  fetches at once, bounded by `--webhook-min-interval`.
- `kubectl annotate` on a `PersistentVolume` moves the tree with no
  forge.
- The unit tests prove each verification and each answer. They prove
  the comparison key against the URL forms in this plan, the ref rule,
  the coalescing, and the watch that reconnects. Coverage stays at
  100%.
- Drill 10 passes in the lab, and drills 03 and 07 still pass.
- The read-only guide has a webhooks section with the `Secret`, the
  `Service`, the `Ingress`, the forge's settings, the manual trigger,
  and the inline gap. The install guide lists the new flags and the
  ports.
- Two comments this plan makes wrong are fixed with it. The
  `ClusterRole` in `deploy/controller.yaml` says every rule is the
  sidecar's, and the driver container now reads and patches
  `PersistentVolume`s. `controller.go` says the controller holds no
  state, and it now holds a client and a cache.
