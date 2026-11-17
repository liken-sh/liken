# 08: Knowledge from outside

The design says transect's code knows Kubernetes and nothing else, and
that everything about a particular project comes from resources
transect loads.  The code still holds facts about particular projects:
Flux's kinds, labels, and revision format; the usage queries and the
alert label conventions of one monitoring stack; an ingress controller
found by its workload's name; cert-manager's issuer annotations; `liken`'s
`Machine` as the machine under a Node; LogsQL's level values; and the
URL layouts of a few forges.  This plan moves each into a resource.

Nothing in the owner's feedback asks for this, and no page changes when
it lands.  It is the work that has to be done before transect is
published for clusters that run other tools, so it is scheduled against
publishing, not against the feedback.  Plan 07 needs two things from
it, the full `MetricsSource` for its stage 4 and a settled kind
knowledge schema for its stage 8; that is a dependency of plan 07, and
it does not set when this plan is built.

## Built

Every stage is built.  The design holds every rule they set; in short:

1. **Text goldens and the side-by-side script** (d303148, d8c3167).
2. **The full MetricsSource** (a48ab8d, 48adad2).
3. **GitOps knowledge, and the reader** (21ef3df, fb6a63e).
4. **GitOps from knowledge, and the code removed** (19c8338, 5dd04b4).
5. **Machines, log levels, and issuers** (74ea5e0, 5343dea, 9dd6c71).
   `levels` gained a `plain` list beside `error` and `warning`, since a
   store's `INFO` overrode the guess from a line's text before the
   move, and it comes with `fields.level`, one without the other not
   validating.  A `referencedBy` template without `{namespace}` names
   an object beside the annotated one, as a namespaced issuer named
   alone is meant, and the issuer is found in the store, so its name
   links to its page.
6. **Ingress controllers** (e245ef1).  IngressClasses are watched whole
   for `spec.controller`, and a guessed controller ends in "?".  The
   proxy the fixture gained showed a namespace's machine columns were
   unlinked names, and they now link.
7. **Forges and storage consoles** (d167ff3, 4bd87b4).  The CSI
   driver's page shows its `console` template.
8. **Check the design**, in the commit that adds this section.  The
   design gained `primary`, `fields.machine`, and the `lines` query,
   which the code read and the design did not name.

Beside the stages, the pod watch drops the fields Job tracking and the
kubelet rewrite and no page reads (6cab6c1).

A fresh-eyes review of the built plan found five things to fix and a
dozen smaller ones, and a fix pass closed them:

- **The alerting path** comes from Kubernetes, not from Prometheus's
  `/api/v1/alertmanagers`, which other metrics stores may not serve:
  the pods behind the `MetricsSource`'s `alerts.service`.  With none
  declared, the loss view says so in a gray line (20a1c5e).
- **`node`** works on a kind of any scope, such as Cluster API's
  namespaced Machine (a7eb42a).
- **Knowledge that matches nothing** gets a `Matched False` condition
  on `/resources`, for kind knowledge, an `IngressController`, a
  `Forge`, a `MetricsSource`, and `DeviceKnowledge`, and the sections
  they fill keep a gray line (9f94cbf).
- **A resource that did not load** is named, with a link to
  `/resources`, where a page would otherwise say that one would show
  the section (15c0d8a).
- **The goldens** render at the fixture's pinned time, so the changed
  list holds its entries and ages need no masking, and they gained
  `/resources`, a log store's levels, and a CSI driver's console
  template (4b63ad5, 9f94cbf, 685e1f2, 3d9208f).
- **Field names** follow one convention, in a table in the design: a
  path alone is a bare string, `url` is always an address, kind
  knowledge's URL templates are `repository`, lists of label names end
  in `Labels`, and `ignoreChanges` names kinds by `kind` and `group`
  (c6a27df).  An applier's mark takes `formats`, a list, as a
  revision's does (af2efd7).
- **TLS issuers** are found through the Certificate that writes an
  Ingress's TLS Secret, with `role: issues-tls` on a reference, as well
  as through annotations (075ef45).
