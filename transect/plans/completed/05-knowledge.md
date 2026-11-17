# 05: Slice 3, knowledge

Slices 1 and 2 drew what the Kubernetes API and the signals give.  This
plan adds the other two layers of "What transect knows": kind
knowledge, written by the operator that owns a custom kind, and
intent, written by the people and agents who run the cluster.  It also
adds log sources and log patterns.  Plans 02, 03, and 04 left a slot
for each piece of stored text they could not read yet, and this plan
fills those slots.  Like the earlier plans, it is built in stages, and
each stage ends in something to reload and look at against a real
cluster.

The comp for this plan is board 9 (a custom kind through kind
knowledge).  Its kind knowledge YAML is a sketch, and this plan
replaces it with the shape below.

## What the earlier plans left for this one

| Gap | Where it was left | Stage |
|---|---|---|
| a card's opening line of what the thing is, from `why` | plan 02, "Intent text" | 3 |
| "loses office file conversion" in the cascade, from `provides` | plan 04, "Where `provides` slots in" | 4 |
| the cut's second lines, such as "tells it to rescan" | plan 04, "The cut's second lines are evidence, not intent" | 4 |
| an address configured inside a Secret or a ConfigMap | the design, "Intent in annotations" | 5, 13 |
| sign-in that an application does itself | plan 02, "Authenticates through" | 6 |
| forward auth on an ingress controller's middleware | plan 02, "Authenticates through" | 14 |
| a custom kind's health from its own condition | plan 02, "Not in this plan" | 9 |
| references in a custom kind's spec and status, and guessed ones | the design, "Four primitives" | 10, 11 |
| a custom kind drawn through its knowledge (board 9) | plans 02 and 04, "Not in this plan" | 12 |
| a controller whose status writer is `Go-http-client` | `ownership/controller/managers.rs` | 7 |
| a database whose budget lets no pod go because its operator switches primaries | the guards' "none may go" warning | 7 |
| a CSI driver that keeps its data on the machines' own disks | `storage/source.rs`, which draws every CSI driver in the ground | 15 |
| one storage appliance drawn as two ground boxes, its CSI driver and its NFS address | `storage/mod.rs`, "only intent text could" | 15 |
| a registry mirror whose public-looking host is not the registry transect must read | the image census's unreadable registries | 16 |
| log stores, and patterns that tag lines | plan 02, "The API's log tail" | 17, 18 |

## Decisions

- **Resources load from `--resources` in this plan, and CRDs wait.**
  The design keeps the resources' shapes in local YAML "until they
  settle," and this plan is where they change most.  A CRD would need
  a settled group, an install step in the development loop, and
  transect running in the cluster to write status, which raises the
  design's open question on how the page authenticates.  So no CRD is
  in this plan.  What a CRD's status would carry is built anyway: each
  loaded resource gets a list of conditions in the shape of
  `status.conditions`, shown on a page at `/resources`.  The plan that
  adds the CRDs then only watches the resources and writes those
  conditions, which are the only writes transect makes.

- **One loader for every kind of resource.**  `layers/load.rs` reads
  one kind from a directory today.  A new `resources/` module reads
  every document under `--resources` once and sorts it by `kind` into
  `Layers`, `KindKnowledge`, `Intent`, `Outside`, `LogSource`, and
  `LogPattern`.  A document of an unknown kind, or of a known kind that
  does not validate, no longer stops transect: it gets a condition that
  says why and is left out.  `layers/` keeps selector matching and
  placing.

- **Resources reload when their files change.**  The development loop
  is "change the YAML, reload the page," so the loader rereads the
  directory when a file under it changes and marks the cluster
  changed, the way a watch event does.  It polls modification times
  every two seconds, which needs no new dependency and works on any
  filesystem.

- **The group and the prefix are the same name.**  Every resource
  carries `transect.liken.sh/v1alpha1`, and every annotation uses
  `transect.liken.sh/` as its prefix, as `layers/` already does for
  `transect.liken.sh/layer`.  One constant holds the name.  The prefix
  is fixed, not configurable: a manifest annotated for transect should
  read the same in any cluster that runs it.

