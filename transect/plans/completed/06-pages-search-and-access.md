# 06: Finish the feedback

Plans 01 to 05 drew the map, the section view, the loss view, and the knowledge
layer.  The owner used them against a real cluster and sent a round of
feedback; the table at the end of this plan lists every item.  This
plan answers that feedback and nothing else, then stops for the
owner's next pass.

Every stage is built.  Three audits of stages 1 to 9 found layout
bugs, search bugs, a per-view watch that cost more than it gave, and
new pages that read as lists where the owner praised boards; stages 10
to 20 repaired those and finished the remaining items.  Two pieces of
work that the first draft of this plan also held have plans of their
own: moving project knowledge out of the code is plan 08, and the
access lens is plan 09.

Each stage ends in something to reload and look at against a real
cluster, names the files in `local/resources/` it adds, and names what
it changes in `plans/00-design.md`.  The `local/` files are untracked
and specific to one cluster, so the plan names them by what they hold.
Where a decision below changes a rule, the design already states the
new rule; the stage that builds it corrects the design to what was
built.

## Built: stages 1 to 9

The design holds every rule these stages set.  In short:

1. **Entry, and one link for every object** (3288a6f).  "Front door"
   became "entry" in page text and `front_door/` became `entry/`; the
   owner accepted the word.  `object_link` in `src/links/` draws every
   object's name, and a crawl test fails on a known object's name
   outside a link.
2. **The sidebar holds the card** (b769ba4).  The sidebar is sticky
   with its own scroll, and a Runs box in the main column holds the
   image lines in labeled cells.  A policy that admits every namespace
   is one card line and marks no parcel; a parcel's relation is gray.
3. **Lens tabs, and logs as a lens** (0e3b861).  Each lens is a tab
   with its own URL: overview, section, "if lost", and logs.  The loss view
   keeps `?lose=a,b`.
4. **Quick search** (bc9f551).  `ctrl+k`, `/`, or any printable key
   opens a dialog over a server-side index rebuilt on each change.
5. **Every CRD, watched trimmed** (fa5e898).  Each CRD that kind
   knowledge does not need whole is watched trimmed to its metadata and
   conditions, and signals a change only when the trimmed object
   differs.  A namespace lists its custom resources by kind.  The
   commit message holds the before and after measurements and what the
   HTTP/2 research found.
6. **A page for every object** (3a494d7).  The generic page, with the
   per-view watch that stage 12 deletes.
7. **Printer columns** (d9a7dac).  The JSONPath subset that
   `additionalPrinterColumns` use, in `knowledge/path.rs`.
8. **Kind pages** (75844d2).  `/kinds` and `/kinds/{plural.group}`.
9. **What an operator manages** (e3fe0fe, b3f3996).  A workload's
   Manages section, and "If it goes down" as one sentence per kind.

Two fix passes followed (319b743..dc9c61e and a554b36..b3d9cb2).

## Built: stages 10 to 20

10. **Repairs** (64fbaea..ff718a3).
11. **Calm reasons** (90aa0a1), after the cut was renamed the section
    (5981dba).
12. **One store, no view watch** (3251943).
13. **HTTP/2 to the API server** (5aefdfe), and a fix pass on 10 to 13
    (c5f09a7..d27c342) that added `--fixture`.
14. **Every object page is a board** (bcc937d..a50fa22).
15. **Machine pages in boxes** (00cc7bf), after quiet change signals
    (be485f7).
16. **Ground boxes link out** (f60bb45).
17. **Volumes** (769ce91).
18. **Traffic on the entry box** (f42b54b).
19. **Peripheral to adapter, and device facts** (ea6a2ea, b1c43ac,
    8dfd5e0).  Pages then drew from the last model built (9ac58f3), and
    a parcel's relation to the selection took the place of its count
    (a6437e7).
20. **Check, crawl, and stop.**  A review of 14 to 19 and one crawl of
    the real cluster, 6,000 pages with one dead link, were followed by
    a fix pass (216a579 onward) that closed what they found: the
    relation line, the ground page's users, the "Belongs to" box, links
    on every claim, device, and ground box, and the nice-to-haves.

## Decisions for stages 10 to 20

### Repairs come first