- Smaller fixes: forge URLs with `/@owner` and `/~owner` (2bf8eb1), an
  empty `levels` entry refused (4ae9f2c), "served by" on the Ingress
  board and the card's controller row only for custom kinds (6ebcc20),
  a CSI driver's console URLs linked (3d9208f), two lists sorted
  (d6aa691), and four modules split (ef7b209).
- `examples/` holds the knowledge public projects would ship, which a
  test loads (a9b20ad).

Three review items are left for later: validation messages that name
the field path of an unknown field, a MetricsSource `service.path` and
headers for a store behind a path or a tenant, and Gateway API routes
as an entry beside Ingresses.

## Decisions

### How a move is proved

Each move takes the same steps:

1. Add the resource kind, read it, and write the local file that says
   what the code said, and a fixture copy of it for the tests.
2. Table tests on the new reader assert the output struct the old code
   produces, over the same fixture objects.  The pages are drawn from
   that struct, so this is the proof that matters.
3. The text goldens of the pages the move touches hold, except for
   text the stage means to change.
4. `scripts/side-by-side` runs the old and the new binary against the
   cluster at the same moment, on two ports, and diffs their pages'
   visible text with ages and numbers masked.  A live crawl alone
   cannot prove parity, because ages, rates, usage, alerts, and log
   lines change between two runs.
5. Delete the constants.

Without the local file, the feature is off, and the page says which
resource would turn it on, the way a missing `LogSource` reads today.
When a resource of that kind is there but did not load, the page says
that it did not, and links to `/resources`.

The goldens are narrow on purpose.  Golden HTML of every page fails on
any change to a template's text, so every later change rewrites them,
and a rewritten golden is reviewed by nobody.  So a golden holds the
visible text of the pages one move touches, with tags stripped and a
log line's level named in brackets, from a fixture model drawn at the
fixture's pinned time in `cargo test`, and a command rewrites them.  There is
no debug flag that runs both readers on the live cluster: the table
tests and the side-by-side diff cover what it would.

### What moves

| Today in code | Moves to | Stage |
|---|---|---|
| the Prometheus address, the usage queries, the node label, alert severities, and the labels that name objects | the full `MetricsSource` | 2 |
| Flux's kinds, labels, fields, and revision format | kind knowledge: `applies`, `source`, `directory`, `chart`, `revision`, `url`, `ref` | 3, 4 |
| `liken`'s `Machine` as the machine under a Node | kind knowledge `node: metadata.name` | 5 |
| LogsQL's error-level values | `LogSource.spec.levels` | 5 |
| cert-manager's issuer annotations | `referencedBy` with `role: issues-tls` | 5 |
| the ingress controller found by its workload's name, and kind knowledge's `frontDoor` | an `IngressController` resource, with the entry queries | 6 |
| forge URL layouts and detection | a `Forge` resource | 7 |

The Prometheus HTTP API, the Alertmanager v2 API, and LogsQL stay in
code: they are protocols, addressed and filled in from resources.

### One resource for the metrics and the alerts

`MetricsSource` holds both, in two sections, because both describe one
monitoring stack's conventions: the labels its scrape configuration
puts on series are the labels its alert rules copy onto alerts, with
the same `exported_` prefix.  Plan 06 stage 17 adds the resource with
one query, `volumeUsed`; this plan adds fields to it and changes none
of that shape.  A second `MetricsSource` gets a condition, as a second
`LogSource` does.

### GitOps knowledge is shaped by two tools

Flux drives more than edges: the applied-by map with HelmRelease
precedence, each applier's source and revision, the code links through
`src/flux/forge.rs` and `src/flux/code.rs`, card sentences, the changed
list (`src/changed/read.rs`), image origin (`src/images/origin.rs`), and
addresses (`src/addresses/sources.rs`).  `src/flux/` and its callers are
about 2,000 lines, so parity covers that whole output, not only edges.