- **Every fact says which layer it came from.**  `Edge` gains a
  `provenance` beside `guessed`, and each card line that comes from
  stored text carries one too:

  | Provenance | Says | Shown as |
  |---|---|---|
  | Kubernetes | nothing more: it is the default | no mark |
  | Signals | the source: Prometheus, Alertmanager, a log source | the source's name, as the cards already do |
  | Kinds | the kind knowledge's name and the field path it read | "from `status.receiver.name`", as board 9 draws it |
  | Intent | the annotation and its object, or the Intent resource | "from why on Deployment web", in the line's title, with a small mark |

  A guess stays its own flag, since a guess can come from the API (a
  controller among several candidates) or from a custom kind's field
  names.  A legend line on each card with any marked line says what
  the marks mean.  One askama macro draws the mark, so every card shows
  it the same way.

- **Stored text is untrusted.**  An annotation, kind knowledge, an
  Intent resource, a condition's message, and a log pattern's meaning
  are each cut to a length a card can hold and stripped of control
  characters, and a `docs` value links only when it is plain `http` or
  `https`.  Any other `docs` value shows as text.  These are the rules
  plan 03 set for image labels.

- **Intent: annotations first, and an Intent resource with the same
  fields.**  The annotations are the design's: `why`, `gotcha`,
  `provides`, `depends-on`, `docs`, and `layer`, plus two this plan
  adds below.  transect reads them from the metadata of every object it
  already watches, and from ConfigMaps through a new metadata-only
  watch.  The Intent resource is namespaced and selects objects in its
  namespace:

  ```yaml
  apiVersion: transect.liken.sh/v1alpha1
  kind: Intent
  metadata:
    namespace: a-namespace
    name: the-database
  spec:
    selects:
      - {resource: statefulsets, name: db}
      - {resource: deployments, matchLabels: {app.kubernetes.io/part-of: a-store}}
    why: Keeps orders and accounts.
    gotcha: A failover drops connections for about ten seconds.
    provides: order storage
    dependsOn: [backups.example.com:443]
    docs: https://docs.example.com/db
  ```

  An annotation on the object wins over an Intent resource for the same
  field.  Two Intent resources that set one field on one object both
  get a condition, and the first by name is shown.  The resource lets
  intent be tried against a real cluster from `local/` before the
  prefix settles, with no edit to the cluster's manifests.

- **Overlays stand in for annotations a cluster does not carry yet.**
  `--resources` also loads plain Kubernetes objects of any kind
  transect watches, such as an `apps/v1` Deployment, that carry only
  `metadata`: a namespace, a name, and annotations or labels.  transect
  merges each overlay's annotations and labels onto the live object
  with that kind, namespace, and name, as if the cluster's manifest
  carried them, and marks every fact read from one with the overlay's
  file.  An annotation on the live object wins over an overlay's.  An
  overlay that matches no live object gets a condition on the
  `/resources` page.  An overlay file is the patch a person would make
  to the real manifest, so trying intent in a cluster is one file per
  object in `local/`, and adopting it is moving those annotations into
  the cluster's repository.  The same holds for kind knowledge, Layers,
  and transect's own CRDs: they live as files, and nothing is applied
  to a cluster until a person does it.

- **Whose intent a card shows.**  A card shows the intent on its own
  object: the namespace card the Namespace's, a workload card its top
  owner's, a pod page its workload's.  A workload whose top owner has no
  `why` falls back to the `org.opencontainers.image.description` of its
  first image, marked with the image's provenance, as plan 03 planned.

- **`depends-on` entries.**  The value is a list split on commas and
  newlines.  An entry with a scheme is a URL; an entry that reads
  `<resource>/<name>` or `<namespace>/<resource>/<name>`, with a
  `<resource>` the cluster serves, is an object; any other entry is a
  host or `host:port`.  Addresses resolve through `addresses/`, so an
  entry that names nothing in the cluster becomes a box outside it.  An
  entry that is none of these shows on the card as text, "names
  <entry>, which transect cannot read."  The edge is the `depends on`
  kind that `addresses/` already draws, with Intent provenance, so a
  card lists both together.

- **`depends-on` on a Secret or a ConfigMap reaches the workloads that
  read it.**  The pod elevation already knows which Secrets and
  ConfigMaps each pod reads, by name, from its spec.  A `depends-on` on
  one of them becomes an edge from each workload that reads it.

