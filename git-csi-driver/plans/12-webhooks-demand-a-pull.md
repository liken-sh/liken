# 12, Webhooks demand a pull

## The problem

Plan 10 gives the driver a channel: an annotation on a
`PersistentVolume` demands a pull. A person can write it, and a script
can write it, but the thing that knows a push happened is the forge.
GitHub, GitLab, Gitea, and Forgejo all send an HTTP request on every
push, and the payload names the repository and the ref.

This plan accepts that request and writes the annotation, so a push
reaches the pods in seconds, and a volume with `pull: on-demand` needs
no timer at all. A `Deployment` that serves a static site from a
read-only volume then updates on every push, with no image build.

## The design

### The listener runs in the controller plugin

The webhook listener runs in the controller `Deployment`. That
`Deployment` is one pod and it holds no volume. A `Service` in the base
manifests and an `Ingress` the person writes give a forge outside the
cluster one address to post to.

The node plugins do the pulls, and no forge can reach them. The node
plugin is a `DaemonSet` on the pod network with no ingress of its own.
Two ways to reach the node plugins were weighed and both lose.

- **A listener on every node plugin, behind one `Service`.** A `Service`
  sends each request to one pod. That pod would pull for its own node
  alone, and the other nodes would hear nothing.
- **A per-node `Service` and a per-node `Ingress`.** Every node needs
  its own route and its own entry in the forge's webhook list. Adding
  a node means editing the forge.

The controller reaches the node plugins through the channel plan 10
built. It writes `git.liken.sh/pull-requested-at` on each
`PersistentVolume` the webhook matches, and the node plugins do the
rest. The controller already holds `patch` on `persistentvolumes` in
`deploy/controller.yaml`.

### The listener always serves

The listener needs no flag to turn it on. It serves on `--webhook`,
which defaults to `:8080`, and the base manifests declare the port and
the `Service`. An empty `--webhook` serves none, the way an empty
`--metrics` does. A request to a path that names no `Secret` answers
`401`, so a listener nobody configured refuses everything. It is
unreachable from outside the cluster until a person writes the
`Ingress`, and turning it on is then two objects the person owns: the
`Secret` and the `Ingress`. No patch to the `Deployment`.

The listener speaks plain HTTP, and the `Ingress` terminates TLS. That
is where the cluster already holds its certificates.

### The `Secret` is the tenant's

A read-only claim already names its deploy key through
`nodeStageSecretRef`, a `Secret` in the claim's namespace. The webhook
secret follows the same shape. The volume names it with one attribute
beside `url` and `ref`:

```yaml
spec:
  csi:
    driver: git.liken.sh
    volumeAttributes:
      url: https://code.example.com/data/x.git
      ref: main
      pull: on-demand
      webhookSecret: x-webhook
```

`webhookSecret` names a `Secret` in the namespace of the claim that
binds the `PersistentVolume`. The `Secret` holds one key, `secret`,
whose value is the string the person typed into the forge's webhook
form:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: x-webhook
  namespace: sites
stringData:
  secret: <the secret configured on the forge>
```

The forge posts to `/webhook/<namespace>/<name>`, here
`/webhook/sites/x-webhook`. The controller reads that one `Secret`,
verifies the request against it, and then matches only the volumes
whose `webhookSecret` names that `Secret` from that namespace. A push
that verifies against one team's `Secret` can only ever mark that
team's volumes. Nothing is chosen from the payload, because the
payload is what the verification is for.

The cost is one RBAC rule. The driver reads no `Secret` today, because
the kubelet hands it the deploy key. The controller gains `get` on
`secrets` in every namespace, and RBAC cannot narrow that to "the ones
a volume names". The `ClusterRole` comment in `deploy/controller.yaml`
says why the rule is there.

The controller reads the `Secret` on each request, under a deadline
and behind a short cache, so a person rotates the secret with no
restart of the `Deployment`.

The claim's namespace comes from `spec.claimRef` on the
`PersistentVolume`. A `PersistentVolume` with no claim, or with a claim
in another namespace than the path names, is not matched.

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
`git@code.example.com:data/x.git` are one repository. The rule reduces
both sides to a comparison key: the lowercased host and the path. The
scheme, the userinfo, the port, a leading `/`, a trailing `/`, and a
trailing `.git` are removed. All three example URLs above reduce to
`code.example.com/data/x`. The controller builds that key from each
URL the payload carries and from each volume's `url` attribute. A
volume matches when any pair of keys is equal.

The rule drops the port because a volume often clones over one port
while the forge advertises another. The lab is one such case: the
volume clones over `git://` on port 9418. The cost is that two
repositories on one host and one path, on two ports, would match each
other. The `webhookSecret` scope makes that a collision inside one
team's own volumes, and no deployment on record has that shape. An
attribute that states the key by hand is the answer if one ever does,
and it is not built until then.

