# 00: The design of the proof of concept

This document was `plans/00-design.md` until 2026-11-20, and the
completed plans beside it name it by that path.  It describes the code
as it is.  The maintainer set that code as a proof of concept on
2026-11-20, and the product design that new work follows is now in
[../00-design.md](../00-design.md).

## The problem

A cluster run by one person, with agents doing much of the work, changes
faster than that person can remember how it fits together.  The
documentation is complete and long, and reading it is the slow way to
find out what broke at 2am or how two services relate on a quiet
afternoon.

`transect` is a map for both of those questions.  It uses what people
remember well:

- **Places.**  Spatial memory is the strongest kind.  Every box keeps
  its position from one visit to the next, so "the database corner"
  means something without reading a label.
- **Recognition over recall.**  Seeing a familiar picture brings back
  how a part works faster than reading about it.
- **Small chunks.**  Each level shows a handful of boxes, and the next
  level down opens on a click.

It avoids panels of graphs that all look alike and a drawing of every
edge at once, because both tire the reader.

## What it shows

### Elevations

The map nests boxes, one level inside the next:

1. the cluster
2. a layer, such as machines, platform, operators, or applications
3. a namespace
4. a workload: a `Deployment`, `StatefulSet`, `DaemonSet`, `CronJob`, or
   an operator's own kind
5. a pod
6. a container

The Kubernetes API records every level except the layer.  A namespace
gets its layer from its `layer` annotation, from a layer's namespace
selectors, or from kind knowledge, as "Layers" describes.

Positions come from the hierarchy by a deterministic layout, so the same
cluster draws the same map each time.

### Edges

An edge has a kind, and the viewer turns each kind on or off.  Traffic
is one kind among several, and the map is not organized around it.

| Kind | Example | Source |
|---|---|---|
| runs on | a pod → its machine | the pod's `spec.nodeName` |
| stores on | a database → the NAS that backs its volume | PVC → `PersistentVolume` → `StorageClass` |
| holds | a home automation bridge → a USB radio | `ResourceClaim` → `ResourceSlice` |
| reconciles from | a service's objects → the applier that applied them → its source | the marks a GitOps tool writes and its source references, read through kind knowledge's `applies` and `source` |
| depends on | an application → the database its environment names | addresses in pod specs, kind knowledge's references, and intent's `depends-on` |
| managed by | a Redis → its operator | owner references and the custom resource's kind |
| authenticates through | an application → the identity provider | an `Ingress`'s forward-auth middleware, read through kind knowledge, and intent's `authenticates-through` |
| allowed to call | a client → a database | NetworkPolicies |
| part of | a camera → the optical train it is part of | kind knowledge's references |

A cycle among these edges is a cold-start risk: two parts that each need
the other to come up.  The map shows cycles.  `part of` records what an
object is a piece of, not what it needs, so loops do not count it, a
loss's cascade does not follow it, and the edge row has no switch for
it.

An edge to something outside the cluster, such as a NAS or a cloud API,
ends at a box for that thing on the map's ground band.  Each address
and each CSI driver is a box of its own until an `Outside` resource
gathers them into one thing (see "Outside the cluster").

### Health and security

- Alerts from Alertmanager, error rates, restarts, and readiness color
  each box.  A healthy box and a normal edge are gray, so only a
  problem has a color, and the map of a day with no problems is gray.
- A vulnerability scanner's reports, read through kind knowledge's
  `findings`, give each finding that the scanner reports in the images
  a workload runs now.  `transect` lists them on `/vulnerabilities`, on
  one page per finding id, and on a workload's vulnerabilities tab.
  The severity and the score are the scanner's.  Wherever a severity
  word is named, it is red for critical, the brand's warm orange for
  high, and gray otherwise, and nothing on the map or in the Now list
  counts them.  A report is current only when a pod of its workload
  runs the image digest that the report names.  Plan 10 describes the
  pages and the rule.
- The access lens answers two questions: what may reach a port, and
  what an account may do.  It answers them as a matrix for the cluster
  (`/access` and `/access/accounts`), with both on a namespace's access
  tab, and as a drawing for one workload or one account.  The lens
  draws what the cluster's objects declare so that a reader can see the
  gaps.  It is not an alerting system: it raises no finding, uses no
  problem color, and counts nothing as an issue.  Its sentences are
  plain facts in the present tense, with "may" and never "could", and a
  sentence that the people who run the cluster wrote is shown beside a
  fact as intent.  `transect` reads access from the RBAC objects alone,
  with no `SubjectAccessReview` and no `Secret`'s data, and does not
  draw access on the cluster map.  Plan 09 describes the pages, words,
  and marks.

### Code

Each box links to two kinds of code:

- **Where it is declared.**  A GitOps tool marks every object it
  applies with the applier that applied it, in the label or annotation
  that kind knowledge's `applies` names.  The applier's `directory`,
  with the URL and ref that the applier or its source fetches, gives
  the directory in the repository.  A tool such as Flux does not record
  which file an object came from, so the link opens the directory.
  Without kind knowledge that declares `applies` for a kind the cluster
  serves, the card states that in place of code, and until an applying
  kind's watch has listed, the card states that reconciles from is off
  for now.
- **What it runs.**  The OCI labels `org.opencontainers.image.source`
  and `org.opencontainers.image.revision` on each image give the
  repository and the commit.  When a registry host does not answer from
  where `transect` runs, such as a host that the machines pull through
  a mirror, `transect` reads its labels through a mirror.
  `--registry-mirror <host>=<mirror>` names the mirror, or a
  cluster-scoped `RegistryMirror` resource names it with `spec.host`
  and `spec.mirror`.  A mirror is an `http` or `https` URL, which
  `transect` requests the way containerd does, with the host in the
  `ns` query.  A mirror may also be `api-server`, for the registry that
  the API server's address also serves, as k3s's embedded registry
  does.  The flag takes precedence over the resource.

Both links go to a forge's pages, and each forge lays them out
differently.  A cluster-scoped `Forge` resource declares one layout and
the hosts that serve it:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: Forge
metadata:
  name: a-forge
spec:
  hosts: [forge.example.com]
  hostPrefixes: [forge]
  images: [forge-server]
  tree: '{repository}/src/{refKind}/{ref}/{path}'
  commit: '{repository}/commit/{sha}'