- **A Secret's annotations come from what produces it, and from the
  Secret only when the operator of transect turns it on.**  A metadata
  watch on Secrets still needs `list` on Secrets, which grants their
  data.  So kind knowledge can name the field of a custom kind that
  names the Secret it writes (`writesSecret` below), and the producer's
  intent then applies to that Secret.  `--secret-annotations` turns on
  a metadata-only watch of Secrets for an identity that already has
  that access, such as a workstation's admin kubeconfig.  It is off by
  default, and the page says so where a Secret's dependency is missing.

- **Two annotations beyond the design's table.**

  | Annotation | On | Says |
  |---|---|---|
  | `<prefix>/authenticates-through` | any object, most often a Secret or ConfigMap | addresses or objects it signs in through, read like `depends-on` |
  | `<prefix>/data-on` | a CSIDriver | `machines`: its volumes keep their data on the disks of the machines that mount them |

  Sign-in that an application does itself is configured in a Secret,
  so it reaches the application the way `depends-on` does, but as an
  `authenticates through` edge, the design's kind for it.  A separate
  annotation keeps each value a plain list.  `data-on` is written by
  whoever ships the CSI driver, in the same manifest as its CSIDriver
  object, or added to it by a patch in the cluster's own manifests.

- **The `Outside` resource names a thing outside the cluster.**  A
  ground box today is an address or a CSI driver, neither of which is
  an object that can carry an annotation.  `Outside` is cluster-scoped,
  names one thing, lists the addresses and CSI drivers that reach it,
  and carries the intent fields:

  ```yaml
  apiVersion: transect.liken.sh/v1alpha1
  kind: Outside
  metadata:
    name: a-nas
  spec:
    title: The NAS
    addresses: [nas.example.internal]
    drivers: [csi.nas-vendor.example]
    why: Holds every volume that must survive a machine.
    gotcha: Its updates restart the iSCSI service.
  ```

  Every backend it lists draws as one ground box with its volumes
  counted by type across all of them, and a `depends on` edge to any of
  its addresses ends at the same box.  An identity provider or a cloud
  API outside the cluster gets a box the same way.

- **Kind knowledge's shape.**  It starts from the draft for Players and
  keeps its fields, with three changes: each reference's `edge` is one
  of transect's edge kinds, a new `label` carries the verb a card
  shows, and an address reference reads `address: true`.  The name is
  the CRD's name, `<plural>.<group>`:

  ```yaml
  apiVersion: transect.liken.sh/v1alpha1
  kind: KindKnowledge
  metadata:
    name: caches.cache.example.io
  spec:
    summary: A cache that keeps sessions in memory.
    layer: platform
    healthy: {condition: Ready}
    references:
      - {path: status.primary.node, kind: Node, edge: runs on}
      - {path: "spec.replicas[].claim", kind: PersistentVolumeClaim, edge: stores on}
      - {path: spec.upstream, address: true, edge: depends on, label: fills from}
      - {path: status.display, label: shows on}
    writesSecret: {path: spec.credentialsSecret}
    controller: {image: example.io/cache-operator}
    disruptions: operator
    docs: https://cache.example.io/docs/
  ```

  | Field | Means |
  |---|---|
  | `summary` | the card's opening line for any object of the kind with no `why` of its own |
  | `layer` | the layer for namespaces whose objects are this kind, when it names a layer the cluster's `Layers` declares |
  | `healthy.condition` | the condition whose `True` means the object works |
  | `references[]` | a field path, with `[]` for each item of a list, and what it names: an object by `kind` and `group`, or an address |
  | `references[].edge` | one of `runs on`, `stores on`, `holds`, `depends on`, `managed by`, or `authenticates through`; `depends on` when left out |
  | `references[].label` | the verb a card and board 9 show, such as "plays through"; the edge kind's name when left out |
  | `writesSecret.path` | the field that names the Secret an object of the kind writes, in its own namespace |
  | `controller.image` | the image repository its controller runs, without a registry host |
  | `disruptions: operator` | its operator moves work off a pod before eviction, so a budget that lets none go is expected |
  | `docs` | where to read more |

  A reference with neither `kind` nor `address` names something
  transect has no object for, such as a monitor, and shows on the card
  as the field's value with its label, with no edge.  The edge
  vocabulary is fixed because loops, the cascade, and the edge row read
  edge kinds; a verb an operator invents is display text.  So the
  Players draft's `calls` becomes `depends on` with the label "talks
  over": `calls` is the edge the flow agent observes, not one a
  configuration declares.