The audits found pages that show wrong values, a search that cannot
find what the owner would type first, and project knowledge added to
`src/` by stage 9.  Those go before anything new, in stage 10.

### Calm reasons

- **An idle resource never draws orange.**  Kind knowledge gains
  `healthy.calm`: condition reasons that mean the object is at rest,
  such as `Idle` or `NotReserved`.  A `False` on the healthy condition
  with one of those reasons draws gray, with its message as a gray line
  under the title.
- **Only the condition that decides health takes a color.**  On an
  object page, every other condition draws gray.  transect cannot know
  whether `True` is good for a condition such as `AttentionNeeded`, so
  it colors neither value.

### One store, no view watch

- **The trimmed watch keeps what the pages read.**  For each object of
  a trimmed kind, the store keeps its metadata; its conditions, each
  with type, status, reason, message, and `lastTransitionTime`;
  `status.observedGeneration`; and the values of the CRD's printer
  columns for the storage version, computed when the object is trimmed.
  The rest of the object is dropped before the store.  A whole-watched
  kind computes its column values from the whole object with the same
  function.

- **Every page reads the stores.**  The per-view watch is deleted:
  `src/objects/watch/`, `Sources::hold` in `src/web/render.rs`, the
  view's holdings in `src/web/views.rs`, the cap of 16, and the
  "loading" and "not read yet" notes.  An object page of a trimmed kind
  shows its conditions, printer columns, owner, controller, and
  references in.  It draws no guessed references out, since those read
  the spec; the map already draws none for trimmed kinds (3a494d7), so
  the page and the map agree.

- **A built-in kind with no store is read once per page load.**  A
  Lease, a LimitRange, or a ResourceQuota is read with one GET when its
  page loads, through a small bounded cache whose entries live a few
  seconds, so a burst of loads makes one request.  It holds no
  connection and gets no live updates, and the page says so in one gray
  line: "Read when the page loaded; reload for a newer copy."  A 404
  draws the gone page; a 401 or 403 draws one line saying the API
  server refused the read.  Secrets are never read this way, and a
  ConfigMap's page reads only the metadata store.

- **A PersistentVolume or a PersistentVolumeClaim page reads its
  store.**  Both kinds are watched today.

- **One table says how each built-in kind is read.**  Each row names
  the kind and one of: a store with status, a metadata store, a
  redirect to a page of its own, a cached GET, or never.  The object
  lookup is driven by it, in place of the three lists that must agree
  today (`BUILT_IN`, `STORED`, and `found`).

- **A kind never drops out while it is re-listed.**  When a kind
  changes from trimmed to whole, the trimmed store serves until the
  whole one holds its first list.  A kind that has been whole stays
  whole until transect restarts, since a kind whose objects owned pods
  once will likely own them again.

### HTTP/2 to the API server

kube-client 5.0.0 builds its connector with `.enable_http1()` alone
(`config_ext.rs:468`), so each watch is its own HTTP/1.1 connection,
about one per watched kind, and every CRD is watched.  In a cluster
those connections go through one Service address per replica.  Stage 13 builds the client
the way kube's custom-client example does, so HTTP/2 carries the
watches on a few connections.  If HTTP/2 cannot be made sound, the
stage records why in the design and keeps HTTP/1.1.

### Every object page is a board

- **The rule.**  An object page is composed boxes across the page's
  full width, as the Player's board and the section view are, and never an
  unboxed list of links.  The object sits in the middle with its
  conditions and printer columns; what it is part of, what it names,
  and what names it are boxes around it.  Knowledge adds boxes and
  labels; a kind with none gets the same board from its edges, its
  owner, and its controller.  A volume, a ground box, and a machine are
  then the same kind of page with different boxes.
- **Long lists fold.**  A box that would list more than a few names,
  such as "allowed to call" with 30, shows a count that opens to the
  list.
- **One link style.**  A name that links looks the same wherever it
  shows.  Today a page mixes green underlined names, bold white names,
  gray chips, and green names with no underline.
- **Existing parts only.**  The boards use the box and card macros and
  the tokens the map, the section view, and the Player board use.

### Ground boxes link out

`Outside` gains `url`, a plain `http` or `https` URL to the thing's own
console.  A ground box's title links to its transect page,
`/ground/{name}`, and a `↗` beside it links out.  A `url` of any other
scheme shows as text.