The fields were shaped from one tool, so before they were fixed, the
knowledge for a second GitOps tool was written out: Argo CD's
`Application`, which keeps its source inline rather than in a kind of
its own, marks what it applied with a tracking annotation whose value
holds more than the application's name, and records its revision in
`status.sync.revision` as a bare commit.  Flux's Kustomization and a
source read:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: kustomizations.kustomize.toolkit.fluxcd.io
spec:
  applies:
    name: {label: kustomize.toolkit.fluxcd.io/name}
    namespace: {label: kustomize.toolkit.fluxcd.io/namespace}
  source: [spec.sourceRef]
  directory: spec.path
  revision:
    paths: [status.lastAppliedRevision]
    formats: ['{ref}@sha1:{commit}', 'sha1:{commit}', '{ref}@*']
---
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: gitrepositories.source.toolkit.fluxcd.io
spec:
  repository: ['{spec.url}']
  ref: {branch: spec.ref.branch, tag: spec.ref.tag, commit: spec.ref.commit}
  revision:
    paths: [status.artifact.revision]
    at: status.artifact.lastUpdateTime
    formats: ['{ref}@sha1:{commit}', 'sha1:{commit}', '{ref}@*']
```

A HelmRelease adds `precedence: 1` to its `applies`, names two places
its source may be, `source: [spec.chart.spec.sourceRef,
spec.chartRef]`, and reads its chart from `chart:
spec.chart.spec.chart`.  A Bucket's repository is
`['{spec.endpoint}/{spec.bucketName}']`.  Argo CD's knowledge, a sketch
that no code is written for:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: applications.argoproj.io
spec:
  applies:
    name: {annotation: argocd.argoproj.io/tracking-id, formats: ['{namespace}_{name}:*', '{name}:*']}
  directory: spec.source.path
  chart: spec.source.chart
  repository: ['{spec.source.repoURL}']
  ref: {name: spec.source.targetRevision}
  revision:
    paths: [status.sync.revision]
    at: status.operationState.finishedAt
    formats: ['{commit}']
```

An `ApplicationSet` needs no GitOps fields: the Applications it makes
carry owner references to it, so `managed by` already draws it.

The sketch changed five Flux-shaped assumptions:

- An applier is named by a label or an annotation, and `formats`,
  tried in order, find the name, and the namespace when the value holds
  one, in a value that holds more, such as `{namespace}_{name}:*` for
  Argo CD's applications in any namespace before `{name}:*` for those
  in its own.  Without `namespace`, the applier is the one object
  of its kind with that name.
- A tool may keep its source inline, so `repository`, `ref`, and
  `revision` may sit on the applier itself, and `source` is optional.
  The repository is a list of URL templates whose placeholders are
  fields, tried in order, because a Bucket's URL is two fields.
- A ref field may hold a branch, a tag, or a commit, unsaid which, as
  `targetRevision` does: `ref.name`.  A forge whose directory page takes
  any ref links it; one that needs to know which links it only when it
  is a commit.
- `revision.formats` is a list tried in order, because one tool writes
  `main@sha1:<commit>`, `sha1:<commit>`, `v3@sha256:<digest>`, and
  `sha256:<digest>`.  A placeholder takes the fewest characters it can,
  `*` the most, and `{commit}` matches only seven or more hex digits, so
  a chart version never reads as a commit.  A revision no format
  matches is all ref.
- A revision has its own time when the tool records one (`at`); an
  applier without one, as a Kustomization is, is dated by its source's
  fetch of the same revision, and else by when its condition last
  changed.

Two things do not carry over, and neither is a GitOps field.  Argo CD
reports health in `status.health.status`, not in a condition, and
`healthy` reads only conditions, so an Application's code line would
read "not reconciled yet".  An Application with several sources,
`spec.sources[]`, reads its first.  Both wait for a cluster that runs
Argo CD.

Without `healthy`, an object of a GitOps kind is read by its `Ready`
condition, as every kind's dot is, so Flux's knowledge declares none.

### One watch per GitOps kind