```

A host belongs to the forge when `hosts` names it, when `hostPrefixes`
names its first label, or when the cluster serves it through an
`Ingress` to a workload and `images` names the last part of that
workload's image.  In `tree`, `{repository}` is the repository's web
address, `{ref}` is the branch, tag, or commit, `{refKind}` is which of
the three it is, and `{path}` is the directory, which is left out with
its slash at the root.  In `commit`, `{sha}` is the commit.  When a ref
does not state whether it is a branch, a tag, or a commit, a layout
with `{refKind}` links it only when it is a whole commit.  A wrong
guess at a link opens a 404, so a repository on a host that no `Forge`
claims is shown as text.

### Cards

A box opens to a card that fits one screen: what the thing is, why it
exists, the one thing that goes wrong with it, what it depends on, what
depends on it, and its links to code.

## How it works

`transect` keeps no store of its own and has no history.  It shows the
cluster as it is now.

### The server

The server is one program, written in Rust, on kube-rs for the watches
and caches and axum for HTTP.  It watches the Kubernetes API with
informers and keeps the graph in memory, queries Prometheus for health
on the scrape interval, and streams changes to each open browser with
server-sent events.  Its access to the cluster is read-only, except for
the status of its own resources.

It is not an operator in the usual sense: it reconciles nothing into
the cluster.  It writes conditions on its own resources only, such as
"this selector matches nothing" or "this PromQL does not parse," so that
a person writing one learns at once what is wrong with it.

`transect` uses HTTP/2 to the API server when the server offers it, so
its watches share a few connections where HTTP/1.1 would take one
connection per watched kind.  hyper keeps one HTTP/2 connection per
host, and a request past the server's `SETTINGS_MAX_CONCURRENT_STREAMS`
waits on that connection with no error, which for a watch is forever.
So `transect` reads the limit the server sends when it starts, opens
streams up to a fifth below the limit on one connection, and sends a
request that would pass that number to a new connection.  `transect`
pings each connection after 20 seconds without a frame and closes it
when a ping gets no answer for 10 seconds, so a connection that dies
with no reset ends its watches, and the watches resume on a new
connection.  As kube's own client does, `transect` retries a request
that the server answers with 429, 503, or 504, with backoff, and waits
for the time a `Retry-After` gives.  When the handshake chose h2,
`transect` sets each connection's client to HTTP/2, so hyper opens one
connection for a burst of requests rather than one each.  A server
that offers only HTTP/1.1, as `kubectl proxy`'s http:// address does,
gets one pool of HTTP/1.1 connections, with no stream limit to spread
requests under.  A kubeconfig that names a proxy gets kube's own
HTTP/1.1 client.  `transect` probes custom kinds eight at a time at the
start.

`transect` never writes live data into a resource's status.  Health and
traffic change every few seconds, and writing them to the API would
load etcd for no reader that needs it.

### The web interface

The server renders the pages.  HTMX swaps fragments into them and
server-sent events push changes, so the interface is not a single-page
application.  Each elevation has its own URL, so zooming is navigation
and every view can be linked.  A page loads a client library only where
the library improves the page.

A page request waits for the model to be built only when it would
otherwise answer 404.  It renders from the last model built, and the
page's event stream delivers anything newer.  The cluster changes every
few seconds and a build takes from a tenth of a second to most of a
second, so a click that waited for one would stall often enough to
notice.  A page whose object the last model does not contain, while the
cluster has changed since that model or a build is under way, waits for
that one build before it answers 404, so a link to an object created a
second ago opens it.

The map is dark by default, calm, and gray where things are healthy.
The top level is horizontal bands, one per layer, and the namespaces in
each band are drawn as parcels sized by weight, like a treemap.  Every
namespace gets its own parcel, at a minimum size if it is small, and a
band grows taller instead of folding a parcel away.  Parcels sort by
namespace name, alphabetically and ignoring case, so a reader finds a
name at its place in the alphabet and a parcel keeps its place as its
workloads come and go.  A parcel's width still grows with its workload
count.  The folded bands, the loss view, and a machine's page list
namespaces in the same order.

The sidebar shows what to know right now when nothing is selected, and
the card of the selection when something is.  The sidebar contains the
card and nothing else, stays in place beside the main column, and
scrolls on its own.  A namespace's card lists its workloads as chips
beside the map, because the map's parcel only counts them.  On the
namespace's page the card does not list them, because the main column
draws each one.  In a narrow window a card stacks below the map or the
board it belongs to, and only the cluster page's "Now" and a loss come
first.

Every table scrolls with the page, with its header row fixed at the top
of the window, and no table scrolls in a box of its own.  A table fits
its column by wrapping its names.  Under 1400px (1600px for a
namespace's eight-column ports matrix), a table too wide for the column
beside the card takes the page's width, with the card below it.  What a
workload runs is a Runs box in the main column, with one row per image
and its repository, version, commit, and roll age in labeled cells.

The machines band draws the control-plane machines first, by the
`Node`'s `node-role.kubernetes.io/control-plane` label, each with a sage
border, and the workers after them.  Every other row of machines does
the same: a machine page's switcher row, which names its groups with the
band's gray "control plane" and "workers" labels and draws no sage
border, since its ring marks the machine shown; a folded namespace's
machine chips; and the `DaemonSet` matrix's columns.

On the map, a selection outlines the parcels its edges reach in gray,
and each one's second line shows how it relates in place of its workload
count, with "it" as the selection.  Every edge kind that crosses between
two namespaces gives a pair of short phrases, and the first word of each
gives the direction, so a line cut by a narrow parcel keeps it: "it
needs" and "needs it" for depends on, "it logs in via" and "logs in via
it" for authenticates through, "manages it" and "it manages" for
controlled by, then "it stores on" / "stores on it", "it holds" / "holds
it", "contains it" / "it contains" for part of, "it routes to" / "routes
to it", "it selects" / "selects it", "applies it" / "it applies" for
reconciles from, "owns it" / "it owns" for managed by, "it runs on" /
"runs on it", and "it may call" / "may call it".  The line shows one
phrase, the first by priority: a relation read from the cluster before a
guess, then by kind in that order: depends on, authenticates through,
controlled by, stores on, holds, part of, routes to, selects, reconciles
from, managed by, runs on, and allowed to call last.  Within a kind,
what the selection does to the parcel comes first.  More relations add
"+N", and the title lists every one.  A relation whose every edge is a
guess ends in "?", and the title states what it was guessed from, "a
guess from labels" for a controller.  The line keeps the count's type,
ends in an ellipsis inside the parcel when it does not fit, and never
changes the parcel's size.  Color is kept for a loop and for problems.
For "allowed to call", only the callers a policy names get a relation.
A rule that admits every namespace marks no parcel and is one line on
the card, "Any namespace may call it."  No `NetworkPolicy` applies to a
pod on its machine's network (`hostNetwork: true`), so a workload whose
pods are there marks no parcel, and its card states that it is on its
machine's network and any pod may call it at its machine's address.  A
pod on its machine's network calls from its machine's address, so a pod
or namespace selector in a policy does not admit it as a caller, and an
`ipBlock` that contains the address does.

Every object page has a row of lens tabs at the end of its top row, each
tab its own URL, showing only the lenses that apply: the overview at the
object's URL on every object, the section view at `…/section` on
workloads, "if lost" at `/?lose={name}` on machines, and the logs at
`…/logs` on pods and workloads, the access at `…/access` on a workload
that serves a port, and the vulnerabilities at `…/vulnerabilities` on a
workload a scanner's report names, whose card shows the count line of
its current reports.  The loss view keeps its query form because it
takes a set of machines.  The logs lens is full width and shows one
pod's lines a container at a time, with the lines each log pattern tags
and the source they came from.  A workload's logs lens lists its pods,
each with its health's dot, the least healthy first and then the newest,
and opens on the first: the pod that is failing, or the run that just
happened.  A pod's started init containers are offered beside its app
containers, marked "(init)".  The pod page keeps its last five lines as
a preview that links to the lens.

Quick search finds any object with a page, a volume by its claim's name
among them, any namespace or machine, each custom kind, each ground box
by its title, its `Outside`'s name, or an address or driver it lists,
each host an `Ingress` answers for, each finding id a current report
contains, and the fixed pages.  The masthead is navigation only: the
wordmark, the crumb, links to `/access`, to `/vulnerabilities` when some
kind knowledge loaded has `findings`, and to `/kinds`, and a search
control with its shortcut.  `ctrl+k` and `/` open search empty, and any
other character typed outside a form field opens it with that character.
Escape closes it, and Back never reopens it.  Each keystroke asks the
server, which scans an index built once per change of the cluster.  A
kind is found by its kind, singular, plural, and short names, and an
object also by its kind's names, after the kind's own row.  A machine is
one row, whether its `Node` or a resource that describes it, through
kind knowledge's `node`, matched.  Names rank by an exact match, then a
prefix, then the start of a word after `-`, `.`, `/`, or `_`, then the
query whole inside the name, then its letters in order, the tighter the
better, and only when no stronger tier matched; ties break by kind, then
by name, and kinds of one name by their API group in DNS order.  Health
never changes the order, so one query gives the same list each time, and
a dot on each row shows it: a namespace's, a workload's, a pod's, or a
machine's.  A kind nothing assesses shows no dot.  A query that is an
address, such as a `Service`'s DNS name, resolves to the workload behind
it.  Enter opens the first row, and Tab reaches a row's lens chips, so a
workload's section is two keys after its name.  Nothing is kept in the
browser.

The edge kinds filter a page's picture, so their switches are in the
page's main column, a quiet row at the right directly above what they
filter: under the switcher row on a workload, a pod, a board, and a
GitOps object, at the top of the cluster and namespace maps, and on the
legend's line in a section view.  Below 1260px they fold behind one
"Edges" control.  They show only on pages that draw edges: not on
`/kinds`, a kind's page, a namespace's list of one kind, a generic
object page, a volume's page, a machine's page, a ground box's page, the
logs lens, the access pages, `/changed`, search, or the page for an
object that is gone.  The kinds chosen are the URL's `edges` parameter.

Most views draw no lines: an edge shows at its far end, as a short
relation or a count on the box it reaches.  Lines over wrapped parcels
would need each box's position, which only the browser has after layout.
A workload's section takes its name from architectural drawing, where a
section is the drawing of a vertical slice and the companion of an
elevation; here it is one service drawn down through the layers from its
entry to its storage.  The section view is a fixed layout, so the server
computes every box's position and draws its lines as SVG beneath HTML
boxes, with no graph library in the page.  A workload's entry is the
Ingresses and Services a request reaches it through; the word is
"entry" and not "ingress" because the entry may be a `LoadBalancer`
`Service`.  Each `Ingress` on the entry box, in the section view's
entry, and on its own board shows each backend `Service`'s requests per
second, p95 latency, and 5xx share, a gray line per backend, named when
it has several.  The queries come from the `entry` of the
IngressControllers (see "Ingress controllers"); with none,
the `Ingress` states in one gray line that an `IngressController` with
entry queries would show its traffic.  The numbers have no thresholds
and raise no findings, since Alertmanager already does.

One link rule applies on every page: an object's name links to its page
wherever it shows, as a card title, a chip, a box in a section view,
each name a "+N more" box represents, a row, an edge's far end, or a
name inside a sentence a card or a loss writes.  One askama macro,
`object_link` in `crates/transect-graph/src/links/`, draws each name,
and `map::pages::Pages` decides which objects have a page, so a name
with no page is plain text and never a link that leads nowhere.
Text a person stored, such as what intent states a workload provides, is
never searched for names: a loss's cascade line keeps the objects it
names apart from its words, so a workload named `web` or `database` does
not turn those words inside stored text into links.  A crawl of every
page of a fixture cluster fails on a link that opens no page and on the
name of an object with a page outside a link.

The access lens has one exception to the rule: on a page of the lens, an
object's name opens its access page when it has one, since a reader
there is following access, and its usual page otherwise.  "Access"
below lists which pages are in the lens and which links lead out of it.
A `ServiceAccount`'s page is its access drawing, so its name opens the
same page everywhere.

A name is written as kubectl writes it: a namespaced object is `Kind
namespace/name`, such as "Deployment shop/web", and a cluster-scoped one
is `Kind name`.  The namespace is left out on a namespace's own page and
on a page about an object in the same namespace, and the kind is left
out where the place already gives it, such as a table of one kind or a
box whose line names the kind.  `links::kind_and_name_in` and
`links::name_in` build every such name, and `object_link` lets a long
one wrap after its slash before it breaks inside a word.  A sentence
names objects the same way, as in "Until ops/applier returns, nothing
reconciles 3 Trees", and so does a condition on `/resources`.  No box or
row adds "in <namespace>" under a name, since the namespace is in the
name or is the page's own.  "in" names a namespace only as a scope:
where a `ClusterRole`'s binding makes it apply, what a policy admits, as
in "every pod in shop", or the namespaces a box for several callers
represents; and "in" names a container in a pod.

Every object has a page.  A namespaced object's is
`/namespaces/{ns}/{resource}/{name}` and a cluster-scoped one's is
`/cluster/{resource}/{name}`, where `{resource}` is kubectl's name for
the kind: the plural alone for the core group, `apps`, and `batch`, and
`<plural>.<group>` for every other group.  A kind with a page of its own
keeps it: a workload, a pod, a custom resource of a known kind, an
object of a GitOps kind, a `Node`, whose `/cluster/nodes/{name}`
redirects to its machine, and a `PersistentVolume`, whose
`/cluster/persistentvolumes/{name}` redirects to `/volumes/{name}`.
Every other object opens the generic page.

A volume's page, `/volumes/{pv}`, is a board, and its claim's URL draws
the same board.  It is titled by its claim, the name a reader followed,
with the `PersistentVolume`'s generated name as a fact in its box; a
volume no claim holds is titled by its own name.  The volume is in the
middle with its capacity, the bytes used and free, its access modes, its
reclaim policy, its phase, and its age.  To its left are its claim and
namespace, then the pods that mount it, each with the machine it runs on
and whether it writes, and the CronJobs whose pods mount it when they
run.  To its right is where its bytes are: its `StorageClass`, the
driver that provisions it, and the ground box or the machine that stores
them.  The bytes used come from the `volumeUsed` query of a
`MetricsSource`; without one, or without Prometheus, or with no series
for the claim, one gray line under the title states that.  A claim that
waits for a volume draws the same board around the claim.

Every box that draws a claim, a device, or a ground box links to its
page.  In the section view, a box of one claim is titled by the claim
and opens its page; a box of several is counted, and its last line
names each claim, linked.  A device opens its machine's page at the
device's own line, `/machines/{name}#device-{driver}-{device}`, and a
ground box opens `/ground/{name}`, wherever it draws: the section view,
a known kind's board and its Refers to box, and a card.

Every object page is a board: composed boxes across the page's full
width, as the `Player`'s board and the section view are, and never an
unboxed list of links.  The object is in a box in the middle, under a
strip that names its namespace and its kind's list there.  The box
shows its conditions as chips, the message of each condition that is
not healthy, and its own facts, such as its printer columns, as labels
beside values, with a gray dash for a value that is not set.  The boxes
to its left show what it belongs to, in a box headed "Belongs to" (its
owner and its controller, or a volume's claim; the strip above names
its namespace), and then what names it: every edge that reaches it and
the objects whose annotations name it through `referencedBy`.  The
boxes to its right show what it names: every edge that leaves it.  A
section that its kind has is on the side its facts belong to, and a
section with a table, such as a `Role`'s rules, spans the page under
the board.  A side with nothing to show is left out, and the object
takes its width.  Knowledge adds boxes and labels, and a kind with no
knowledge gets the same board from its edges, its owner, and its
controller.