### Volumes

Capacity, access modes, reclaim policy, and what uses a volume come
from the API.  Used bytes come from Prometheus, through a query that is
not in the code.  The smallest resource that can hold it is a
`MetricsSource` with one query:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: MetricsSource
metadata:
  name: metrics
spec:
  queries:
    volumeUsed:
      query: 'max by (namespace, persistentvolumeclaim) (kubelet_volume_stats_used_bytes)'
      labels: {namespace: namespace, claim: persistentvolumeclaim}
```

`labels` names the label in each answer that holds the claim's
namespace and name.  Prometheus is the one `--prometheus` names.
Without the resource, the volume page says which resource would show
used bytes, the way a missing `LogSource` reads.  A second
`MetricsSource` gets a condition.  Plan 08 widens this resource into the
full `MetricsSource`, with Prometheus's address, the usage queries, and
the alerts section; this shape is the first part of it, so plan 08 adds
fields and changes none.

### Traffic

The entry box shows each Ingress's request rate, p95 latency, and 5xx
share today (c0faeb3), but only when the ingress controller's kind
knowledge declares `frontDoor`, and only per Ingress.  Stage 18 shows
the numbers per backend Service, on every box that draws an Ingress,
and says in gray what would turn them on when nothing does.  It adds no
thresholds and no findings: the numbers stay gray, and transect does
not raise its own alerts beside Alertmanager's.

### Peripheral to adapter, and device facts

- **`part of` is an edge kind, drawn as nesting.**  It joins the fixed
  vocabulary.  A telescope is part of an observatory, an optical train
  part of a telescope, a camera part of an optical train.  On an
  object's board, the object it is part of is the frame around it, and
  the objects part of it are boxes inside it.  An object with two `part
  of` references nests under the first and lists the second as a line.
  Loops do not count `part of`, and a cascade does not follow it: a
  telescope does not stop because its observatory object is deleted.
  A kind with no `part of` references, such as a Player, keeps the
  board plan 05 gave it.
- **A reference can join by value.**  `references[].match` names the
  field on the far object whose value equals this field's, for a
  reference by value, such as a MAC address, in place of by name.  The
  far kind of a match is watched whole, since the join reads a field
  the trim drops.
- **Device facts are keyed by the DRA driver's name.**  A knowledge
  resource is named for the thing it describes, by the name Kubernetes
  gives that thing: a CRD's name for a kind, a DRA driver's name for
  its devices.  So device facts go in a `DeviceKnowledge` resource
  whose name is the driver's, not in a section on whichever of the
  operator's kinds was nearby.  A driver with no `DeviceKnowledge` gets
  the generic reading: each device titled by its device name, every
  attribute listed as a fact, and devices on one machine that share
  `resource.kubernetes.io/pciBusID`, a Kubernetes standard attribute,
  drawn as one box.

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: peripherals.bluetooth.liken.sh
spec:
  summary: A Bluetooth device the radio has paired.
  healthy: {condition: Ready, calm: [Disconnected]}
  references:
    - {path: status.adapter, kind: Adapter, group: bluetooth.liken.sh,
       match: status.address, edge: depends on, label: pairs through}
---
apiVersion: transect.liken.sh/v1alpha1
kind: DeviceKnowledge
metadata:
  name: bluetooth.liken.sh
spec:
  title: {attribute: alias}
  facts:
    - {attribute: battery, label: reports its battery}
    - {attribute: connected, label: connected, whenFalse: not connected}
```

`DeviceKnowledge` fields:

| Field | Means |
|---|---|
| `title.attribute` | the attribute whose value titles a device, in place of its device name; `liken`'s drivers use `name` |
| `facts[]` | the attributes to show, each with a `label`; any other attribute is left out.  A `true` value shows the label alone, and a `false` value shows `whenFalse`, such as "not connected", or nothing without it |
| `references[]` | an attribute whose value names another device, by `driver` and the attribute there that holds the same value, with a `label`, such as an HDMI audio sink that is the speakers of a display |

### Every change to kind knowledge in this plan

| Change | Means | Stage |
|---|---|---|
| `healthy.calm` | condition reasons that mean the object is at rest | 11 |
| `part of` edge kind | nesting on an object's board | 19 |
| `references[].match` | a reference by value, with the far kind watched whole | 19 |