Today each Flux kind is watched twice: once by Flux's own store in
`cluster/flux.rs`, and once trimmed as every CRD is.  After stage 4
there is one watch per kind, the trimmed one, driven by knowledge: a
trimmed watch also keeps the fields its kind's GitOps knowledge reads,
copied to the same paths, so a Kustomization holds its source
reference, its directory, its revision, and its `Ready` condition, and
not the inventory of everything it applied or the history it grows on
each reconcile.  A kind whose GitOps knowledge keeps fields is resolved
by discovery, at the version the knowledge's paths are written
against.  Nothing waits for the custom kinds' first lists, so until an
applying kind has listed, the cards say that reconciles from is off
for now, while it lists.

### Every change to kind knowledge in this plan

| Change | Means | Stage |
|---|---|---|
| `reconciles from` edge kind | in the vocabulary, now that knowledge declares it | 3 |
| `applies` | on an applier kind: the label or annotation on an applied object that names the applier, and the one that names its namespace, each with optional `formats`, and a `precedence` | 3 |
| `source`, `directory`, `chart` | on an applier kind: the fields that may hold a reference to its source, tried in order; the field holding the directory it applies; the field holding the chart it installs | 3 |
| `revision` | on any GitOps kind: the fields that may hold the last revision, its time field, and the `formats` that split it into a ref and a commit, tried in order | 3 |
| `repository`, `ref` | on a kind that fetches, a source or an applier with its source inline: templates of the repository URL, and the fields holding its branch, tag, commit, or a ref of any of the three | 3 |
| `node` | on a kind of any scope: the field naming the Node an object describes, such as `metadata.name` | 5 |
| `referencedBy[].role`, `references[].role` | `issues-tls`, so the entry box reads "TLS from" the object an Ingress's annotation names, or the one a certificate names that writes the Ingress's TLS Secret | 5 |
| `ignoreChanges` | the labels and annotations a tool rewrites without changing anything, each for the kinds it names or for every kind | 4 |
| `frontDoor` removed | moves to `IngressController.spec.entry` | 6 |

Stage 3 renamed Flux's `path` to `directory`, beside a `path` that
every field already holds, and added `chart` and `ref.name`, after the
Argo CD sketch.

### The etcd label stays in the code for now

The loss view reads `node-role.kubernetes.io/etcd`, which a
distribution such as k3s sets on the machines that run an etcd member
(`ETCD_LABEL` in `src/machines/mod.rs`).  Kubernetes sets nothing.
Moving it out is right in principle, but the resource it would go in
is not clear from one label: kind knowledge named `nodes` would be a
resource whose schema mostly does not apply to Nodes, with two possible
shippers.  The fallback exists and is honest: without the label, the
loss view counts each control-plane machine as a member and says so,
from `node-role.kubernetes.io/control-plane`, a Kubernetes label.  The
move waits for a second fact about a distribution to shape a resource
for distributions.

### New and renamed modules

| Module | Holds |
|---|---|
| `web/golden/` | the text goldens, drawn from the one fixture in `web/fixture/` that `--fixture` serves, whose `signals.yaml` holds the signals' answers |
| `gitops/` | `src/flux/` renamed, reading from kind knowledge |
| `forge/`, `alerts/`, `usage/`, `entry/` | gain their resources |

## Stages

### 1. Text goldens and the side-by-side script

`golden/`: fixture models as YAML, the stores' objects and the signals'
answers, and `cargo test` rendering the pages the later stages touch
into committed files of their visible text, with tags stripped, and a
command that rewrites them.  The fixtures include an alert firing,
usage on every machine, a loss, a custom kind with knowledge, and all
six Flux kinds: a Kustomization and a HelmRelease that applied the same
object, Git, OCI, Helm, and Bucket sources, a commit pin, and a
`{ref}@sha1:{commit}` revision.  `scripts/side-by-side` runs two
binaries on two ports against the cluster and diffs their pages'
visible text with ages and numbers masked.  Look: change a template's
text, and `cargo test` fails with a diff of the text it changed; run
the script with one binary twice, and the diff is empty.  Files: none.
Design: none.