A known kind whose knowledge names references draws the diagram of its
references, with the object in the middle and a box per reference
around it, in a frame as wide as the column.  The same boxes are in one
row under the diagram, without the names the diagram already shows.  A
known kind whose only references give what it is part of draws no
diagram, and a `part of` reference is never drawn in a diagram.  The
thing an object is part of frames the object's board, under the heading
"Part of", which no other box uses, and the objects that are part of it
are boxes inside its own box, each naming its own parts.  An object
that is part of two things nests under the first that its knowledge
names, and its box names the second in a line.  A cycle of `part of`
edges never draws an object inside itself, because the object and what
it is part of are left out of its parts.  A known kind with no
references, such as an `Observatory`, gets the board with the object in
the middle, and no edge kinds above it.

The card beside the board shows the CRD's description, the health in a
word, the controller, the GitOps objects that apply it as its code, and
the kind's knowledge.  A card that would show none of those states what
the object is in plain facts: its kind and group, its namespace, its
age, and that no controller was found.  A fact that names more than six
things shows its count, which opens to them, as a card's row of more
than six callers does, and a linked name looks the same on every page.
A page never renders a raw spec or YAML.

The built-in kinds with sections of their own are a `Service`, an
`Ingress`, a `ConfigMap`, an `IngressClass`, a `PriorityClass`, a
`Role` and a `ClusterRole`, their bindings, a `StorageClass`, a
`CSIDriver`, and a `ResourceClaim`; a `ServiceAccount`'s page is its
access drawing, under Access.  ServiceAccounts and PriorityClasses
are watched as metadata, and IngressClasses whole, for the controller
each names, so a name that refers to one links only when it exists.  A
`Secret`'s name stays text until its page is built from what names it.

The kind pages are boards too.  `/kinds` is a box per API group, packed
in columns, each kind with its count, how many of its objects are not
ready, and its controllers, named once on the group when every kind in
it shares them; the kinds with no objects fold into a last box.  A
kind's page and a namespace's list of one kind are a strip of the
kind's neighbors (the other kinds of its group, or the namespace's
other custom kinds) over a box with a table of its objects, beside a
card with the CRD's description, the count, and the controllers with
the rule that found them.

A list of names in DNS form, such as API groups, DRA drivers, and
hosts, sorts by the name's labels read from the right and shows each
name as written, so `liken.sh` and `media.liken.sh` are adjacent and
`cert-manager.io` is next to `acme.cert-manager.io`.  The core group
sorts first.  `/kinds`'s boxes, a namespace's custom kinds, a
machine's device drivers, search's kinds of one name, and the hosts
on a card and on Ways in all use this order.

Every page reads the stores, and no render makes a request.  A page of a
trimmed custom kind shows what the trimmed store keeps: its conditions,
printer columns, owner, controller, and references in, and no guessed
references out, since those read the spec.  One table names how each
built-in kind is read: from a store with its status, from a metadata
store, by a redirect to a page of its own, by a cached read, or never.
A built-in kind with no store, such as a `Lease`, a `LimitRange`, or a
`ResourceQuota`, is read with one GET per page load, through a bounded
cache: a copy read in the last five seconds serves a new load, so a
burst of loads makes one request, and every render of the open page
draws the copy its load read.  A load of an object being read waits for
that read; reads of different objects run side by side, at most four at
once.  A `Lease`'s page shows its holder, linked when it names a pod or
a machine, when it was renewed, and how long a renewal lasts, so a
reload shows the next renewal.  A name Kubernetes does not allow, such
as one with a `,` or an `=`, is never read.  An object read this way
keeps no connection open and gets no live updates, and its page states
that in one gray line.  A 404 draws the gone page, and a 401 or a 403
draws one line saying the API server refused the read.  A `Secret` is
never read this way, and a `ConfigMap`'s page reads only its metadata
store.

## What transect knows

Every box, edge, and card comes from one of four layers, and `transect`
marks each fact that does not come from the API with where it came
from.

| Layer | Who writes it | Examples |
|---|---|---|
| Kubernetes | `transect`, for the built-in kinds | owner references, `Service` selectors, `Ingress` backends, PVC → PV → `StorageClass`, `nodeName`, ResourceClaims, NetworkPolicies, PDBs, affinity, probes, resources, the names of the Secrets a pod reads |
| Signals | the cluster, as it runs | usage and allocatable from Prometheus, alerts from Alertmanager, log lines from a log source, Events, commits from the repository a GitOps tool reads |
| Kinds | the operator that owns a custom resource | which fields of a `Player` name its `Machine`, `Remote`, and `Receiver`; which condition means healthy; its layer; its docs |
| Intent | the people and agents who run the cluster | why a thing exists, what goes wrong with it, dependencies nothing else reveals |

### Provenance marks

A fact from the Kubernetes API has no mark, since it is most of the
map.  A fact from a signal names its source, such as Prometheus or the
log source, in its own words.  Every other edge and card line has a
small mark, and the mark's title states where the fact was written:

| Mark | Means | Title, for example |
|---|---|---|
| intent | written by the people who run the cluster | "from why on Deployment web", "from gotcha in the overlay overlays/web.yaml", "from docs in the Intent shop/db" |
| kind | from the kind knowledge its operator ships | "from spec.upstream in the kind knowledge caches.cache.example.io" |
| image | from an image's labels | "from the description label of web:1.2" |
| guess | from what a field of a custom resource is named, or from labels a CRD shares with a workload | "a guess from the field spec.serviceName" |

A card with any marked line ends with a legend line that explains what
its marks mean.  When every line of a section has the same mark, or
marks from one kind knowledge, the section's heading shows the mark
once and each line keeps its title.  One template macro draws every
mark, so every card shows them alike.

Stored text is untrusted.  An annotation, kind knowledge, an `Intent`
resource, a condition's message, and a log pattern's meaning are each
cut to a length that fits on a card and stripped of control characters,
and a `docs` value links only when it is a plain `http` or `https` URL.

### Layers

A cluster-scoped resource declares the layers, top to bottom, each with
a name and any number of namespace selectors; a namespace that matches
any of them belongs to the layer.  A selector lets a label the
cluster already has place a whole group of namespaces, such as a label
that marks every namespace an agent runs in.

A namespace's layer comes from the first of these that names one:

1. its `layer` annotation;
2. the first layer with a selector that matches it;
3. the kind knowledge of the custom resources in it, and when their
   kinds name different layers, the highest of them, with the
   namespace card naming each;
4. the "unsorted" band at the bottom.

The cluster's own `Layers` comes before kind knowledge because an
operator ships with no knowledge of one cluster's layers.  An
annotation that names no declared layer places the namespace in the
unsorted band instead of continuing to the next rule, so the typo is
visible.  A missing assignment is visible the same way instead of being
guessed.

### Four primitives

Most of the map comes from four operations over the first two layers:

1. **Reference walking.**  The built-in kinds' references are in the
   code.  A custom kind's references come from its kind knowledge, and
   without any, `transect` guesses from fields named `*Ref` and `*Name`,
   and from strings that resolve to an address inside the cluster.  A
   guess is a `depends on` edge marked as a guess, with its field as its
   provenance, and a field that matches no object, or several, draws
   nothing.  Kind knowledge turns the guesses off for its kind.
2. **Address resolution.**  Any string that looks like a host, a
   `host:port`, or a URL resolves against `Service` DNS names, `Ingress`
   hosts, `LoadBalancer` addresses, and machine addresses.  An
   environment variable naming a database `Service`, a broker address in
   a custom resource's status, a `GitRepository`'s URL, and an NFS
   server's name all become edges this way.
3. **Controller attribution.**  The API does not record which controller
   reconciles a kind.  `metadata.managedFields` names the field manager
   that writes each object's status, and RBAC names the
   ServiceAccounts allowed to write the kind.  Together they find the
   controller's pods.  When they leave several candidates, as a status
   written as `Go-http-client` does, kind knowledge's `controller.image`
   keeps the candidates that run that image repository.  This follows a
   custom workload's own pods; the objects it leaves without a
   controller, most of them resources that run no pods, such as
   certificates or scan reports, are attributed by kind, by the first of
   two rules that finds a workload: the image kind knowledge's
   `controller.image` names, among the workloads that run pods; or the
   Kubernetes project's recommended labels, `app.kubernetes.io/part-of`,
   `instance`, and `name`, that the kind's CRD shares with exactly one
   workload, narrowed by `component` when several match.  Shared labels
   show that the two were installed together, not that one reconciles
   the other, so an attribution by labels is always drawn as a guess.  A
   workload's page has a Manages box per kind it reconciles, in the row
   with Runs and Guards, whose table folds after eight rows; each box
   and a kind's page state which rule matched.  The workload's card
   counts what it manages by kind, "16 Clusters", and does not list
   namespaces.
4. **Findings.**  Rules turn signals into facts worth knowing: a machine
   whose requests nearly fill it, pods that use more memory than they
   requested, a workload with replicas and no readiness probe, a
   replicated workload with no `PodDisruptionBudget`.  A few are built
   in; the rest are declared as PromQL or log queries.

Alerts come from Alertmanager's API, not from Prometheus's `ALERTS`
series, so that silences and inhibitions apply to the map.  During a
rollout or a lost machine, `ALERTS` shows exactly the alerts an
inhibition rule exists to hide.

### Kind knowledge

An operator ships one small cluster-scoped resource per custom resource
definition, in the same place it ships its CRDs, named as the CRD is,
`<plural>.<group>`:

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
  writesSecret: spec.credentialsSecret
  controller: {image: example.io/cache-operator}
  disruptions: operator
  docs: https://cache.example.io/docs/