`DeviceKnowledge` is a new resource, not a change to kind knowledge.

## Stages

Each stage builds, passes `cargo test`, `cargo clippy --all-targets`,
`cargo fmt --check`, and `loq check`, and is looked at against the
real cluster before its commit.

### 10. Repairs

Every item the audits marked as broken or to fix first:

- **Table columns.**  `td.objects-name` has `display: flex`, which
  takes the cell out of the table layout, so on a kind page and in a
  Manages table every value after the name sits one column left and the
  row rules step.  Put the flex on an element inside the cell.
- **Search finds kinds.**  Each custom kind is in the index by its
  kind, singular, plural, and short names, and leads to its
  `/kinds/{plural.group}` page; an object also matches on its kind's
  names, after the kind's own row.  "telescope" then lists the
  Telescope kind and the telescopes.  The letters-in-order tier shows
  only when no stronger tier matched, since its matches are noise
  beside a real one.
- **Search's dialog.**  Back never reopens it: the dialog closes before
  htmx saves the page to history, and a restored page starts with it
  closed.  Escape always closes it.  The close button uses the
  existing tokens, not the only blue in the app.
- **One row for a machine.**  A Node and the `liken` `Machine` of the
  same name are one search row that opens `/machines/{name}`; the
  machine page links the `Machine` resource.
- **Search stays current.**  `/search` has no event stream, so it can
  rank a model as old as the last page load.  A search starts a build
  when the cluster has changed since the model it reads.
- **Search is visible.**  The masthead has a search control that
  shows its shortcut, and a link to `/kinds`.
- **Relations on parcels are readable.**  A selection's relation to a
  parcel reads in full or keeps its direction when cut, and moves no
  parcel.  It is now the short line that takes the place of the
  parcel's count, as the design says.
- **"If it goes down" from structured pieces.**  `IfDown::lines`
  returns pieces and renders them as `Hops::parts` does for the loss
  cascade.  The other card lines that call `Linker::parts` move to
  pieces the same way, and `Linker::parts` and `links::linked` are
  deleted, so no stored text is searched for names.
- **Helm out of `src/`.**  `src/ownership/controller/packaged.rs`
  matches only the `app.kubernetes.io/` labels: `part-of`, `instance`,
  and `name`, narrowed by `component`.  Helm's
  `meta.helm.sh/release-name`, `meta.helm.sh/release-namespace`, and
  `helm.sh/chart` go, and the module comment says what the code does.
  Shared labels say two objects were installed together, not that one
  reconciles the other, so every attribution by labels draws as a
  guess, with one workload matched or several.
- **Generic test data.**  `src/search/rank.rs` uses generic names for
  its workloads.
- **Links survive any name.**  Each path segment goes through
  `links::percent_encoded`, so a ClusterRole named `a?b` links to its
  own page.
- **Pages show only working controls.**  The edge checkboxes show only
  on pages that draw edges, not on `/kinds`, kind pages, generic pages,
  or the logs lens.
- **Smaller layout bugs.**  The section view's heading clears the tab row; the
  machines band on a namespace page keeps "name · N pods" on one line;
  "+3 more" names each object with its kind; a section view whose object has no
  entry, such as a Player's or a Remote's, is not titled "From entry to
  storage".
- **Comments.**  The orphaned doc comment in `src/loss/sidebar.rs`
  goes, and `src/cluster/custom.rs` stops describing a planned move.

Copy, from the UI audit:

| Where | Now | Becomes |
|---|---|---|
| a card's "If it goes down", when the two objects share a name | "{name} can't reach {name}" | "Player {name} can't reach Remote {name}" |
| the logs lens | "The last 50 lines of {container} from the log source {source}, read at …" over fewer lines | "Container {name}: its last {n} lines, from {source}, read at …" |
| generic page headings | "Columns", "Names", "Named by" | "Status", "Refers to", "Referred to by" |
| Manages, matched by image | "Matched by the controller image the kind knowledge … names." | "Matched by image: the kind knowledge {name} names this workload's image as its controller." |
| Manages, matched by labels | "Matched by the labels the CRD … shares with this workload: …" | "A guess from labels: this workload and the CRD {name} both carry {labels}." |
| a missing PodDisruptionBudget | "Nothing limits how many of its 2 pods evictions take at once, so a drain can take the last ready one while the others are still starting." | "No budget limits evictions, so a drain can evict both pods at once." |
| a card's "If it goes down", one consumer per line | five lines, each "{name} loses {what}" | one line: "{a}, {b}, and {c} lose {what}" |
| the loss card's "What else goes with it" | one clause per object, run together | one line per lost controller: "Until {controller} returns, nothing reconciles {n} {Kinds}.", the names behind a link |
| a board's center box | "Each relation names the field it was read from." | a caption under the board |
| the DaemonSet matrix legend | "no pod, though nothing keeps one off" | "no pod, and nothing excludes this machine" |
| card legends and headings | "kind knowledge", "the notes on the kind …", "notes on the resource type" | "kind knowledge" everywhere |
| a CRD description on `/kinds` and a generic page | cut mid-sentence with "…" | cut at the last full sentence |

Look: a kind page and an operator's Manages table put each value under
its header; type "telescope" and the kind's page is the first row;
search, open a result, press Back, and the dialog is closed; Escape
closes it every time; a machine is one row; the masthead shows search
and `/kinds`; select a namespace on the map and every relation keeps
its direction with no parcel moved; a card whose dependent is named `web` links only
the object, not the word inside the provided text; an operator matched
by labels shows the guess mark.  Files: none.  Design: search's
tiers, its row for a machine, and the masthead; attribution by labels
as a guess.

### 11. Calm reasons

`healthy.calm` in kind knowledge's parser, the health reading, and
the condition list on an object page, where only the deciding condition
takes a color.  Unit tests cover a calm reason, a `False` that is not
calm, and a condition other than the healthy one.  Look: between
reservations, every telescope, camera, mount, and focuser reads gray,
with the condition's message as a gray line under its title, and a
namespace of telescopes has no orange dot; on a telescope's page,
`AttentionNeeded: True` is gray.  Files: kind knowledge for the
observatory kinds with `healthy.calm`, from the reasons their operator
writes when no reservation holds them.  Design: `healthy.calm`, and
color on the deciding condition only.

### 12. One store, no view watch

As the decisions say:

- `src/cluster/trim.rs` keeps `lastTransitionTime` on each condition,
  and computes each printer column's value at trim time, from the CRD
  store's columns for the storage version.  A change to a CRD's columns
  re-lists its kind.  The change signal compares the column values too.
  Date columns keep their timestamp and are formatted at render.
- Delete the per-view watch and everything that serves it.  Every
  object page, kind page, and Manages table reads the stores.
- The cached GET for a built-in kind with no store, and the one table of
  how each built-in kind is read.  PersistentVolume and
  PersistentVolumeClaim pages read their stores.
- `track` in `src/cluster/custom.rs` keeps the old store until the new
  one has listed, and never moves a kind from whole back to trimmed.
  Test it with a fake start.
- A printer column whose path names an object prints `{…}`, and a list
  of scalars prints as a comma-separated list, never as JSON, so a CRD
  author's column on `.spec` cannot show raw spec.

Fix the per-render rebuilds the implementation audit names:

- `guessed_for` (`src/web/snapshot/known.rs`) builds the guess lookup
  for one object on every render.  Build it once per model.
- A kind page clones every object and parses each column's path once
  per row (`src/web/pages/kinds.rs`, `src/web/pages/object/columns.rs`).
  Read the stored column values, with no copy.
- `object_page`, `logs_page`, `objects.rs`, and `pages.rs` build the
  whole `ClusterMap` to learn one namespace's layer.  A `layer_of` that
  reads only namespaces and layers serves all four.
- The duplicates that survive the deletion: one `same`,
  one lookup of a custom kind by group and kind, one function for a
  kind's name in a URL, and no `Catalog.crds`.

Measure on the real cluster before and after, and write the numbers in
the commit message: memory, connections to the API server, and renders
per minute of one open object page.  Look: an operator's page with many
kinds shows every Manages table with its columns at once, with no
"loading" line; a telescope's page shows its printer columns, a date
column included, on first load; a Lease's page says it was read once,
and a reload shows a newer renewal; a volume claim's page opens from
its store; a kind whose objects start and stop owning Jobs never
leaves `/kinds`.  Files: none.  Design: the trimmed store's contents,
every page reading the stores, and the cached read.