- **The layer order.**  A namespace's layer comes from its `layer`
  annotation, then the `Layers` selectors, then kind knowledge, then
  the unsorted band.  The cluster's own `Layers` wins over an
  operator's default because an operator cannot know a cluster's
  layers.  A `layer` in kind knowledge that names no declared layer is
  ignored, with a condition on the kind knowledge.  When a namespace
  holds kinds whose knowledge names different layers, the highest of
  them wins and the namespace card names both.

- **Custom kinds are watched in full when a card needs their
  fields.**  `cluster/custom.rs` keeps metadata-only watches on the
  kinds that own pods.  A kind that has kind knowledge, or that is a top
  owner, gets a full watch instead, since its health, references, and
  `writesSecret` are in its spec and status.  A kind named by kind
  knowledge gets a watch even when it owns no pods, so a kind such as
  a remote control, which runs nothing, still shows in its namespace.
  The same discovery, list-of-one probe, and background retry apply.

- **A custom kind's health.**  With kind knowledge, its named
  condition decides: `True` is healthy, `False` is the failure fill,
  and `Unknown` or missing is the warning outline.  The condition's
  `reason` and `message` are the card's reason line.  Without kind
  knowledge, a `Ready` condition decides the same way.  With neither,
  health stays the roll-up of the built-in workloads underneath, as it
  is now.

- **Guessed references.**  For a kind with no kind knowledge, transect
  walks its spec and status for fields named `*Ref` or `*Name`.  A
  `*Ref` that holds `kind` and `name` names that object.  A `*Name`
  string names an object of the kind its prefix names, such as
  `serviceName`, `secretName`, `claimName`, and `nodeName`, or, for any
  other prefix, the one object of that name in its namespace among the
  kinds transect watches.  Each match is a `depends on` edge marked as
  a guess, with the field path as its provenance.  A field that matches
  no object, or several, draws nothing.  Address resolution runs on the
  same kinds' string fields, also as guesses.  Kind knowledge turns
  the guesses off for its kind, since its references are the operator's
  own.

- **A controller by its image.**  When RBAC and the status writer leave
  several candidates, as they do for a status written as
  `Go-http-client`, `controller.image` keeps the candidates that run
  that image repository, matched without the registry host so a mirror
  does not hide it.  One left is a controller that is not a guess.  The
  operator knows its image when it writes its knowledge, and the image
  survives a Helm chart renaming the ServiceAccount.

- **A budget that lets none go, by design.**  For a workload whose top
  owner's kind has `disruptions: operator`, the guard reads "PDB: none
  may go; its operator moves the primary first" and is held, not a
  warning.  A database operator that switches primaries before a drain
  sets a budget like this on purpose.

- **A registry mirror is a flag.**  A machine's container runtime can
  pull a public-looking host from a mirror, and that host may not
  answer from where transect runs.  The mirror is set on the machines,
  not in the API, and the right address differs between a workstation
  and the cluster, as `--registry-auth` does.  So `--registry-mirror
  <host>=<url>`, repeated, sends transect's reads for `<host>` to
  `<url>`.  A digest names the same content on both, so the cache is
  unchanged.

- **Log sources.**  The draft's shape stays, with three changes.  The
  query placeholders separate field names from values: `{fields.pod}`
  for the name of the pod field, and `{pod}` for the pod's name.  `url`
  wins over `service` when both are given, so one resource serves a
  workstation through a port-forward; in the cluster, `service`
  resolves by its DNS name.  Credentials wait for the CRD plan: from a
  workstation a port-forward needs none, and in a cluster transect
  will read only the one Secret a `LogSource` names.

  ```yaml
  apiVersion: transect.liken.sh/v1alpha1
  kind: LogSource
  metadata:
    name: logs
  spec:
    service: {namespace: a-log-store, name: logs, port: 9428}
    url: http://localhost:9428
    language: logsql
    query: /select/logsql/query
    fields:
      namespace: k8s.namespace.name
      pod: k8s.pod.name
      container: k8s.container.name
      node: k8s.node.name
      time: _time
      level: severity_text
      message: _msg
    queries:
      tail: '{fields.namespace}:"{namespace}" {fields.pod}:"{pod}" | sort by ({fields.time}) desc | limit {limit}'
      search: '_time:{window} {pattern} | stats by ({fields.namespace}) count() matches, max({fields.time}) last'
  ```

  `language` picks how a value is quoted into a query and how the
  answer is read.  This plan builds LogsQL alone, since it is the only
  store this plan can be checked against.  A cluster declares at most
  one log source; a second gets a condition.