```

| Field | Means |
|---|---|
| `summary` | the card's opening line for any object of the kind with no `why` of its own |
| `layer` | the layer for namespaces that hold objects of the kind, when it names a layer the cluster's `Layers` declares |
| `healthy.condition` | the condition whose `True` means the object works |
| `healthy.calm` | condition reasons that mean the object is at rest, such as `Idle`: a `False` with one of them draws gray, with its message as a gray line under the title |
| `references[]` | a field path, with `[]` for each item of a list, and what it names: an object by `kind` and `group`, an address with `address: true`, or, with neither, a value `transect` has no object for, such as a monitor |
| `references[].edge` | one of `runs on`, `stores on`, `holds`, `depends on`, `managed by`, `authenticates through`, `reconciles from`, or `part of`; `depends on` when left out |
| `references[].label` | the verb a card shows, such as "plays through"; the edge kind's name when left out |
| `references[].match` | the far object's field whose value equals this field's, for a reference by value, such as a radio's address, in place of by name.  It joins only a far object in the referring object's namespace, unless the far kind is not namespaced; a value no such object holds names nothing, and a value more than one holds draws no edge and is reported on `/resources` |
| `references[].acrossNamespaces` | `true` lets `match` join a far object in any namespace |
| `references[].role` | what the far object does for the `Secret` the object writes, when a page reads it: `issues-tls` makes it the issuer of the certificate in that `Secret`, so an `Ingress` that serves its TLS from the `Secret` shows "TLS from" it |
| `referencedBy[]` | an annotation on another kind that names objects of this kind: `kind`, `group`, `annotation`, and `name`, a template for one object's name in it, such as `{namespace}-{name}@kubernetescrd`; a template without `{namespace}` names an object of a namespaced kind in the annotated object's own namespace |
| `referencedBy[].role` | what the named object does for the annotated one, when a page reads it: `issues-tls` on an `Ingress`'s annotation makes the named object the issuer of its certificate, and the entry shows "TLS from" it |
| `writesSecret` | the field that names the `Secret` an object of the kind writes, in its own namespace; `metadata.name` when the `Secret` takes the object's name |
| `controller.image` | the image repository its controller runs, without a registry host, so a mirror does not hide it |
| `primary` | for a kind that runs one primary and replicas that follow it, the field naming the pod that takes writes: losing that pod stops writes until the operator promotes a replica, and a loss states that |
| `disruptions: operator` | its operator moves work off a pod before eviction, so a budget that lets no pod go is expected, not a warning |
| `docs` | where to read more |
| `applies` | for a GitOps tool's kind that applies objects: `name` and `namespace`, each a `label` or an `annotation` on an applied object, with `formats` such as `['{namespace}_{name}:*', '{name}:*']` when the value holds more, tried in order until one matches, and a `precedence`, of which the higher is used when an object names two appliers.  Without `namespace`, the applier is the one object of the kind with that name.  `account` gives which `ServiceAccount` an applier applies as: `field`, the field of the applier that names one in its own namespace, and `default`, the account it applies as when the field is empty; without a default, or without `account`, it applies as its controller's account.  The trimmed watch keeps the field |
| `source` | the fields that may hold a reference to the object it fetches through, `{apiVersion, kind, name, namespace}`, tried in order.  It names an object of a kind whose knowledge declares `repository` |
| `directory` | the field holding the directory in its repository that it applies |
| `chart` | the field holding the chart it installs |
| `revision` | `paths`, the fields that may hold the revision it last applied or fetched, tried in order, of which the first value read is used; `at`, the field holding when; and `formats`, such as `{ref}@sha1:{commit}`, which split it into a ref and a commit, tried in order, `*` matching anything |
| `repository` | templates of the repository URL it fetches from, each placeholder a field, such as `{spec.endpoint}/{spec.bucketName}`; the first whose fields all read is used |
| `ref` | the fields holding the `branch`, `tag`, or `commit` it follows, or `name` for a field that may hold any of the three |
| `node` | the field naming the `Node` an object describes, such as `metadata.name` for a resource named for the machine under its `Node`, or `status.nodeRef.name` on a namespaced kind such as Cluster API's `Machine`.  A `Node`'s name is unique across the cluster, so the kind may have any scope.  The machine page shows that object in an Underneath box, and search finds the machine by its kind's names |
| `findings` | for a scanner's report kind: `subject` (`owner: true`, and the `label` or `annotation` naming the container), the fields holding the `digest` scanned and when (`scannedAt`), the `summary` of severities, most severe first, each with its count's `path`, the `items` list, and each finding's `item` fields, `id` and `severity` required and `score`, `package`, `installed`, `fixed`, `link`, and `title` optional.  The trimmed watch keeps exactly these fields |
| `ignoreChanges[]` | a `label` or an `annotation` its tool rewrites without changing anything, for the `kinds` it names, each a `kind` and a `group`, empty for the core group, or for every kind, such as the ownership labels a GitOps tool rewrites on a `Namespace` two of its appliers apply |

The GitOps fields may be on any kind, so a tool that keeps its source in
a kind of its own, as Flux does, puts `repository` and `ref` on the
source kind and `source` on the applier, and a tool that keeps it
inline, as Argo CD does, puts all of them on the applier:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: KindKnowledge
metadata:
  name: trees.apply.example.io
spec:
  applies:
    name: {label: apply.example.io/name}
    namespace: {label: apply.example.io/namespace}
    account: {field: spec.serviceAccountName}
  source: [spec.sourceRef]
  directory: spec.path
  revision:
    paths: [status.lastAppliedRevision]
    formats: ['{ref}@sha1:{commit}', 'sha1:{commit}', '{ref}@*']
```

A `{commit}` matches only seven or more hex digits, so a chart version
is never taken as a commit, and a revision that no format matches is
taken as a ref.  An object of a GitOps kind is ready by the condition
`healthy` names, or `Ready`.

The edge vocabulary is fixed because loops, the cascade, and the edge
row read edge kinds; a verb an operator invents is display text in
`label`.

`referencedBy` reads names that an annotation writes in its own form,
such as an ingress controller's list of middlewares on an `Ingress`,
where a namespace and a name that both contain dashes do not show where
one ends.  `transect` renders the template for each object of the kind
and matches the annotation's entries against those, and never splits an
entry to guess.  A middleware's forward-auth address is then a
reference with `edge: authenticates through`, and the applications
behind the `Ingress` authenticate through what it resolves to.  A
certificate tool's issuer kinds declare the annotations that name an
`Ingress`'s issuer with `role: issues-tls`, so the entry box, the
section view, and the `Ingress`'s board show "TLS from" that issuer,
linked, and with no such knowledge an `Ingress` with TLS shows "TLS".
Most clusters issue through certificate objects instead: a certificate
kind's knowledge names the `Secret` it writes with `writesSecret` and
its issuer with a reference whose `role` is `issues-tls`, and an
`Ingress` whose `tls[].secretName` is a `Secret` that a certificate in
its namespace writes shows "TLS from" that certificate's issuer.  An
annotation on the `Ingress` takes precedence over a certificate.

A GitOps tool's kind is watched trimmed, and its trimmed objects also
keep the fields its GitOps knowledge reads, copied to the same paths, at
the version discovery prefers, so one watch per kind serves both its own
page and the code links, and an applier's list of everything it applied
never reaches a store.  A scanner's report kind with `findings`
knowledge is watched the same way: its trimmed objects keep the digest,
the scan time, the summary counts, and only the named fields of each
finding, never a finding's title or description.  A kind whose knowledge
reads its fields (a reference, the `Secret` it writes, its primary, or a
`node` outside its metadata) is watched in full, even when it runs no
pods, and so is every kind that owns pods or workloads at any depth, and
the far kind of every reference by value, whose field the match reads.
Knowledge that reads only a summary, a layer, a healthy condition, a
controller image, a disruption rule, or docs leaves its kind trimmed, so
a kind of many large objects, such as a scanner's reports, is not stored
whole for a line of text.  Every other CRD the cluster serves is watched
trimmed: on its storage version, read from the CRD store rather than
from discovery, and cut before the store to its metadata; its
conditions, each with type, status, reason, message, and
`lastTransitionTime`; `status.observedGeneration`; and the values of the
CRD's printer columns, computed when the object is trimmed.  So every
kind has a `Ready` dot and the columns `kubectl get` prints, and the
rest of the spec of a kind `transect` has no knowledge of, which may
contain a credential, never reaches a store.  A printer column whose
path names an object shows `{…}`, never the object.  Every watch, whole
or trimmed, built-in or custom, marks the cluster changed only when an
event's object, trimmed as its store trims it, differs from the one
stored in something other than its `resourceVersion` and the labels and
annotations kind knowledge's `ignoreChanges` names for its kind, so an
operator that rewrites its status every few seconds, or a GitOps tool
that rewrites a `Namespace`'s ownership labels at each reconcile,
renders no view.  The store still keeps the object as the API gives it,
so a page drawn for another reason reads the newest value.  Each store's
trim decides what counts: managed fields are dropped everywhere, a
metadata store compares only metadata, every condition drops the
heartbeat times Kubernetes's kinds update on each check
(`lastHeartbeatTime`, `lastProbeTime`, `lastUpdateTime`), and a GitOps
tool's kinds keep only the fields their knowledge reads, so an applier's
history of reconciles never counts as a change.  A kind that becomes
whole keeps its trimmed store until the whole one has listed, and stays
whole until `transect` restarts.  Aggregated APIs are not CRDs and are
not watched.

A namespace lists its custom resources that are not workloads below its
workloads, one row per kind, each name linked to its page, and each kind
to `/namespaces/{ns}/{resource}`, the list of that kind's objects in the
namespace.  A custom resource of a known kind opens to its own page: the
object in the middle with its conditions, and one box per reference
around it, each with its label, its far end, and the field it came from.

A custom resource's health comes from the condition its knowledge
names: `True` is healthy, `False` fails with the condition's reason and
message, and `Unknown` or a missing condition is a warning.  A `False`
whose reason the knowledge lists in `healthy.calm` is at rest and draws
gray, so an idle resource never draws orange.  Without knowledge, a
`Ready` condition decides the same way, and with neither, health is the
roll-up of the built-in workloads under it.  Only the deciding
condition takes a color; every other condition on an object's page
draws gray, since nothing states whether `True` is good for it.

`transect`'s code encodes Kubernetes and nothing else: the built-in
kinds, their references, and the conventions every cluster shares.
Everything about a particular project, such as an operator's kinds, a
GitOps tool, an ingress controller, a database operator, or a log
store, comes from outside, as kind knowledge and the other resources
`transect` loads.  An operator that ships nothing still gets boxes,
health from a `Ready` condition, and guessed references.  `liken`'s
operators are the first to ship knowledge this way, and they are
examples, not special cases.

The alternative is to put the same facts in `x-` extensions inside the
CRD's OpenAPI schema.  That ties the knowledge to the CRD's own release
and to a vocabulary no other tool reads, so `transect` starts with the
separate resource.

### Intent in annotations

Intent is written on the objects it describes, as annotations, in the
same manifests that create them.  A change to a service and a change to
its description are then in one commit, and anyone or any agent editing
the manifest sees the description beside it.