### 13. HTTP/2 to the API server

Build the kube `Client` as kube's custom-client example does: a
rustls config from kube's `Config` (`rustls_client_config`), an https
connector from `hyper_rustls::HttpsConnectorBuilder` with HTTP/2 ALPN
enabled beside HTTP/1.1, a `hyper_util` client, and kube's
`base_uri_layer` and `auth_layer` on a tower `ServiceBuilder`.  Carry
over what kube's own connector does that the example leaves out: the
kubeconfig's `tls-server-name`, and its proxy, or keep the default
client when a kubeconfig names one.

hyper's HTTP/2 client keeps one connection per host and multiplexes
every request on it.  A watch holds a stream for as long as it runs,
so the server's `SETTINGS_MAX_CONCURRENT_STREAMS`, which kube-apiserver
sets with `--http2-max-streams-per-connection`, caps the watches one
connection carries.  When a client asks for one more stream than that,
it may wait for a free one with no error, which for a watch is forever.
So the stage proves what happens at the limit, and if the request
waits, it spreads the watches over several clients, each its own
connection, kept below the limit the server sent.

Limit the concurrent probes at startup, whichever protocol the stage
ends with, so the first discovery does not open hundreds of
connections that stay idle until the pool closes them.

Verify against the real cluster, and put the measurements in the
commit message:

- the connections to the API server (`ss -tnp`), before and after;
  one per watch is expected to drop to a handful;
- with the server's stream limit lowered on a test cluster, or the
  client's own limit set below the watch count, a watch past the limit
  opens on a second connection and is never queued;
- HTTP/2 keep-alive pings at an interval with a timeout, so a
  connection that dies with no reset is found and replaced;
- after a connection is reset, such as by killing it with `ss -K`,
  every watch on it re-lists and the pages keep updating.

If HTTP/2 cannot be made sound, record why in the design's section on
the server, keep HTTP/1.1, and keep the startup limit.  Look: the
connection count, and a page that updates after a reset.  Files: none.
Design: how transect connects to the API server.

### 14. Every object page is a board

State the rule in the design, then rebuild to it, with the existing
box and card macros and tokens:

- **The generic object page.**  The object in the middle with its
  conditions and printer columns; its owner, its controller, what it
  refers to, and what refers to it as boxes around it, each name a link
  in the one style.  A relation the page knows, such as a Telescope's
  observatory or a Sink's Node, is a box with a link, not text.  The
  card sidebar, as a workload has: the CRD's description, health, its
  controller, and code.  The board spans the main column's full width.
- **A board of a known kind.**  The sections that hang under board 9
  today become boxes on the board, and a board with one relation does
  not leave one box in a corner.
- **`/kinds`.**  One box per API group, each kind in it with its count,
  health, and controller, in place of one table of every kind.
- **A kind page.**  The kind's description and controller in a card,
  and its objects as a table in a box, one row each.
- **Manages.**  One box per kind on the operator's page, in the row
  with Runs and Guards, and the sidebar's Manages list counts by kind
  in place of repeating each namespace.
- **The namespace card.**  It no longer repeats the workloads the main
  column lists, and on a narrow window it stacks below the map, as a
  workload's card does.
- **The section view** uses the column's full width.
- **Long lists fold.**  A box with more than a few names shows a count
  that opens to them.
- **One link style** for a linked name, on every page.

Look: a telescope, a Service, a Lease, `/kinds`, a kind page, and a
database operator's page each read as boxes across the full width, with
the card beside them; "allowed to call" on a Player's page reads as a
count that opens; every linked name looks alike.  Files: none.  Design:
the board rule, in place of the generic page's list of sections.

### 15. Machine pages in boxes

A machine's page is a board: a header strip with role, readiness, and
CPU and memory bars; then one box per layer, in the map's order, with a
namespace box inside each and a row per pod: name, workload, CPU and
memory bars, restarts, age.  DaemonSet pods fold into one row per
DaemonSet, which links to its namespace's DaemonSet matrix.  Devices
group by driver, each held one linking to its claim.  Then the volumes
whose data lives on the machine, and the machine underneath when kind
knowledge describes one.  Look: open a machine, and its pods sit in
their layers as on the map, with the DaemonSets folded.  Files: none.
Design: none.