### 2. The full MetricsSource

`MetricsSource` gains: Prometheus's `url` or `service`, with
`--prometheus` winning; the labels that may name a Node; and the usage
queries, CPU and memory for machines and containers.  Its `alerts`
section names the Alertmanager (`url` or `service`, with
`--alertmanager` winning), the severity values (`failing`, `notice`,
`hidden`), the labels that name objects and machines, the `exported_`
prefix, and the annotations for the summary and the description.  It
follows the move's steps.  Look: the goldens hold; the side-by-side run
shows the same machine bars, pod bars, and alerts; change the `failing`
list, and an alert's box changes color on the next poll.  Files: the
`MetricsSource` gains the kube-prometheus and cAdvisor conventions.
Design: the full `MetricsSource`.

### 3. GitOps knowledge, and the reader

Write kind knowledge for Argo CD's `Application` beside Flux's, and fix
the fields so both read (the decision above holds both).  Then add the
fields, their parser, and a `gitops/` reader, with table tests over the
stage 1 fixture objects asserting the output struct `src/flux/`
produces today, and one test that runs both readers over the fixture
and compares every object, edge, revision, and source.  The old code
still draws the pages.  Look: the goldens hold, and the table tests
pass against the old code's output.  Files: kind knowledge for Flux's
Kustomization, HelmRelease, GitRepository, OCIRepository,
HelmRepository, and Bucket.  Design: the fields and `reconciles from`
in kind knowledge.

### 4. GitOps from knowledge, and the code removed

`gitops/` draws the pages.  Run the side-by-side script against the
binary from before this stage.  Then delete `src/flux/`'s kinds,
labels, and fields, and `cluster/flux.rs` with them: the trimmed watch
every CRD already has keeps what the knowledge names, so each GitOps
kind has one watch.  The off reasons read "no kind knowledge declares
applies for a kind this cluster serves", "transect is still listing"
the applying kinds, or "transect may not list" them.  The loops
comment names the generic case.  A GitOps object's page is drawn
before a known kind's board, since its kind now has knowledge.  The
fixture keeps Flux, so the goldens prove the move; the code and every
other test name a generic tool.

Knowledge also names the metadata keys a watch ignores when it decides
whether an object changed.  A GitOps tool that lists one object in two
appliers' inventories, as Flux does for a Namespace an app's
Kustomization declares and the root Kustomization applies too, rewrites
its ownership labels on that object at each reconcile, and today each
rewrite redraws the map on every open page.  The applier's knowledge
lists the keys:

```yaml
ignoreChanges:
  - {label: kustomize.toolkit.fluxcd.io/name, kinds: [{kind: Namespace}]}
  - {label: kustomize.toolkit.fluxcd.io/namespace, kinds: [{kind: Namespace}]}
```

An entry is a `label` or an `annotation`, and without `kinds` it holds
for every kind.  The comparison in `cluster/change.rs` drops those keys
before it compares, and the store still keeps the object as the API
gives it, so a page drawn for another reason reads the newest value.
The applied-by map may then name either applier until the next real
change, and both applied the object.

Look: the goldens hold, the side-by-side diff is empty, with the Flux
knowledge moved aside the code lines say which knowledge would turn
them on, and a Flux reconcile that only rewrites a Namespace's
ownership labels pushes no page.  Files: `ignoreChanges` on the
Kustomization knowledge.  Design: the "reconciles from" edge's source,
the code links, and `ignoreChanges`.

### 5. Machines, log levels, and issuers