| Annotation | On | Gives |
|---|---|---|
| `<prefix>/layer` | a `Namespace` | which layer it belongs to, overriding any layer's selector |
| `<prefix>/why` | any object | why it exists |
| `<prefix>/gotcha` | any object | what goes wrong with it |
| `<prefix>/provides` | any object | what it does for the things that depend on it, as a short noun phrase such as `office file conversion` |
| `<prefix>/depends-on` | any object | addresses or object references it depends on that nothing else reveals |
| `<prefix>/docs` | any object | where to read more |
| `<prefix>/authenticates-through` | any object, most often a `Secret` or `ConfigMap` | addresses or objects it signs in through, read like `depends-on` |
| `<prefix>/data-on` | a `CSIDriver` | `machines`: its volumes keep their data on the disks of the machines that mount them |
| `<prefix>/console` | a `CSIDriver` | a URL template of the console of what keeps its volumes, each placeholder a volume attribute of its PersistentVolumes or a parameter of its StorageClasses, such as `https://{dsm}:5001` |
| `<prefix>/host-access` | a workload | why it uses its machine's network, a host port, a host path, or a privileged container or an added capability, shown beside those on its access drawing |
| `<prefix>/signs-in` | a workload, or an `Ingress` for that route alone | how the workload signs its callers in itself, which the API does not show, such as `OAuth, in the app` |

The prefix is the same name as the API group of `transect`'s resources,
`transect.liken.sh/`.  It is fixed, not configurable, so a manifest
annotated for `transect` has the same meaning in any cluster that runs
it.  The name is one constant in the code.

A card shows the intent on its own object: the namespace card the
`Namespace`'s, a workload card its top owner's, and a pod page its
workload's.  A workload whose top owner has no `why` opens with its
kind knowledge's `summary`, or else with the
`org.opencontainers.image.description` label of its first image.

`depends-on` and `authenticates-through` are lists, split on commas and
newlines.  An entry with a scheme is a URL; an entry of the form
`<resource>/<name>` or `<namespace>/<resource>/<name>`, with a
`<resource>` the cluster serves, is an object; any other entry is a host
or `host:port`.  Addresses resolve through address resolution, so an
entry that names nothing in the cluster becomes a box outside it.  An
entry that is none of these shows on the card as text.  A list draws
edges only from a workload, from a `ConfigMap` a workload reads, or for
a `Secret` a workload reads, as below.  On a `Service`, a `Namespace`,
or any other object it draws nothing, and the overlay or `Intent`
resource that wrote it gets a condition that states this.

Sign-in that an application does itself is configured in its `Secret` or
`ConfigMap`, so `authenticates-through` reaches the application the way
`depends-on` does, as an `authenticates through` edge.  A separate
annotation keeps each value a plain list.

`depends-on` matters most on Secrets and ConfigMaps, because an
application's dependency on an identity provider or an outside API is
usually configured inside one.  The pod's spec names the Secrets and
ConfigMaps it reads, so a list on one of them becomes an edge from
each workload that reads it.  `transect` never reads `Secret` data, so
an annotation is its only source for where a `Secret`'s credentials
lead.

Reading a `Secret`'s annotations takes `list` and `watch` on Secrets,
and Kubernetes has no permission for metadata alone: an identity that
can list Secrets can read their data.  So `transect` reads a
`Secret`'s intent from the object that writes it, such as the custom
resource of a secrets operator, through its kind knowledge's
`writesSecret`.  It reads the Secrets' own annotations only with
`--secret-annotations`, which is off by default and meant for an
identity that may read Secrets anyway, such as a workstation's admin
kubeconfig.  Each edge read for a `Secret`'s readers states which way it
was read.

`provides` lets a cascade state what is lost instead of what is down:
when a workload with `provides: office file conversion` is lost, its
dependents' cards show "loses office file conversion."  When an
identity provider is lost, sign-in is lost with it, so an application
that authenticates through it shows "loses sign-in."

`data-on` is written by whoever ships the CSI driver, in the same
manifest as its `CSIDriver` object.  A volume of such a driver stores on
the machine its `PersistentVolume`'s node affinity names, as a local
volume does, and strands its pods when that machine is lost; with no
node affinity, it stores on whichever machine runs its pod.

### The Intent resource

For intent that should not, or cannot yet, be written in a manifest, a
namespaced `Intent` resource selects objects in its namespace and has
the same fields:

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

`authenticatesThrough` is a list like `dependsOn`, `signsIn` is
`signs-in`, and `hostAccess` is `host-access`.  An annotation on the
object takes precedence over an `Intent` resource for the same field.
When two Intents set one field on one object, both get a
condition, and the first by name is shown.

### Overlays

An overlay is a plain Kubernetes object of a kind `transect` watches,
such as an `apps/v1` `Deployment`, that has only `metadata`: a
namespace, a name, and annotations or labels.  `transect` merges its
annotations and labels onto the live object of that kind, namespace,
and name, as if the cluster's manifest contained them, and marks each
fact read from one with the overlay's file.  An annotation on the live
object takes precedence over an overlay's, and an overlay that matches
no live object gets a condition.

An overlay file is the patch a person would make to the real manifest,
so trying intent against a cluster is one file per object, and adopting
it is moving those annotations into the cluster's repository.
`transect` applies nothing to the cluster; a person does.

### Device knowledge

A DRA driver's devices are described by one cluster-scoped
`DeviceKnowledge` resource named for the driver, by the name a
`ResourceSlice`'s `spec.driver` gives it, as kind knowledge is named for
its CRD:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: DeviceKnowledge
metadata:
  name: sound.example.io
spec:
  title: monitorName
  facts:
    - {attribute: connectionType, label: through}
  references:
    - {attribute: monitorId, driver: screen.example.io, match: monitorId, label: speakers of}
    - {attribute: address, kind: Pairing, group: radio.example.io, match: status.address, label: pairs through}
```

| Field | Means |
|---|---|
| `title` | the attribute whose value titles a device, in place of its device name |
| `facts[]` | the attributes to show, each with a `label`; any other attribute is left out.  A `true` value shows the label alone, and a `false` value shows `whenFalse`, such as "not connected", or nothing without it |
| `references[]` | an attribute whose value names something else: a device of `driver`, or an object of `kind` and `group`, whose attribute or field `match` has the same value, with a `label` and an `edge`, `depends on` when left out |

A machine's page lists the hardware it publishes in a box per driver,
headed by the driver's name and how many devices it publishes and how
many a claim holds.  Each device has the same lines: its title, each
claim that holds it or "not held", its facts, and what its references
name.  A device is titled and described as its driver's knowledge
declares; a driver with none gets each device by its own name with every
attribute as a fact.  Held devices come first, and a driver's unheld
devices fold inside its box past six.  Devices of one driver on one
machine that share `resource.kubernetes.io/pciBusID`, a Kubernetes
standard attribute, draw as one piece of hardware.  The same title names
a device wherever it shows: a section view's device box, a board's
Refers to box, an object page such as an adapter's, and a namespace
card's Holds.  The card's Holds names each workload that holds a device,
with the machine once beside it, and each device on its own line under
it, linked to its line on the machine's page; a workload whose devices
are on several machines names the machine on each device's line instead.
A reference to a device prefers one on the same machine, and a reference
to an object draws as what that object represents on the map, as any
reference to an object does.  The far kind of an object reference is
watched whole.  In a section view, a device a workload holds draws a
line to each box in the right column that its references name, through a
lane past the column, which widens the view to fit it.

### Ingress controllers

An `IngressClass` names its controller by an identifier in
`spec.controller`, not by a workload, and an ingress controller often
ships no CRD for kind knowledge to describe.  A cluster-scoped
`IngressController` resource declares one:

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

| Field | Means |
|---|---|
| `controller` | the identifier an `IngressClass`'s `spec.controller` gives it |
| `image` | the image repository its workload runs, without a registry host, matched as `controller.image` is |
| `entry` | the PromQL for each backend's request rate, p95 latency, and 5xx rate, the label that names a backend in their answers, how the controller names a `Service`'s backend (`{namespace}`, `{service}`, and `{port}`), and the window the rates cover |
| `entryPoints` | the ports it serves an `Ingress` on: `annotation`, the `Ingress` annotation that names entry points, comma-separated; `default`, those an `Ingress` without it is served on; and `ports`, each with its `name`, its `port`, whether it serves the `Ingress`'s TLS (`tls`), and the entry point it redirects to (`redirectsTo`) |

An `Ingress`'s class names its `IngressClass`, and the class's
controller names the `IngressController` that declares it.  The
workload whose pods run that `IngressController`'s image serves the
`Ingress`, and the entry box and the `Ingress`'s own board show "served
by" it.  When two workloads run the image, the box is unlinked.  When
no `IngressController` declares the class's controller, the workload is
guessed from those a `LoadBalancer` `Service` sends web traffic to on
80 or 443: the one whose name contains the class's, or the only one.  A
guess ends in "?", and its title states what it was guessed from.  A
second `IngressController` for one controller gets a condition.
Without `entryPoints`, an `Ingress` is served on port 80, and on 443
when it has `spec.tls`, as the `Ingress` API documents.

Some distributions answer a `LoadBalancer` `Service` with a `DaemonSet`
of their own whose pods open the `Service`'s ports as host ports.  A
pod is such a listener when its `DaemonSet` has an owner reference to
the `Service`, or when a cluster-scoped `LoadBalancerController`
declares which label or annotation of a listener names the `Service`:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: LoadBalancerController
metadata:
  name: a-load-balancer
spec:
  listener:
    name: {label: lb.example.io/service-name}
    namespace: {label: lb.example.io/service-namespace}
```

A `LoadBalancerController` whose keys no pod has gets the
`Matched False` condition.

### Outside the cluster

A ground box is an address or a CSI driver, and neither is an object
that can have an annotation.  A cluster-scoped `Outside` resource names
one thing outside the cluster, lists the addresses and CSI drivers that
reach it, and has `why` and `gotcha`:

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
  url: https://nas.example.internal:5001