### 16. Ground boxes link out

`/ground/{name}` for each `Outside`, built as a board: its title, `why`
and `gotcha`, the addresses and drivers it lists, its volumes by type,
and every workload that depends on it.  `Outside.url` adds the `↗`
link wherever the ground box draws: the map's ground band, the section view,
and its own page.  An address or driver no `Outside` names gets the
same page by its name.  Look: the NAS's ground box opens its page, and
its `↗` opens the NAS's console.  Files: the NAS's `Outside` gains
`url`.  Design: `Outside.url` and `/ground/{name}`.

### 17. Volumes

`volumes/`: `/volumes/{pv}`, with the claim's URL showing the same
page, built as a board.  Capacity, access modes, reclaim policy, and
age; used and free from the `MetricsSource` `volumeUsed` query, with a
gray line saying so when no `MetricsSource` declares it or Prometheus
has no series for the claim; the pods that mount it, each with its
machine and whether it writes; where the bytes are, through the
StorageClass and CSI driver to a ground box or a machine; and the
CronJobs that mount the same claim.  Every volume box and line on
other pages links here.  Look: open a database's volume, and it names
the machine or the NAS it lives on, the pods that mount it, and how
full it is.  Files: a `MetricsSource` with the `volumeUsed` query, for
the kubelet's volume metrics.  Design: `MetricsSource`'s first query,
beside `LogSource`.

### 18. Traffic on the entry box

Each backend Service on the entry box gets its own requests per
second, p95 latency, and 5xx share, in gray, under its Ingress.  The
same numbers show on the section view's entry boxes and on an Ingress's board.
When no kind knowledge declares `frontDoor`, the entry box says in one
gray line that the ingress controller's knowledge would show traffic.
No graphs, no thresholds, no Now line, and no tab.  Look: open a
workload behind an Ingress and read its numbers on the entry box and
in the workload's section; move the ingress controller's knowledge aside, and the gray
line names what would turn them on.  Files: none.  Design: the entry
box's numbers.

### 19. Peripheral to adapter, and device facts

- `part of` joins the edge vocabulary and draws as nesting on a board,
  as the decision says.
- `references[].match`, with the far kind of a match watched whole.
- `DeviceKnowledge`, named for its DRA driver, and the generic reading
  of a ResourceSlice's devices.  Devices on one machine that share a
  PCI bus ID draw as one box, such as a GPU and its media capability
  device.  A device reference by attribute draws an edge, such as an
  HDMI audio sink to the display it is the speakers of.

Unit tests cover a match with one far object and with none, nesting
with one and two `part of` references, and a device with knowledge and
without.  Look: open a telescope, and its observatory is the frame, its
optical trains are boxes inside it, and each train holds its camera,
filter wheel, and focuser; a Player's page is unchanged; the section view of a
remote that holds a Bluetooth device draws the device's line to the
adapter it pairs through; a machine's devices read as named hardware
with a few facts each, and a USB radio shows its product name in place
of its device name.  Files: kind knowledge for the observatory kinds
with `part of` references; kind knowledge for the Bluetooth Peripheral
with its `match`; a `DeviceKnowledge` for each of `liken`'s audio,
display, Bluetooth, and media drivers.  Design: `part of`, `match`, and
`DeviceKnowledge`.

### 20. Check the design, crawl, and stop

Read `plans/00-design.md` against the code: every resource, field,
annotation, edge kind, and URL it names exists with that name, and
every one the code reads is in it.  Fix what disagrees.  Crawl every
page of the real cluster and of the fixture cluster: every page answers
200, and no linked name leads nowhere.  Then stop for the owner's pass,
with these to show him:

- the sticky sidebar with its own scroll, and the logs lens, which he
  raised as questions ("not sure", "let's rethink") and which were
  built without him;
- the map's parcels, which sort by a live pod count and so can swap
  places between two loads, against the design's rule that every box
  keeps its place;
- the logs lens, which colors every warning line, so a run of
  harmless warnings reads as a wall of orange.

Look: the design and the code agree on every name, and the crawl is
clean.  Files: none.  Design: whatever disagreed.