- `liken`'s `Machine` under a Node becomes kind knowledge `node:
  metadata.name`, so any cluster-scoped kind named for its Node fills
  the section.
- LogsQL's error values move to `LogSource.spec.levels`, with today's
  values as the example.
- cert-manager's issuer annotations become `referencedBy` entries on
  the issuer kinds with `role: issues-tls`, and the entry box reads
  "TLS from" from any referrer with that role.

Each follows the move's steps.  Look: the goldens hold; delete the
Machine knowledge, and the machine page drops the section.  Files:
`node` on the Machine knowledge, `levels` on the log source, and kind
knowledge for cert-manager's issuer kinds.  Design: `node`, `levels`,
and `role`.

### 6. Ingress controllers

An `IngressController` resource names an IngressClass's
`spec.controller`, the image its workload runs, and the entry queries
that kind knowledge's `frontDoor` carries until now:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: IngressController
metadata:
  name: an-ingress-controller
spec:
  controller: example.io/ingress-controller
  image: example/ingress-controller
  entry:
    backend: {label: service, name: "{namespace}-{service}-{port}@kubernetes"}
    requests: 'sum by (service) (rate(requests_total[5m]))'
    latency: 'histogram_quantile(0.95, sum by (service, le) (rate(request_duration_seconds_bucket[5m])))'
    errors: 'sum by (service) (rate(requests_total{code=~"5.."}[5m]))'
    window: 5m
```

The workload is the one that runs `image`.  The name heuristic stays as
a guess when no resource matches, marked as one.  `frontDoor` is
deleted from kind knowledge.  There are no traffic thresholds; plan 06
shows the numbers in gray and stops.  Look: the goldens hold, and the
entry box's "served by" carries no guess mark.  Files: an
`IngressController` for the cluster's ingress controller, and the
`frontDoor` section removed from its route kind's knowledge.  Design:
`IngressController`.

### 7. Forges and storage consoles

A `Forge` resource per URL layout: exact `hosts`, `hostPrefixes`,
`images` that mark a forge the cluster serves, and `tree` and `commit`
templates with `{repository}`, `{refKind}`, `{ref}`, `{path}`, and
`{sha}`.  The code keeps URL parsing and the rule that a link is drawn
only for a known forge.

A CSI driver's console is a URL template of the same kind, written by
whoever ships the driver as a `<prefix>/console` annotation on its
CSIDriver, beside `data-on`.  A placeholder names a volume attribute of
the driver's PersistentVolumes or a parameter of its StorageClasses,
such as `https://{dsm}:5001` for a NAS driver whose StorageClasses name
the NAS as `dsm`, so the driver's ground box links to the console with
no URL written by hand.  An `Outside` resource's `url` overrides it.
When the driver's volumes fill the template with more than one URL,
the ground page lists each one and the title links none.  The same
rule as `url` holds: only a plain `http` or `https` URL is a link.

Look: the goldens hold, a code link reaches the right directory on each
forge the cluster reads from, and with the `Outside` `url` moved aside
the NAS's ground box still links to its console.  Files: a `Forge` for
each layout the cluster uses, and the `console` annotation in the CSI
driver's overlay.  Design: `Forge`, and `console` in the intent
annotations.

### 8. Check the design

Read `plans/00-design.md` against the code: every resource, field, and
annotation it names exists with that name, and every one the code
reads is in it.  Grep `src/` for the names of the projects this plan
moved, and each hit is a comment about the protocol, test data, or a
bug.  Look: the design and the code agree on every field name.

## Not in this plan

- The etcd role label, as the decision says.
- Kind knowledge for a built-in kind, which only the etcd move needed.
- Traffic thresholds.
- Attribution by Helm's release annotations, which plan 06 stage 10
  removed from the code; a cluster that wants it would declare the keys
  in a resource, as `applies` declares an applier's labels.
- Gateway API routes as an entry beside Ingresses.  An ingress
  controller such as Envoy Gateway serves HTTPRoutes, not Ingresses, so
  its entry box draws nothing.  Gateway API is a Kubernetes API, so
  reading it in the code is within the rule.
- Health from a field rather than a condition, which Argo CD's
  `status.health.status` needs.
- A reference whose target kind is read from a field, as a
  Certificate's `issuerRef.kind` says whether its issuer is an Issuer
  or a ClusterIssuer; a reference names one kind, so the examples read
  a Certificate's issuer as a ClusterIssuer.