```

Every backend it lists draws as one ground box under its title, with
its volumes counted by type across all of them, and a `depends on` edge
to any of its addresses ends at the same box.  An identity provider or
a cloud API outside the cluster gets a box the same way.

Each ground box has a page, `/ground/{name}`, under its `Outside`'s
name, or for a backend no `Outside` names, under the address or driver
itself.  Every link to a ground box uses its `Outside`'s name, and
`/ground/` with an address or driver the `Outside` lists, or with its
title, draws the same page.  The page is a board: the thing in the
middle with its `why` and `gotcha`, the addresses and drivers that reach
it on the left, and on the right the claims it keeps by type, the
workloads whose volumes are on it, through a claim or an NFS export a
pod names by one of its addresses, and the objects whose edges of any
other kind end at it, such as `depends on` or `authenticates through`.
Both lists group by namespace and fold when long, and the summary
counts each workload once.  The page and every link to it read one
lookup, so a box links to its page only when the page exists.  The
box's title links to that page on the map's ground band, in the section
view, and wherever else it draws.  `url` is the thing's own console; a
`↗` beside the title opens it in a new tab.  Only a plain `http` or
`https` URL is a link, and a `url` of any other scheme shows as text on
the page.

An address in a form only a `Service`'s DNS name takes,
`name.namespace.svc`, `name.namespace.svc.<domain>`, or `name.namespace`
where the namespace contains a `Service`, is never a ground box.  It
reaches the workload behind that `Service`, or, when the cluster has no
such `Service`, the `Service` by name, which has no page and shows as
text.

A CSI driver's `console` annotation, written by whoever ships the
driver beside `data-on`, gives a ground box its console with no URL
written by hand: each of the driver's PersistentVolumes and
StorageClasses fills the template from its volume attributes or
parameters, and a value that contains what a host or a path would not
fills nothing.  When the box's drivers fill one URL, the title links
to it; when they fill several, such as one driver for two NASes, the
ground page lists each and the title links none.  An `Outside`'s `url`
overrides the template, and the same rule as `url` applies: only a plain
`http` or `https` URL is a link.  The CSI driver's own page lists
each URL the template fills to, as a link, with the template beside
them in gray.

### Access

The access lens reduces Roles, ClusterRoles, RoleBindings, and
ClusterRoleBindings, which `transect` already watches whole, the
ServiceAccounts' metadata, and the pods' specs to what each account
may do, and where.  It makes no `SubjectAccessReview`, needs no
permission beyond listing and watching those, and never reads a
`Secret`'s data.  The reduction runs as part of a model build, and a
model keeps the last one when a fingerprint of what it read, each
role's and binding's version, the ServiceAccounts, and what each pod
runs as and mounts, has not changed.

An account is a `ServiceAccount`, a group, or a user, as a binding's
subject names it.  A `User` named
`system:serviceaccount:{namespace}:{name}` is that `ServiceAccount`, and
a `ServiceAccount` is in the groups `system:serviceaccounts`,
`system:serviceaccounts:{namespace}`, and `system:authenticated`, so a
grant to a group reaches it.  Subject names are literal, as the API
server reads them.

Six capabilities are read.  This table is the spec the reduction's
tests check: a rule grants a capability when it allows one of the
verbs on one of the resources in its row, and nothing else does.

| Capability | The verbs and resources that grant it |
|---|---|
| read Secrets | `get`, `list`, or `watch` on `secrets` |
| exec into pods | `create` on `pods/exec` or `pods/attach`; `patch` or `update` on `pods`, whose new image runs as the pod's account, or on `pods/ephemeralcontainers`; `get` or `create` on `nodes/proxy`, the kubelet's API, which runs commands in any pod on its machine, through a `ClusterRoleBinding` |
| run pods | `create` on `pods`, or on `jobs` or `cronjobs` in `batch` |
| change workloads | `create`, `patch`, or `update` on `deployments`, `statefulsets`, `daemonsets`, or `replicasets` in `apps`, or on `replicationcontrollers`; `patch` or `update` on `jobs` or `cronjobs` in `batch` |
| grant roles | `bind` or `escalate` on `roles` or `clusterroles` |
| change what a GitOps tool applies | `create`, `patch`, or `update` on a kind whose kind knowledge declares `applies` or `repository` |

A request on a cluster-scoped resource has no namespace, so a
rule on `nodes/proxy`, `users`, `groups`, or `clusterrolebindings`
grants only through a `ClusterRoleBinding`; through a `RoleBinding` it
grants nothing.

Exec takes `create` alone.  SPDY opens exec with a POST, authorized
as `create`; a websocket opens it with a GET, and from Kubernetes 1.35
the API server also checks `create` on that upgrade
(AuthorizePodWebsocketUpgradeCreatePermission, on by default).  The
lens reads neither the server's version nor its feature gates, and the
card states that on an older server `get` also opens exec.

The lens reads a rule with `*` in any position, and reads subresources
as the API server's matcher does: `*`, the exact `pods/exec`, and
`*/exec`, while `pods/*` is a resource of that literal name and grants
nothing.  A rule limited by `resourceNames` grants by name.  A
`ClusterRoleBinding` grants in every namespace and a `RoleBinding` in
its own.

A capability follows from a grant, without a binding that grants it,
by these steps, the second half of the spec:

| An account that may | may also |
|---|---|
| run pods or change workloads in a namespace | run a pod as any `ServiceAccount` there, and do what it may; and read any `Secret` there, which a pod it starts may mount |
| exec into a namespace's pods | read the Secrets they mount, by name; and act as the `ServiceAccount` of each pod that mounts its token, which a pod does unless `automountServiceAccountToken` turns it off; a projected token counts only when its `audience` is unset or the API server's own, `https://kubernetes.default.svc` with or without `.cluster.local`, since the API server refuses a token for another audience |
| change an object that applies, where it may, or by its name | do what the account the object applies as may: the `ServiceAccount` its `applies.account` field names, or the field's default, or else its controller's account, the account RBAC lets write the applier kind's status, as controller attribution reads it, or, when no role names a writer, the account of the pods that run the image its kind's `controller` names; and when its kind's knowledge gives `applies.account`, do what every `ServiceAccount` in the object's namespace may, since it may set the field to any of them, and what the object applies as when the field is empty |
| change a source an applier fetches through, where it may, or by its name | do what each applier that fetches through it applies as, in any namespace |
| `bind` on `roles` and `create` on `rolebindings` in a namespace | bind each `Role` there that the `bind` names to itself, and do what it grants there |
| `bind` on `clusterroles` and `create` on `rolebindings` in a namespace | bind each `ClusterRole` the `bind` names to itself in a `RoleBinding`, and do what it grants there; in every namespace when both hold in every namespace |
| `bind` on `clusterroles` and `create` on `clusterrolebindings`, through ClusterRoleBindings | bind each `ClusterRole` the `bind` names to itself in every namespace, and do what it grants, cluster-scoped rules too |
| `escalate` and `update` or `patch` on the kind of a role a binding binds it to, where both hold | rewrite that role to grant anything, and do all six where the binding holds; a `ClusterRole` is cluster-scoped, so only grants through ClusterRoleBindings rewrite one |
| `create` on `serviceaccounts/token` in a namespace | sign in as each `ServiceAccount` there it may mint a token for, and do what it may |
| `impersonate` on `serviceaccounts` in a namespace | act as each `ServiceAccount` there it may impersonate |
| `impersonate` on `users`, through a `ClusterRoleBinding` | act as each `User` a binding names that it may impersonate |
| `impersonate` on `groups`, through a `ClusterRoleBinding`, beside `impersonate` on users or ServiceAccounts | act as each `Group` a binding names that it may impersonate; and with `Group` `system:masters`, which the authorizer lets do anything, bound or not, do all six in every namespace |

The steps run to a fixed point, a hop further each pass, so each
capability keeps the first hop of a shortest chain, and the page names
that hop.  A rule that names objects grants no `create` of the kind
itself, since a create has no name; a subresource's create, such as a
token, names its object.  `escalate` alone, or `bind` alone, takes no
step.  The card names the routes the lens does not follow: a certificate
request it may approve and sign, a `PersistentVolume` it may create, an
admission webhook it may write, and a `Secret` containing a
`ServiceAccount`'s token, whose type a `Secret`'s metadata does not
show.  A grant limited by name takes no step, except the token,
impersonation, bind, rewrite, applier, and source steps, and exec, which
reach only the objects it names: exec by a pod's name follows from that
pod alone, and `nodes/proxy` by a `Node`'s name from the pods on that
machine.

`/access/accounts` draws a row per account that has any of the six, in
three runs: the accounts bound in every namespace to a role with every
verb on every resource, the accounts from which that follows, by the
first hop they take, and every other account.  An account whose every
binding Kubernetes ships, labelled `kubernetes.io/bootstrapping:
rbac-defaults`, folds into one line of Kubernetes' own components.  A
cell is a filled block where a binding grants the capability and an
outlined one where it follows, full for every namespace and short for
some, named beside it; its title names the binding and role, or the
step.  The middle run's heading names the step its rows take, the
account they may run as or the applier they may change, and the role
that account is bound to, so a row in it names a step only where its own
differs.  A row's "through" cell is one line, the role and then the step
after an arrow, cut with an ellipsis and whole on hover, so each row is
one slot high.  A `ServiceAccount`'s `why` shows as the `intent` mark
beside its name, with the sentence on hover, so intent may be written on
a `ServiceAccount`, and an overlay may supply it.

`/access`, Ways in, draws a row per host or address, and port, where a
request from outside the cluster enters it.  An `Ingress` gives a row
per rule host and port its controller serves it on, and the Ingresses
that route one host and port share a row.  A host's row reaches the
workloads behind its own rules' paths, and the default backend unless a
path of `/` takes every request first; a default backend beside host
rules also gives an "any host" row.  An `Ingress` whose class no
workload in the cluster is known to serve is no way in, and a line under
the table names it.  A way in is an address and a port, so a `Service`
gives a row for each address it answers on, with the workloads it
selects: a `LoadBalancer` `Service` its ports on its listeners' machines
or the addresses its status gives; a `LoadBalancer` or `NodePort`
`Service` its node ports on every machine's address, a row of their own,
unless `allocateLoadBalancerNodePorts: false` left them unset; and a
`Service` of any type its ports on its `externalIPs`.  A pod with a host
port, unless its `hostIP` is a loopback address, or on its machine's
network, gives a row per workload, counted over the machines its pods
run on, and the row names the workload that listens where a `Service`'s
row names its `Service`.  A row's ports stay whole: a list longer than
the port column wraps between ports into lines of even length, flush
right.  Listeners are their `Service`'s way in, and the first address
the ingress controller's own `Service` answers on, its load balancer's
or else its node ports, is named in a line under the table with the
count of hosts that enter through it; its other addresses are rows that
reach the controller.  An `Ingress` that routes only to what the other
routes sign in through is the sign-in's own callback, named in a line
and not a row.  A row on one machine's address names the machine,
"node-a's address", and the machine's name is a link of its own to the
machine's page, which has no access page, beside the row's link to the
workload's access tab; a namespace's ports matrix names and links it the
same way.

Each row has three gates, each a dot: filled when the cluster's objects
put the gate on every route or pod the row represents, half when on
some, and open when on none.  TLS is filled where the port serves TLS
and the `Ingress`'s `spec.tls` names the host.  Sign-in is filled where
an object an annotation on the `Ingress` names draws an `authenticates
through` edge, and the dot names it.

A gate the API does not show is declared: a ring around a dot, with the
`intent` mark of the sentence that declares it beside it and the
sentence on hover.  Sign-in is declared where the route has `signs-in`,
or else every workload the host's route reaches does, and is half where
only some of them do; an object that puts sign-in on the route takes
precedence over the sentence.  TLS is declared on a port whose entry
point `redirectsTo` one that serves TLS, when the route's `spec.tls`
names the host, titled with the `IngressController` that declares the
redirect.  A run's heading states a declared gate in words: "sign-in the
workload declares", "a redirect to TLS the ingress controller declares".
The `NetworkPolicy` gate is judged on each of the row's ports, at the
port of the pods behind it: a policy that selects the pods limits who
may call them there unless one of its rules admits any caller on that
port, which a rule with no `from` does, and so does an `ipBlock` of
`0.0.0.0/0` or `::/0` with no `except`.  The dot is filled when a policy
limits who may call every pod the row reaches on every port, open when
no policy selects them, when they are on their machine's network, or
when a rule admits any caller on every port, titled "NetworkPolicy
{name} admits any caller on {port}", and half otherwise.  A host's
filled dot states whether the policy admits the ingress controller the
host enters through.  A workload's own dot, beside its callers on its
access pages, reads its rules the same way over all of its ports.  Hosts
come first, in runs by their gates, whose headings state the gates in
words; then `LoadBalancer` Services, Services' external IPs, node
ports, host ports, and pods on their machine's network.  A line names
the namespaces that contain pods and no `NetworkPolicy` that limits
calls in, and another the custom kinds named `…Route` that have objects
the lens does not read.

These are the rules the ways-in tests check:

| What | Rule |
|---|---|
| a way in | an address and a port where a request from outside the cluster enters it |
| an `Ingress` host | a row per host and port its controller serves, reaching its own rules' paths, and the default backend unless a path of `/` takes every request first |
| a default backend beside host rules | an "any host" row |
| an `Ingress` whose class no workload serves | a line under the table, not a row |
| a `LoadBalancer` `Service` | a row for its ports on its load balancer's addresses, and a row for its node ports on every machine's address |
| a `NodePort` `Service` | a row for its node ports on every machine's address |
| `externalIPs`, on a `Service` of any type | a row for its ports on those addresses |
| the ingress controller's `Service` | its first address is the line under the table; its others are rows |
| a host port | a row per workload, unless its `hostIP` is a loopback address |
| TLS | filled where the port serves TLS and `spec.tls` names the host; declared where its entry point redirects to one that does |
| sign-in | filled where an object an annotation names signs the route in; declared where the route, or every workload it reaches, writes `signs-in`; half where only some routes or workloads have either |
| `NetworkPolicy` | filled where a policy selects every pod the row reaches and no rule admits any caller on its ports; open where none selects them, they are on their machine's network, or a rule admits any caller on every port; half otherwise |

A `ServiceAccount`'s page, at its usual URL, is its access drawing, with
its card beside it and no tab row: left to right, the workloads whose
pods run as it, each with its pods; the account, with its `why`; each
role it is bound to, with where the binding applies, and the binding on
hover; what each role grants; and what follows.  A grant that is one of
the six is a box with its filled block and the capability's name, with
the verbs and kinds that grant it, and "by name" with the names when a
rule limits it to them.  Every other rule is a box by a template of its
verbs and kinds, every subresource named, but a `status` beside its kind
in a rule that only reads: "read" for `get`, `list`, and `watch`, the
verb for one that changes, or "write" with its verbs for several, and
"every verb, every kind" for a role that allows everything.  Roles
that grant the same box share it.  What follows is outlined: the
Secrets a pod it starts may mount, "run as any account" where it may
run pods or change workloads, and each account a step goes through, from
the grant the step starts at, with, a box below each, how that account
has what follows, ending at the role the last account is bound to, its
outlined blocks naming what follows.  When a step reaches an account
bound to every verb in every namespace, through an applier it may change
or otherwise, the drawing draws that one chain, since all that follows
follows through it.  With nothing following, a dashed box shows "nothing
follows" and which of the six it may not do anywhere.  Under the
drawing, "Who may run as it" names the workloads that run as it and
counts the other accounts that may run pods or change workloads in its
namespace, which may run as it.

A group or a user, which no object represents, has the same drawing at
`/access/accounts/groups/{name}` or `/access/accounts/users/{name}`,
with the lens's card.  Its first column states who is in it where the
API defines it: `system:serviceaccounts:{namespace}` is that namespace's
ServiceAccounts, `system:serviceaccounts` every `ServiceAccount`, and
`system:authenticated` every account that signs in; for any other group,
and for a user, it states that the API does not list them.  A group's or
a user's name links to that page on every page, and search finds groups
and users by name, and a workload's access tab as one of its lenses.

The ways in are read as each model is built, so the first access page
after a change does not wait for them.

A namespace's access tab, `/namespaces/{ns}/access`, beside its
overview in the namespace's lens tabs, has two matrices and a line.
"What may reach each port here" draws each workload that serves a port
with the ports its pods declare, a row per way in beside it with the
way's three gates, or one row saying "no way in", then the
NetworkPolicies that limit its calls in, by name, or that none applies
to a pod on its machine's network, and a dot for whether one limits its
calls out.  "What each account may do here" draws a row per account
that may do one of the six in the namespace, each name a link, with a
full block where it may, filled where a binding there grants it and
outlined where it follows, "by name" beside a block whose every grant
names its objects, and the role or the step it goes by; accounts with
the same marks are adjacent with no rule between them, and Kubernetes'
own components fold into a line, as on the accounts matrix.  Under it,
one line states the namespace's Pod Security `enforce` label and what
it lets a pod there do.  The namespace's card is beside them.

The pages of the access lens are `/access` and every page under it, a
namespace's and a workload's access tab, and a `ServiceAccount`'s page.
On each of them, every link to an object with an access page opens that
page: a namespace opens its access tab, a workload that serves a port
its access tab, a custom resource that runs pods among them, and an
account its drawing.  Any other object, such as a pod, a `Role`, or a
`NetworkPolicy`, opens its usual page.  The rule applies in the
drawings, the matrices, the lists, the card, and the switcher row, so a
row of Ways in opens the access of the workload it reaches, a row of
either accounts matrix opens the account, the boxes of a workload's "Who
may change it" open the accounts, or the namespace's access for a box
that represents several, and the switcher row's namespace and its chips
move between siblings without leaving the lens.  Two parts of a page
lead out of the lens and keep their usual links: the crumb, whose
ancestors open their usual pages, and the lens tabs, which open the
object's other lenses.  A page names each object by its usual page, and
the render rewrites every link except those in the two parts to the
lens's page in one pass, `carry_access` in
`crates/transect/src/web/carry.rs`, so a card or a box added later
follows the rule with no code of its own.  A crawl of the fixture checks
every link on every page of the lens.

A workload serves a port when its pods declare one or run on their
machine's network, or a `Service` selects it; such a workload has an
access tab, `…/access`, whose page draws two drawings in the section
view's boxes and line paths, with the workload's card beside them,
which drops below them under 1400px.  "What may reach it, and what it
may reach" reads left to right.  On the left are its rows of Ways in,
each drawn through the ingress controller and its `Service`, or straight
to the port, with its TLS and sign-in gates as dots on the line, and a
dashed "no way in" box when it has none; and under them the callers
inside the cluster, a group per rule that admits them, dashed, with the
rule's policy named before its gate: the namespaces it admits whole,
the workloads apart from those, "the machines' addresses" for workloads
on their machine's network that an address block admits, and address
blocks; or one "any pod" box when no policy selects its pods or they
are on their machine's network.  Each line ends at a port box on the
workload's left edge, or at its edge for any port, and no two lines
cross: the groups whose port a way in or a `Service` also reaches come
first, under the lines from above that share their port, then the
groups for any port, then the rest, and each port is at the height
of its first line, so a port's lines arrive in the order they leave
and nest in the lanes beside the workload.  On the right are
the `ServiceAccount` it runs as, with what its bindings grant; what it
holds: claims, device claims, the Secrets its pod spec names, by name,
host paths, its machine's network and processes, and privileged or
added capabilities, the first of those with `host-access` beside it;
and what it may call: the isolated workloads that admit it, and the
policies that limit its calls out, or "any address" as a dashed line
that runs off the drawing.  The pod trim keeps a container's
`privileged` and the capabilities it adds for this.

"Who may change it" draws the accounts a binding grants `patch`,
`update`, or `delete` on its kind in its namespace, from an index of who
may change each kind that the reduction builds once, with exec into its
pods on the same line; a custom kind's controller; the GitOps objects it
reconciles from, with the accounts that may change what a GitOps tool
applies there; the accounts bound to a role with every verb, in one box;
and one outlined box for the accounts that may run pods or change
workloads in the namespace of an account that may change it.  Accounts
with the same lines share a box, and an account with a `why` has its
own.  The accounts that may patch a GitOps object are grouped at the
top, level with it, so a line into it passes no line that runs on to the
workload; of those that also change the workload, the first is at the
top of the run with its line over the applier, and the rest at the
bottom with theirs under it.  A third such account is closed in between
two lines into the applier and crosses one; every other line is clear.
Otherwise no two lines cross on any access drawing, the workload's two
and an account's, and a test reads every drawing on the fixture's pages
for a crossing.

### How the resources name their fields

Every resource `transect` reads follows the same conventions, so a
field has the same meaning in each:

| Convention | Means | For example |
|---|---|---|
| a path alone | a field that reads one place in an object is that path, a bare string, and one that may read several, tried in order, is a list of them | `node: metadata.name`, `directory: spec.path`, `source: [spec.sourceRef, spec.chartRef]` |
| a path among other fields | an entry that gives more than where to read names its place `path`, or `paths` for several, or `attribute` on a device | `references: [{path: spec.upstream, address: true}]`, `revision: {paths: [...], at: ...}` |
| `url` | the plain `http` or `https` address of what the resource describes, which `transect` reaches or links: a store, an Alertmanager, a console | `MetricsSource.spec.url`, `Outside.spec.url` |
| a template | a string whose `{placeholders}` `transect` fills, named for what it fills to | `repository: ['{spec.url}']`, a `Forge`'s `tree` |
| `*Labels` | a list of label names, tried in order | `nodeLabels`, `instanceLabels`, `scrapeLabels` |
| `kind` and `group` | a kind, by its name and its API group, the group empty or left out for the core group | `{kind: Namespace}`, `{kind: Deployment, group: apps}` |
| `formats` | the templates a value is matched against, tried in order until one matches | `revision.formats`, `applies.name.formats` |

A resource that loads but names something no object here has gets a
`Matched False` condition on `/resources`, so a typo never shows as
"loaded" and nothing more: kind knowledge whose `references`,
`writesSecret`, `primary`, or GitOps fields no object of its kind
has, whose `node` names no `Node`, or whose applier mark no object
has, and whose `ignoreChanges` names a kind the cluster does not
serve; an `IngressController` whose controller no `IngressClass` names,
whose image no running pod runs, or whose entry queries answer no
series with its backend label; a `Forge` on whose hosts no repository
`transect` reads is; a `MetricsSource` whose Services are not in the
cluster, or whose queries' series name no `Node`, pod, or claim; and
`DeviceKnowledge` for a driver no `ResourceSlice` publishes, or that
names an attribute none of its devices has.  A kind with no objects
yet gets none of these.  The section the resource would fill keeps a
gray line: a card whose workload no GitOps applier marks states that,
and a machine page whose `Node` no object of a describing kind names
states that in its Underneath box.

A section that one of these resources would fill names, when it is
empty, which resource would fill it.  When a resource of that kind is
there but did not load, the section states that instead, as "the
MetricsSource metrics did not load; see /resources", linked, so a
reader who wrote the resource goes to the condition that states what is
wrong with it and is not told to write it again.

### Deterministic at runtime

`transect` makes no calls to a language model.  Every sentence it shows
is a template filled from the graph and the signals, or text stored in
the cluster: an annotation, kind knowledge, a log pattern's meaning, or
a comment in the repository.  Every screen works with none of that text,
from names, counts, and template findings alone.  The stored text
improves a screen, and no screen depends on it.

Language models belong in authoring.  An agent drafts annotations and
kind knowledge as commits to the manifests, and a person reviews them
there like any other change.

## Log sources

The default log source is the Kubernetes API: the last lines of one
container's log, with no search.  A cluster with a log store declares
it with one cluster-scoped `LogSource` resource:

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
  levels:
    error: [err, fatal, panic, crit]
    warning: [warn]
    plain: [info, debug, trace, notice]
  queries:
    tail: '{fields.namespace}:"{namespace}" {fields.pod}:"{pod}" | sort by ({fields.time}) desc | limit {limit}'
    search: '_time:{window} {pattern} | stats by ({fields.namespace}, {fields.pod}) count() matches, max({fields.time}) last | limit {limit}'
```

- **How to reach it.**  `url` takes precedence over `service`, and
  `--log-source-url` takes precedence over both, so one resource serves
  a workstation through a port-forward and the cluster through the
  `Service`'s DNS name.  `transect` sends the store no credentials yet;
  in a cluster the credential will be a `Secret` mounted as a file that
  the `LogSource` names by path, so `transect` needs no access to
  Secrets.
- **Its language.**  `language` selects how a value is quoted into a
  query and how the answer is read.  LogsQL is the only language so
  far.
- **What its fields mean.**  `fields` names the store's field for the
  namespace, the pod, the container, the machine, the time, the level,
  and the message; the machine and the level may be left out.
  `fields.machine` identifies which lines are a machine's own log, such
  as its kubelet's or its kernel's, though a pod ships them: those
  whose `field` has one of `values`, such as the name of the
  `DaemonSet` that reads each machine's journal, by `unit`, the field
  that names which of the machine's logs a line is.  It needs
  `fields.node`.
- **What its levels mean.**  `levels` lists the starts of the level
  field's values that name an `error`, a `warning`, or a `plain` line,
  in any case, and comes with `fields.level`: one without the other
  does not validate.  A value no list names, such as OpenTelemetry's
  `Unspecified`, leaves the line to the guess from its text, and with
  no `error` list the error lines are not counted.  An empty entry
  would start every value, so it does not validate.  A level is matched
  by the start of its text only, so a numeric level such as
  OpenTelemetry's `severity_number` cannot be read as a range.
- **The queries `transect` fills in.**  `tail` reads one pod's lines,
  `search` counts one pattern's matches across the cluster, and
  `lines`, when declared, reads the newest matching lines, which a
  finding shows a few of.  A
  placeholder `{fields.pod}` is the store's name for a field, and
  `{pod}` is a value, quoted for the language, so a name with a quote
  in it cannot change the query.

The pod page's last lines come from the store's tail, one container at a
time, so a restarted container's earlier run shows too, and each line
takes its level from the `level` field, as `levels` reads it.  When the
store gives nothing, the API's tail shows instead, and the page states
why.  A second `LogSource` gets a condition.

Log patterns are resources too:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: LogPattern
metadata:
  name: env-file
spec:
  match: '"GET /.env"'
  severity: worth-knowing
  meaning: a scanner looking for leaked secrets
  window: 1h
```

`match` is a filter in the source's language, `severity` is `error`,
`warning`, or `worth-knowing`, `meaning` is the stored text, and
`window` is `1h` when left out.  On a pod's page, each pattern runs as
the tail query with the pattern added, and the lines that come back
have its meaning as a tag.  Every five minutes, and when the source or
the patterns change, each pattern is searched across the cluster over
its window, with a 15-second timeout.  An `error` or `warning` match is
a reason on each namespace it matched in, and takes the health colors;
a `worth-knowing` match is a finding with a line per namespace, and
takes none.  The same round counts each pod's error lines over the
last hour: those whose `level` field starts with a value of
`levels.error`.  A search the store refuses is a
condition on the pattern.

## Metrics sources

`transect` asks Prometheus for what Kubernetes does not report, and
Alertmanager for what is firing.  Which exporter writes which metric,
which label a scrape configuration puts a `Node`'s name in, and how
alert rules label their alerts differ from one cluster to the next, so
every query and every convention comes from one cluster-scoped
`MetricsSource`, not from the code:

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: MetricsSource
metadata:
  name: metrics
spec:
  service: {namespace: monitoring, name: prometheus, port: 9090}
  url: http://localhost:9090
  nodeLabels: [node, instance]
  queries:
    machineCpu: {query: 'max by (node, instance) (rate(machine_cpu_seconds_total[2m]))'}
    machineMemory: {query: 'max by (node, instance) (machine_memory_used_bytes)'}
    containerCpu:
      query: 'max by (namespace, pod, container) (rate(container_cpu_seconds_total[2m]))'
      labels: {namespace: namespace, pod: pod, container: container}
    containerMemory:
      query: 'max by (namespace, pod, container) (container_memory_used_bytes)'
      labels: {namespace: namespace, pod: pod, container: container}
    volumeUsed:
      query: 'max by (namespace, persistentvolumeclaim) (volume_used_bytes)'
      labels: {namespace: namespace, claim: persistentvolumeclaim}
  alerts:
    service: {namespace: monitoring, name: alertmanager, port: 9093}
    url: http://localhost:9093
    severity: {label: severity, failing: [critical, error], notice: [info], hidden: [none]}
    namespace: namespace
    objects:
      - {label: pod, kind: Pod}
      - {label: deployment, group: apps, kind: Deployment}
      - {label: persistentvolumeclaim, kind: PersistentVolumeClaim}
    container: container
    nodeLabels: [node]
    instanceLabels: [instance]
    scrapeLabels: [pod, container]
    exportedPrefix: exported_
    annotations: {summary: summary, description: description}
```

- **How to reach them.**  A `MetricsSource` is reached as a `LogSource`
  is: `url` takes precedence over `service`, and `--prometheus` takes
  precedence over both; the `alerts` section's address works the same
  way, with `--alertmanager`.  The Prometheus HTTP API and the
  Alertmanager v2 API are protocols, and stay in the code.  The pods
  the `alerts.service` `Service` selects are the cluster's alerting
  path, read from Kubernetes alone: a loss that takes them leads with
  it, and without `alerts.service` the loss view states in a gray line
  that the alerting path is unknown.
- **Usage.**  `machineCpu` and `machineMemory` answer cores and bytes
  per machine, filed under the `Node` whose name any of `nodeLabels`
  contains, with a scrape address's port left off.
  `containerCpu` and `containerMemory` answer per container, named by
  their `labels`, and a pod is the sum of its containers.  The
  machines band, the machine pages, and the pod bars draw from them.
- **Volumes.**  `volumeUsed` answers the bytes used on each claim's
  volume, and `labels` names the label in each answer that contains the
  claim's namespace and name; of two answers for one claim the larger
  counts.
- **Alerts.**  `severity.label` gives an alert's severity: a value in
  `failing` colors what it is about as failing, `notice` notes it,
  `hidden` leaves it off the map, and any other value, or none, is a
  warning.  `namespace`, `objects` (most specific first), `container`,
  and `nodeLabels` name what an alert is about; `instanceLabels` name
  a machine by a scrape address when nothing else does.  When the
  `exportedPrefix` form of the namespace label is present, an exporter
  reported the alert about another namespace, so the plain
  `scrapeLabels` are the exporter's and the prefixed ones are read.
  `annotations` names the summary and the description.

Each query may be left out.  What it would show is then missing, and
the page states what would show it: with no `MetricsSource`, `transect`
asks nothing, the machines band shows "no usage: a MetricsSource's
machineCpu and machineMemory queries would show it", and "Now" states
that an `alerts` section would list the firing alerts.  `transect` runs
the usage queries on `--usage-interval`, `volumeUsed` every minute, and
reads the alerts every 30 seconds, and each also when the resources
change.  A second `MetricsSource` gets a condition.

## Running from a workstation

`transect` reads only the Kubernetes API and the stores its resources
name, so the same program runs on a workstation, pointed at a cluster
from outside:

```bash
transect --kubeconfig ~/.kube/config --prometheus http://localhost:9090
```

It uses the kubeconfig's credentials, so it sees what that user sees.
Prometheus can be any URL that reaches it, such as a `kubectl
port-forward`, and `--prometheus` takes precedence over the address the
`MetricsSource` gives.

`transect`'s own resources (`Layers`, `KindKnowledge`, `Intent`,
`DeviceKnowledge`, `Outside`, `RegistryMirror`, `Forge`,
`IngressController`, `LogSource`, `LogPattern`, and `MetricsSource`)
and
overlays load from a directory of YAML files:

```bash
transect --kubeconfig ~/.kube/config --prometheus http://localhost:9090 \
  --resources ./local/resources/
```

So the resources' shapes can change as fast as the code that reads them,
with no CRD to install, until they settle.  One loader reads every
document under the directory, sorts it by `kind`, and reads the
directory again when a file under it changes.  A document of an unknown
kind, or one that does not validate, does not stop `transect`: it is
left out, with a condition that states why.  Each resource's conditions
have the shape of a CRD's `status.conditions` and show on the
`/resources` page, beside those that need the live cluster, such as an
overlay that names no object or a selector that matches nothing.  Once
the resources are CRDs, `transect` writes the same conditions to their
status.

This is the main development loop.  Change the server or the page, run
it against a real cluster, and look, with no image to build and no
deploy.  A debug build reads its styles from the source tree on each
request, so a style edit shows on the next reload, and a change to a
resource file pushes every open page its new rendering.

A debug build's `--fixture` serves the small cluster the tests render,
from `crates/transect/src/web/fixture/`, and reads nothing from the
network, so work on a template or a layout loads no API server.  The
fixture has an example of every page type, and a test serves each of
them through the router.

## What it is not

- **A traffic viewer.**  Kiali and Hubble draw request flow inside a
  mesh or a CNI.  `transect` draws what depends on what, and traffic is
  one kind of edge.
- **A dashboard.**  Grafana already shows time series.  `transect` links
  to the dashboards that exist and draws none of its own.
- **A history.**  `transect` shows the cluster as it is now.