Done as review D and one crawl of the real cluster, then the fix pass
listed under "Built: stages 10 to 20".  The three items above are still
for the owner.

## Not in this plan

- Moving project knowledge out of the code: Flux, the full
  `MetricsSource`, the ingress controller, forges, and the rest: plan
  08.
- The access lens, hardening, exposure, and Secret pages: plan 09.
- Running in the cluster: plan 07.
- The flow agent, the `calls` edge, and allowed against observed:
  slice 2.
- Thresholds on traffic, and any finding from it.
- Attributing a kind to its controller from the ServiceAccounts RBAC
  lets write `<plural>/status`, narrowed by the status managers seen.
  It would rank ahead of the label guess.
- Naming devices by the DeviceClass that selects them, which needs a
  CEL library.
- Vulnerability reports and image supply chain facts.
- Snapshots, the MCP server, and a cascade past one hop.

## What the feedback asked for

Every item from the owner's feedback after the overnight build, with
the commit or stage that answers it.

| Asked for | Answered by |
|---|---|
| the `transect` title and the cluster crumb link home | done, 9f320e3 |
| "allowed to call" is noisy | done, b769ba4; 4835ad1 folds the long lists on object pages, and the map says "may call it" only when no other relation applies (216a579) |
| a second click drills in instead of deselecting | done, e9c1109 |
| text cut off in a custom resource's box | done, 62281df |
| selecting moves everything | done, e9c1109; a6437e7 and 216a579 put a short relation in place of the count, so no parcel moves |
| the cut link overlaps the volumes heading | done, 62281df; 0e3b861 made it a tab, 5981dba named it section |
| truncation on the Player page, which should keep its look | done, 62281df; bcc937d keeps the board |
| the cut is hard to find | done, 0e3b861 and bc9f551; 08a6461 shows search in the masthead, and 5981dba names it a workload's section |
| boxes on the section view and the Player page should link | done, ead6a97 and 3288a6f; claim, device, and ground boxes link in 20a4a48 and aeecf2b |
| a peripheral linked to the adapter it pairs through | done, ea6a2ea and b1c43ac: `references[].match`, and a held device's line to the radio its `DeviceKnowledge` names; 68a5231 keeps a match in one namespace |
| custom resources with no pods, such as a telescope | done, fa5e898, 3a494d7, and 75844d2; 0f2a856 finds them by kind name, 90aa0a1 draws them calm, and 8dfd5e0 nests them by `part of` |
| the Middleware and secrets-operator chips do not link | done, fa5e898 and 3a494d7 |
| the sidebar runs image lines together | done, b769ba4 |
| every card's title links to its page | done, 3288a6f and 3a494d7; ab6c50d gives links one style |
| a fast ctrl+k search | done, bc9f551; 0f2a856 and 08a6461 repair it, and 45a6f0f finds ground boxes |
| a ground box links to the thing's own dashboard | done, f60bb45; 644fd52 lists what stores on it |
| the DaemonSet matrix | kept; 00cc7bf links it from machine pages |
| machine pages are a list of links | done, 00cc7bf and 959081a |
| richer facts from ResourceSlices | done, b1c43ac: `DeviceKnowledge` by driver, and devices that share a PCI bus ID drawn as one; 1ca6102 titles a device by it everywhere |
| which operator manages which kinds | done, e3fe0fe and b3f3996; 64fbaea fixes its columns, 3a11913 boxes it |
| "front door" reads too cute | done, 3288a6f, as "entry"; plan 08 renames the knowledge field |
| PersistentVolumes get pages | done, 769ce91: `/volumes/{pv}`, with the bytes used from a `MetricsSource`; 7b65484 titles it by its claim |
| the sidebar is long; logs may not belong there | done, b769ba4 and 0e3b861, for the owner's pass; 113645e shortens the namespace card |
| ingress request rates are read but not shown | done, f42b54b: per backend, in gray, on the entry box, the section view, and an Ingress's board |
| typography, theme, and styles | no change; bcc937d and ab6c50d bring the new pages to the same styles |
| a security and access lens | plan 09, a draft to design with the owner |
| stay outside for now, aim for in-cluster | plan 07; 5aefdfe cuts the connections it would inherit |