- **Log patterns.**  The draft's shape stays: `match`, a query
  fragment in the source's language; `severity`, one of `error`,
  `warning`, or `worth-knowing`; `meaning`, the stored text; and a new
  `window`, `1h` when left out.  `error` and `warning` take the health
  colors; `worth-knowing` takes none.  A pattern tags a pod's lines by
  running the tail query with the pattern added, and marking the tail
  lines that come back.  A pattern's search runs every five minutes in
  a background task, with a 15-second timeout, and its matches by
  namespace are a finding.

- **New modules.**

  | Module | Holds |
  |---|---|
  | `resources/` | the loader, the reload, the conditions, and the `/resources` page |
  | `intent/` | the annotations and Intent resources read into one model, by object, and the provenance macro |
  | `knowledge/` | kind knowledge: parsing, field paths, references, guesses, health |
  | `outside/` | `Outside` resources, which `storage/` and `addresses/` ask for the box a backend or an address belongs to |
  | `logs/` | gains `source.rs`, `query.rs`, and `patterns.rs` beside the API tail |

## Decisions for you

1. **The group and the annotation prefix.**  Settled:
   `transect.liken.sh`, next to `liken`'s operators' groups.
2. **Marks on Kubernetes facts.**  The design says each fact says which
   layer it came from.  Recommendation: mark only kind knowledge,
   intent, and guesses, and let an unmarked line mean the API, with a
   legend saying so.  A mark on every line would repeat "from the API"
   down every card.
3. **Two new annotations and one new resource.**  `authenticates-through`,
   `data-on`, and `Outside` add to the design's tables.  The
   alternatives are an edge-kind prefix inside `depends-on` values
   ("authenticates through: <url>"), which makes every value a small
   grammar, and storage-only fields on `Intent`, which is namespaced
   while a NAS belongs to none.  Recommendation: add all three, and
   record them in the design in stage 19.
4. **A fixed edge vocabulary in kind knowledge.**  Recommendation:
   fixed kinds plus a free `label`, as above.  Free edge names would
   draw as board 9 does but would leave loops and the cascade unable to
   read them.
5. **Reading Secret annotations.**  Recommendation: from producers
   through `writesSecret`, and from Secrets only behind
   `--secret-annotations`, off by default.  The cost is that a
   cluster with no secrets operator gets in-application sign-in only
   from a workstation with the flag, or from an Intent resource or
   annotation on the workload instead of the Secret.
6. **CRDs.**  Recommendation: not in this plan, as above.  The plan
   after this one installs them, runs transect in the cluster, writes
   the conditions, and answers the authentication question.

## Stages

### 1. Resources

The `resources/` loader reads every document under `--resources`,
sorts it by kind, and keeps a list of conditions for each resource:
`Loaded`, or why it was left out, by file and document.  `Layers`
moves onto it with no change to the map.  `/resources` lists each
resource by kind and name with its file and conditions.  The loader
rereads the directory when a file changes.  Unit tests cover an
unknown kind, a document that does not validate, two `Layers`, and a
file that is not YAML.  Look: break a document in `local/resources/`,
reload `/resources` and read the condition, fix it, and watch the
bands come back without a restart.

### 2. Provenance

`Edge` gains `provenance`, set to Kubernetes or Signals by every
existing source, and the provenance macro and card legend exist with
nothing yet to mark.  Image descriptions, which plan 03 already shows,
take the macro.  Look: every page reads as before, and a card with an
image description shows it marked as the image's.

### 3. Why, gotcha, and docs

`intent/` reads the `why`, `gotcha`, and `docs` annotations from every
watched object, and from Intent resources.  Each card opens with its
object's `why`, or its kind knowledge's `summary` once stage 8 lands,
or its first image's description, or nothing.  `gotcha` is a line
under it, and `docs` is a link in the card's links.  Each line carries
its mark.  Look: write an Intent resource in `local/` for a workload
with a `why` and a `gotcha`, and its card, its namespace card's chip
title, and its cut's card all open with them; delete the resource,
and the cards read as before.