### Verification

Each forge signs its request differently. Every row below was read on
2026-09-06.

| Forge | Header | Value | Source |
|---|---|---|---|
| GitHub | `X-Hub-Signature-256` | `sha256=` and the hex HMAC-SHA256 of the raw body | [Validating webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries) |
| GitLab | `X-Gitlab-Token` | The configured secret, as plain text | [Webhooks](https://docs.gitlab.com/user/project/integrations/webhooks/) |
| Gitea | `X-Gitea-Signature` | The hex HMAC-SHA256 of the raw body, with no prefix | [Webhooks](https://docs.gitea.com/usage/webhooks) |
| Forgejo | `X-Forgejo-Signature` | The hex HMAC-SHA256 of the raw body, with no prefix | [Webhooks](https://forgejo.org/docs/latest/user/webhooks/) |

GitLab also offers a signing token whose signature covers
`{webhook-id}.{webhook-timestamp}.{body}` and asks for a replay
window. The first cut verifies `X-Gitlab-Token` alone, and the open
questions carry the rest.

The controller matches the headers above to choose the verification,
and compares every HMAC in constant time.

| Answer | When |
|---|---|
| `401` | The path names no `Secret`, no header the controller verifies is present, or the verification failed. The body is empty. |
| `400` | The body is not the JSON of a push, or it is over the size limit. |
| `202` | The request verified. The body names how many volumes the controller marked, so a person configuring a webhook reads `0` when the URL matches nothing. |
| `500` | The request verified, and the controller could not list the volumes or read the cluster. The body is empty, so a forge's retry reaches a controller that can. |

The listener bounds the body it reads and the time it holds a
connection, so an unverified caller cannot make the controller buffer
or wait. The webhook names no URL of its own, and it moves only
volumes that already exist. It is never a way to make the driver fetch
a repository nobody deployed.

### A restart demands

A webhook that arrives while the controller restarts is lost. The
`Deployment` is one replica, and with `pull: on-demand` no timer covers
the loss. So the controller's start is a demand: it writes a fresh
`git.liken.sh/pull-requested-at` on every read-only `PersistentVolume`
of this driver. The node plugins pull once through the channel, and
the push that was lost reaches the pods. This is the same rule plan 10
states for the node plugin's restart.

### `controller` and `node` are subcommands

The one binary picks its plugin with `--controller` today. With the
listener, the controller is a process with its own flags, and a
boolean that flips the meaning of every other flag reads badly. The
command line becomes two subcommands:

```console
git-csi-driver node --endpoint=... --node-id=... --store=... --metrics=... --demand-min-interval=...
git-csi-driver controller --endpoint=... --metrics=... --webhook=...
```

Each subcommand accepts only its own flags. `--version` stays a flag
of the command itself. `deploy/node.yaml` and `deploy/controller.yaml`
pass the subcommand, and the install guide shows both.

### What the driver reports

**The controller's log** writes one line per request: the `Secret`,
the forge the headers named, the ref, and the count of volumes marked.
A refused request writes one line with the reason and never the
secret.

**The controller's metrics** already have a listener. `newServer`
builds the registry and opens `--metrics` for both plugins, and the
controller's registry is empty today. This plan puts two counters on
it:

| Metric | Meaning |
|---|---|
| `git_csi_webhook_requests_total`, labeled `result` | Requests by what they answered: `accepted`, `unauthenticated`, `malformed`, `failed`. |
| `git_csi_webhook_marked_total` | `PersistentVolume`s the controller annotated. |

`deploy/controller.yaml` declares no container port for the metrics
listener, and this plan adds one, beside the webhook port.

**The volume's report** already carries the last demand and the last
pull, from plan 10. A person who reads "requested at" moving and
"pulled at" not moving knows the channel works and the pull fails,
and the failure is on the same line.

### What stays out

**Writeable volumes.** A webhook that matches a writeable volume marks
nothing, and the count in the `202` does not include it. Plan 10 says
why a demand never reaches a writeable tree.

**Inline volumes.** An inline volume has no `PersistentVolume`, and
plan 10 refuses `on-demand` on it. A webhook never reaches one.

## Considered and set aside

- **One `Secret` in the driver's namespace, with a key per forge.** It
  needs no `secrets` RBAC and no `claimRef` lookup. It loses because
  the cluster owner then adds every entry, and a team that owns a
  namespace cannot set up its own webhook.
- **A listener flag that defaults to off.** A listener with no
  `Secret` to verify against refuses everything already, and the
  `Ingress` is the proof that the port is closed to the outside.
- **A path that names the volume, such as `/push/<PersistentVolume>`.**
  It removes the URL rule. It loses because one repository backs many
  volumes, and a forge sends one webhook per repository.
- **Flux's `Receiver`.** It is this design for Flux's own kinds, with
  `reconcile.fluxcd.io/requestedAt` as the annotation. Its documentation
  says `spec.resources` names Flux custom resources alone, so it
  cannot mark a `PersistentVolume`.

## Open questions

- **GitLab's signing token.** Its signature covers
  `{webhook-id}.{webhook-timestamp}.{body}` and its timestamp asks for
  a replay window. What would settle it: whether a GitLab deployment
  needs it before the plain `X-Gitlab-Token` is enough.
- **Two replicas.** The restart demand closes the hole for a restart.
  It does not close it for a controller that is down for an hour. The
  question is whether that ever matters more than the second pod
  costs.

## The drill

Drill 12, `lab/drills/12.sh`. The lab's forge is `git daemon`, which
sends no webhook, so the drill posts the request itself. The payload
and its HMAC-SHA256 are computed on the host with `openssl`. A
`debian:12-slim` pod then posts them to the controller's `Service`
inside the cluster with `curl`. The lab runs no ingress controller, so
the `Service` is the endpoint the drill reaches, and the guide covers
the `Ingress`.

1. Apply a webhook `Secret` in the drill's namespace, and a
   `ReadOnlyMany` `PersistentVolume` and claim on the forge's
   repository with `pull: on-demand` and `webhookSecret`, and a reader
   pod. The pod reads the greeting.
2. Push a new greeting to the forge. Post a signed GitHub-shaped push
   payload naming the repository and `refs/heads/main` to
   `/webhook/<namespace>/<name>`. The answer is `202` and names one
   volume. The pod reads the new greeting within 30 seconds.
3. Push again, then post the same payload with a wrong signature. The
   answer is `401`, and the tree does not move.
4. Post the same payload to a path that names no `Secret`. The answer
   is `401`.
5. Post a signed payload naming a repository no volume follows. The
   answer is `202` and names zero volumes, and the tree still does not
   move.
6. Push again, then post twenty signed payloads at once. The node's
   log shows one pull, or two, inside `--demand-min-interval`.
7. Push again, then delete the controller pod. The new controller
   marks every volume on start, and the pod reads the third greeting
   without a webhook.
8. Post a Gitea-shaped payload with `X-Gitea-Signature`. The answer is
   `202`.

Drills 03, 07, and 10 still pass.

## What is done when

- `git-csi-driver` takes `node` and `controller` subcommands, each with
  its own flags, and the manifests pass them.
- The controller serves a webhook listener on `--webhook`, and the
  base manifests declare the port and the `Service`.
- A verified push annotates every `PersistentVolume` of this driver
  whose `webhookSecret`, claim namespace, `url`, and `ref` match, and
  no writeable volume.
- An unverified, an unparsed, and an unmatched request each answer as
  the table above says, and the answer body never carries a secret.
- The controller's start marks every read-only `PersistentVolume`.
- The unit tests prove each verification and each answer, the
  comparison key against the URL forms in this plan, the ref rule, the
  `Secret` scope, the restart mark, and the subcommands. Coverage
  stays at 100%.
- Drill 12 passes in the lab, and drills 03, 07, and 10 still pass.
- The read-only guide has a webhooks section with the `Secret`, the
  attribute, the `Service`, the `Ingress`, and each forge's settings.
  The install guide lists the subcommands, the new flags, and the
  ports. The reference table has the `webhookSecret` row.
- Two comments this plan makes wrong are fixed with it. The
  `ClusterRole` in `deploy/controller.yaml` says every rule is the
  sidecar's, and the driver container now reads `Secret`s and patches
  `PersistentVolume`s. `controller.go` says the controller holds no
  state, and it now holds a client and a cache.