### 4. Provides

`provides` fills `Loss` as plan 04 planned: the cascade reads "loses
order storage" in place of "loses <workload>," the dependent's parcel
reads "no order storage," and the card's "If it goes down" uses the
same words.  On the cut, a side box's second line reads the box's
`provides` or `why` in place of its kind and namespace.  Look: give
a database `provides` in an Intent resource, open the loss of the
machine its only instance runs on, and the application that calls it
reads "loses order storage."

### 5. Depends on, from intent

`depends-on` entries parse into URLs, objects, and hosts, resolve
through `addresses/`, and become `depends on` edges with Intent
provenance.  A ConfigMap's `depends-on` reaches each workload that
reads it, through a new metadata-only ConfigMap watch.  An entry that
names nothing readable shows as text on the card.  Unit tests cover
each entry form, a `<resource>` the cluster does not serve, and a
ConfigMap read by two workloads.  Look: declare a dependency on an
outside API that only a ConfigMap names, and the workload's card, its
cut, and the ground band show a box for that API.

### 6. Authenticates through, from intent

`authenticates-through` reads like `depends-on` and draws the design's
`authenticates through` edge.  The edge row gains the kind, the cut
draws it as a solid side line, loops count it, and the cascade reads
"<application> loses sign-in" when the identity provider is down.
Look: declare an application's in-application sign-in in an Intent
resource, and its card names the identity provider; lose the machine
the identity provider runs on, and the application is in the cascade.

### 7. Kind knowledge: loading, controller, and disruptions

`knowledge/` parses `KindKnowledge` from the loader, with conditions
for a name that is not `<plural>.<group>`, a kind the cluster does not
serve, and an unknown edge kind.  This stage reads the two fields that
need no object's fields: `controller.image`, which narrows controller
attribution, and `disruptions`, which holds the guard.  Look: write
kind knowledge for an operator whose status writer is
`Go-http-client`, and its namespace's "managed by" loses "(a guess)";
write it for a database operator, and its primary's "none may go"
guard turns from a warning to held.

### 8. Kind knowledge: full watches, summary, and layer

Kinds with kind knowledge, and custom top owners, get full watches.
A kind named by knowledge that owns no pods shows in its namespace as
one count chip per kind, such as "Remotes ×3."  `summary` opens each
of its objects' cards, and `layer` places namespaces in the order
above.  The card ends with "Known through <controller>," naming the
kind knowledge.  Look: load the Players draft, and the namespace that runs Players
lands in its layer with no selector naming it; its card's Players
open with the summary.

### 9. A custom kind's health

The named condition, or `Ready`, colors a custom kind's box and its
card's reason line, as the decision says.  Unit tests cover `True`,
`False` with a message, `Unknown`, a missing condition, and a kind
with neither knowledge nor `Ready`.  Look: unplug the screen a Player
draws on, and its box takes the failure fill with the operator's
message; plug it in, and it goes gray.

### 10. Kind knowledge's references

A field path reader (`spec.a.b`, `status.list[].name`) and the
references it reads: an object by kind and group, an address through
`addresses/`, or a value with no object.  Each becomes an edge of its
named kind with its label and Kinds provenance, so the workload card,
the cut, loops, and the cascade read them like any other edge.
`writesSecret` links a producer to its Secret, and stage 5's Secret
path starts reading the producer's intent.  Unit tests cover each path
form, a list, a missing field, and an address to another namespace.
Look: a Player's card lists its receiver, its remotes, and the message
bus it talks over, each with the field it came from.

### 11. Guessed references

The `*Ref` and `*Name` walk and address resolution over custom kinds
with no knowledge, as the decision says, each edge a guess.  Table
tests cover a `secretRef`, a `serviceName`, a `fooName` matching one
object, the same matching two, and a kind with knowledge, which gets
none.  Look: a custom kind with no knowledge shows dotted lines on its
cut to the Services and claims its spec names; each line's title names
the field.

### 12. Board 9: a custom kind's page

A custom kind with kind knowledge opens to board 9: its object in the
middle with its condition chips, and one box per relation around it,
each with its label, its far end, and "from <path>."  The layout is
fixed, as the cut's is, and reuses `map/cut/lines.rs` for its lines.
A relation that is a value with no object draws as a box with no link.
The namespace column on the left lists the kind's other objects and
the count chips from stage 8.  Look: open a Player, and every relation
on board 9 is there, with the field it came from.

### 13. Secrets

Producers' intent applies to the Secrets they write, and
`--secret-annotations` turns on the metadata-only Secret watch.  The
workload card names which way each Secret-borne dependency was read.
Without either, a Secret the pod reads says "transect does not read
this Secret's annotations" in its title on the pod page.  Look: a
database host configured in a Secret, with `depends-on` on the
secrets operator's resource that writes it, draws the application's
edge to the database.

### 14. Forward auth

Kind knowledge gains `referencedBy`: an annotation on a built-in kind
that names objects of this kind, with a template for each name, such as
`{namespace}-{name}@kubernetescrd` for Traefik's Middleware.  transect
renders the template for each object of the kind and matches the
annotation's entries against them, so a name with dashes reads without
guessing where it splits.  A middleware's forward-auth address is a
reference with `edge: authenticates through`.  The Ingress's
applications then authenticate through what the address resolves to.
Look: an application behind forward auth names the identity provider
on its card and its cut, with the field and the annotation it came
from.

### 15. Storage: data on machines, and one box per appliance

A CSIDriver watch, and `data-on: machines` on a driver: a volume whose
PersistentVolume names one machine in its node affinity stores on that
machine, as a local volume does, and strands its pods in a loss; a
volume with no node affinity stores on whichever machine runs its pod
and strands nothing.  `outside/` folds the backends an `Outside` lists
into one ground box, with its title, its intent, and its counts summed.
Look: a per-machine CSI driver's volumes leave the ground band and
show on their machines; a NAS reached by a CSI driver and an NFS
address is one box.

### 16. Registry mirrors

`--registry-mirror <host>=<url>` sends label reads for a host to its
mirror.  The census's unreadable line names the mirror it tried.
Look: a registry that was unreadable from the workstation reads
through the mirror, and its images link to their commits.

### 17. A log source

`logs/source.rs` reads the `LogSource`, and the pod page's last lines
come from it when it is declared, with the API's tail as the fallback
when it does not answer.  A pod that has restarted shows its earlier
container's lines, which the API's tail does not keep.  The lines take
their level from the `level` field in place of the guess `logs/level.rs`
makes.  Unit tests cover placeholder filling, quoting a value with a
quote in it, and reading LogsQL's JSON lines.  Look: open a pod with a
port-forward to the log store and without one, and the lines come from
each source in turn, with the source named.

### 18. Log patterns

Patterns tag a pod's matching lines with their meaning, and each
pattern's search over its window is a finding on "What to know now,"
one line per namespace, such as "a request for a .env file: 14 in
<namespace> in the last hour."  The pod's namespace card shows the
same count.  Look: load a pattern for connection refusals, stop a
database, and the application's pod tags its lines and the sidebar
names the namespace within five minutes.

### 19. Record the design

`plans/00-design.md` records this plan's decisions: the resource
shapes, the new annotations and `Outside`, the provenance marks, the
layer order, and the group, `transect.liken.sh`.  Look: the design
and the code agree on every field name.

## Not in this plan

- CRDs for transect's resources, status conditions written to the
  cluster, running transect in the cluster, a log source's credential
  Secret, and how the page authenticates: the next plan.
- Kind knowledge carried as `x-` extensions in a CRD's schema, which
  the design rejects.
- Findings declared as PromQL, the design's fourth primitive's other
  half, and conditions for a query that does not parse.
- Log stores other than LogsQL, such as LogQL.
- A cascade past one hop, which intent now makes possible but needs a
  field that says whether a dependent goes down or runs on without it.
- Telling a machine's reboot from a crash in the DaemonSet matrix:
  plan 02 left it for kind knowledge, but the boot time is a signal
  (`node_boot_time_seconds`), not knowledge.
- Kind knowledge for third-party operators shipped with transect.  The
  drafts stay in `local/` until the shape settles; whether transect
  carries knowledge for operators that ship none is a question for
  after it does.
- Snapshots, the MCP server, and the `calls` edge: later, and slice 2.
