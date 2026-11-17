# 02: Slice 1, the elevations and the edges

Plan 01 drew the cluster elevation.  This plan finishes slice 1: a
namespace selected on the cluster map, the namespace, workload, and pod
elevations each at its own URL, the edges the Kubernetes API gives, and
the cards.  Like plan 01, it is built in stages, and each stage ends in
something to reload and look at against a real cluster.

The comps for this plan are boards 2 (one namespace selected), 4 (a
namespace opened in place), 5 (a namespace as a matrix), 7 (a
workload), and 8 (a pod).  Board 3 (a machine lost) is skimmed here and
left for a later plan.

## Decisions

- **URLs.**  Zoom is navigation, so every elevation and every selection
  is a URL a person can paste:

  | View | URL |
  |---|---|
  | the cluster | `/` |
  | the cluster, one namespace selected | `/?select=<namespace>` |
  | a namespace | `/namespaces/<namespace>` |
  | a workload | `/namespaces/<namespace>/<resource>/<name>` |
  | a pod | `/namespaces/<namespace>/pods/<name>` |

  `<resource>` is kubectl's form: `deployments`, `statefulsets`,
  `daemonsets`, and `cronjobs` for the built-in kinds, and
  `<plural>.<group>` for a custom kind, such as
  `caches.cache.example.io`.  Clicking a parcel swaps the page with
  `hx-get` and `hx-push-url`, so the back button and a pasted link both
  work.

- **The layer crumb.**  The design lists the layer as elevation
  2, but no comp draws a page for one: the crumb reads
  `cluster / <layer> / <namespace>`, and on the namespace page the other
  layers collapse to one line each.  No layer page in
  this plan.  The layer crumb links to the cluster map scrolled to that
  band (`/#<layer>`).

- **Select, then open.**  Board 2 selects a namespace in place;
  board 4 opens it.  One click on a parcel selects it,
  a click on the selected parcel or on its card's name opens it, and
  Escape or a click on empty band clears the selection.  Selection
  never moves a parcel: the card goes in the sidebar, which is already
  there, and the tags a selection adds sit inside parcels whose widths
  come from their weight.

- **One stream per view.**  Plan 01 renders one block for every
  browser, because every browser looked at the same thing.  Now each
  URL renders differently, so the event stream is keyed by view: a
  page connects to `/events/<its own path and query>`, and the server
  keeps one render task per view that has a browser connected.  Two
  browsers on the same view share one rendering, as before.  A task
  ends when its last browser leaves, which `watch::Sender::closed()`
  reports.  Every change wakes every open view; at this cluster's size
  a render is cheap enough that per-view change filtering waits until
  it is not.

- **Edges are HTML, and no lines are drawn.**  The design's open
  question asks whether the server renders the map as SVG or hands it
  to a graph library in the page.  Neither, for this
  plan.  The comps draw no lines.  An edge shows at its far end, as a
  tag or a count on the box it reaches when the box at its near end is
  selected: "may call" and "manages" on parcels, "2 pods here" on a
  machine strip, "6 NFS · 2 iSCSI" on a ground box, arrows between the
  columns of a workload's front door, Service, pods, and volumes.  Lines
  over a band of wrapped parcels would need each parcel's position,
  which only the browser knows after layout; a graph library would move
  the layout into the page and lose the fixed positions the design
  depends on.  Revisit when the cut (board 6) is built, since that
  board is the first to draw a path from top to bottom.

- **The edge graph is its own domain.**  A new `edges/` module holds
  `Edge { kind, from, to, guessed }` and the queries the views ask of
  it: the edges that touch a namespace, a workload, or a pod, in either
  direction, and the cycles among them.  An endpoint is an object (its
  kind, namespace, and name), a machine, or a box outside the cluster.
  Each edge kind has its own domain module that turns the cluster's
  stores into edges, and the graph is built from those on each render.
  Building it fresh each time keeps every source a pure function with
  table-driven tests; it becomes incremental only if a render gets
  slow.

- **Edge kinds, in the order this plan builds them.**  Each is marked
  by whether the API alone gives it:

  | Kind | From the API alone? | Module | Stage |
  |---|---|---|---|
  | runs on | yes: `spec.nodeName` | `machines/` | 3 |
  | managed by, owner references | yes: `ownerReferences`, walked up to the top owner | `ownership/` | 4 |
  | stores on | yes: PVC → PV → StorageClass, and the PV's NFS server or CSI driver | `storage/` | 6 |
  | depends on | yes, by address resolution of each container's literal environment values against Service names | `addresses/` | 11 |
  | reconciles from | yes, when Flux is installed: its labels and the Kustomization's `spec.sourceRef` | `flux/` | 12 |
  | managed by, the controller | yes, with guesses: RBAC on `<kind>/status` and the status writer in `managedFields` | `ownership/` | 13 |
  | allowed to call | yes: NetworkPolicies, evaluated to namespace pairs | `policy/` | 15 |
  | holds | yes, when the cluster serves `resource.k8s.io/v1`: ResourceClaim → ResourceSlice | `devices/` | 16 |
  | authenticates through | no: it is an ingress controller's own custom kind | slice 3 | |
  | calls | no: it needs the flow agent | slice 2 | |

- **Depends on.**  The edge table has no kind for an address a
  workload is configured with, such as an environment variable that
  names a database's Service.  Call it `depends on`,
  the same kind the `<prefix>/depends-on` annotation will add in slice
  3, so a card lists both in one place.

- **Authenticates through.**  Forward auth is configured on an
  ingress controller's own resource, such as Traefik's Middleware, named
  from an annotation on the Ingress.  Sign-in that an application does
  itself, with its OIDC settings in a Secret, does not show in the API
  at all; the comps' "Sign-in" box on board 7 is that case.
  Reading an ingress controller's middleware would put one
  controller's specifics into transect, which kind knowledge exists to
  keep out.  So this plan skips the kind: forward auth arrives with
  kind knowledge that describes a middleware's auth address, and
  in-application sign-in with the `depends-on` annotation, both in
  slice 3.

- **New watches.**  `cluster/` gains reflectors for Service,
  EndpointSlice, Ingress, PersistentVolumeClaim, PersistentVolume,
  StorageClass, PodDisruptionBudget, NetworkPolicy, ClusterRole,
  ClusterRoleBinding, Role, RoleBinding, and Job.  Kinds a cluster may
  not serve, such as Flux's and `resource.k8s.io`, are
  found by discovery at start; a missing kind gets no store, `synced`
  does not wait for it, and the page says which edge kind is off and
  why.  Owners of a custom kind are watched with metadata-only watches,
  one per kind discovered in an owner reference.  transect still reads
  no Secret and no ConfigMap: environment names come from the pod spec.
  `cluster/mod.rs` splits by group when it nears the line limit.

- **Workload count.**  A custom resource that owns a StatefulSet
  is one thing to a reader, not two.  From stage 4, a
  namespace's workloads are its top-level owners, so a database run by
  an operator counts once, as its custom kind.  Parcels that hold
  custom kinds get lighter, and the map moves once.

- **Cards.**  A card fits one screen and draws only what the API and
  Prometheus give: the kind and name, its health in a word, its hosts,
  its workloads or pods, what it depends on and what depends on it, its
  guards, and its link to the directory it is declared in.  Cards live
  in a new `cards/` module, because the namespace card shows on two
  views, the selected cluster and the namespace page.  Every sentence is
  a template in a `sentence.rs` beside the card, as in `health/` and
  `findings/`.

- **Intent text.**  The comps' cards open with a line of what the
  thing is, which is a `<prefix>/why` annotation, and board 3's "loses
  office file conversion" is `<prefix>/provides`.  Reading two
  annotations is cheap, but the design puts intent in slice 3.
  Intent waits for slice 3, so this plan proves every card works
  with none of that text.

- **Usage gets its own module.**  The workload and pod pages need CPU
  per pod and memory and CPU per container, which the machines band
  does not.  With a second reader, `machines/poll.rs` and
  `machines/usage.rs` move to a `usage/` module that runs every query
  on the scrape interval and answers by node, pod, and container.

- **Workload rules are findings.**  The design lists "a workload with
  replicas and no readiness probe" and "a replicated workload with no
  PodDisruptionBudget" as findings.  They go in `findings/` beside the
  machine rules and show on the workload's page and card.  The cluster
  sidebar shows them as one count per rule, such as "12 workloads with
  replicas have no readiness probe", so they do not bury the concerns.

- **Elevations stay in `map/`,** one submodule each:
  `map/namespace/`, `map/workload/`, and `map/pod/`, each with its view
  model, template, and CSS, as `map/cluster.rs` has now.  A shared
  `crumbs.html` draws the elevation crumb on every page.

## Stages

### 1. Selection

`/?select=<namespace>` fills the selected parcel with the selection
color, and the crumb reads `cluster / <layer> / <namespace>`.  Clicking
a parcel selects it; Escape clears.  The event stream becomes one
stream per view, so a selection survives a push.  `web/` gains the
query and the per-view registry; `map/cluster.rs` marks the selected
parcel.  Look: select a namespace, scale a workload in another one, and
watch the other parcel change while the selection stays.  Paste the URL
into a second window and get the same view.

### 2. The namespace card

The sidebar shows the selected namespace's card (board 2): its name and
health in a word, the hosts of its Ingresses, and one chip per
workload, such as "web ×3", with CronJobs as one count.  With nothing
selected, the sidebar shows "What to know now" as it does today.  Adds
`cards/namespace.rs` and the Ingress reflector.  Look: select a
namespace with a failing workload, and the card names the workload in
the same sentence the sidebar used.

### 3. Runs on

With a namespace selected, each machine strip says how many of its pods
run there, or "none here" (board 2).  `machines/` counts by
`spec.nodeName`, which it already reads.  Look: select a namespace whose
pods spread across machines, and the counts add up to its pod count on
the card.

### 4. Ownership

A new `ownership/` module walks each pod's owner references to its top
owner: Pod → ReplicaSet → Deployment, Pod → Job → CronJob, and up
through any custom kind, such as a database's custom resource that owns
a StatefulSet.  The card's chips name top owners by kind and name, such
as "Cache sessions" for a cache an operator runs, and parcels count
top owners (see the decision on
workload count).  Health stays a roll-up of the built-in workloads
underneath.  Look: select a namespace whose database an operator runs,
and the database is one chip with its custom kind.

### 5. The namespace elevation

`/namespaces/<namespace>` (board 4).  The namespace's band opens to
show one box per top owner: its kind and name, a template line such as
"3 replicas, spread by anti-affinity", and a chip per machine its pods
run on.  Owners with one pod each, no volume, and no Service of their
own group into one box of small helpers.  The other bands collapse to
one line each, naming their heaviest namespaces and "· N more"; the
machines band becomes one row of chips with pod counts.  The card sits
beside it.  Adds `map/namespace/` and per-workload health from
`health/`.  Look: open a namespace from the cluster map, check every
workload is there, go back, and land on the cluster map with the
namespace still selected.

### 6. Stores on, and the ground band

A new `storage/` module follows each PVC to its PV and its backend: an
NFS server and path, an iSCSI target, a CSI driver and StorageClass, or
a local volume on one machine.  The ground band, which plan 01 left
out, draws one box per backend outside the cluster.  A backend is one
box per address the PV names, or per CSI driver when it names none;
folding several drivers into one storage appliance waits for intent.
With a namespace selected, its ground box counts its volumes by type,
such as "6 NFS · 2 iSCSI"; the namespace page shows the same counts on
one line, and the card counts volumes.  A local volume is a stores on
edge to its machine, not to the ground.  Look: the ground band lists
the cluster's storage, and a selected namespace's counts match
`kubectl get pvc -n <namespace>`.

### 7. The workload elevation: pods and guards

`/namespaces/<namespace>/<resource>/<name>` (board 7).  A row of the
namespace's other workloads to switch between, then one tile per pod:
its name, machine, age, restarts, a memory bar that runs to the limit
with the request marked, and a CPU bar that runs to the limit or, with
none, to the request.  Below, the guards: anti-affinity or topology
spread by machine, the PodDisruptionBudget and how many pods it lets
go, the update strategy, a missing readiness probe, a missing CPU
limit.  The workload card shows its ready count, image, and when it
last rolled.  Adds `map/workload/`, `cards/workload.rs`, the `usage/`
move with a per-pod CPU query, the PodDisruptionBudget reflector, and
the two workload findings.  Look: open a Deployment with replicas, and
check its bars against `kubectl top pod`.

### 8. The workload elevation: front door and volumes

The row across the top of board 7: the Ingress that reaches the
workload (host, class, TLS issuer from its annotation), the Service
(type, port, and endpoints from its EndpointSlices), the pods from
stage 7, and the volumes (claim name, size, and backend from stage 6).
A Service reaches a workload when its selector matches the workload's
pod template.  Adds the Service and EndpointSlice reflectors.  Look:
open a web application, and follow its host to its pods to its
volumes.

### 9. The pod elevation: containers and placement

`/namespaces/<namespace>/pods/<name>` (board 8).  A row of the
workload's other pods, then one block per container: image, command,
ports, memory and CPU bars against request and limit, QoS class,
restarts, and probes.  Below, where it runs: the machine, its role, how
many pods run there, and when the pod started.  Adds `map/pod/` and the
per-container queries in `usage/`.  The crumb now runs cluster, layer,
namespace, workload, pod.  Look: open a pod from its workload, and
compare its containers with `kubectl describe pod`.

### 10. The pod elevation: mounts and environment

Each mount's path, its volume, and its claim and backend, with which
other pods share the claim.  The environment as names only: each Secret
and ConfigMap the pod reads, with how many keys it reads from each, and
a count of plain values.  An `envFrom` reads every key, and says so
instead of counting, because counting would mean reading the Secret.
Look: a pod with Secrets shows their names, and no value appears
anywhere in the page source.

### 11. Depends on

A new `addresses/` module indexes the cluster's addresses: each
Service's DNS names in every short and long form, with and without a
port; each Ingress host; each LoadBalancer address; each machine
address.  It resolves every literal environment value against the
index, and a match is a `depends on` edge from the workload to the
Service's workload.  The workload page's "Also needs" row and every
card's "depends on" and "depended on by" read from it.  Unit tests
cover each form of Service name, a URL with a scheme and path, and a
value that only contains a Service's name as a substring.  Look: a web
application's page names its database and its cache, and the database's
card names the application.

### 12. Reconciles from, and links to code

When Flux is installed, a new `flux/` module watches Kustomizations,
HelmReleases, and their sources, and reads Flux's labels on every
object.  An object reconciles from its Kustomization or HelmRelease,
which reconciles from its source.  Each card gains its link to code:
the source's URL and the Kustomization's `spec.path`, which reaches
the directory, not the file.  Without Flux, the card says nothing about
code and the page says the edge kind is off.  Look: a card links to the
directory its manifests live in, and following the link lands on it.

### 13. The controller

`ownership/` finds the controller of each custom kind that is a top
owner: the ServiceAccounts that RBAC lets update `<kind>/status`, not
counting rules that grant every resource, narrowed by the field manager
that writes the object's status in `managedFields`, then the pods that
run as those ServiceAccounts.  The edge goes from the custom resource
to the controller's workload.  When more than one candidate is left,
the edge is marked as a guess.  With a namespace selected, the parcels
of its operators say "manages"; on the namespace page, a box reads
"managed by <namespace>".  Look: select a namespace with a
database run by an operator, and the operator's parcel says "manages".

### 14. Loops

`edges/` finds cycles among the namespace-level edges.  Address
resolution now also runs on Flux source URLs, so a forge that serves
the manifests Flux applies to the forge itself shows as a loop.  A loop
tags its parcels with "⟲ loop" on the namespace page's collapsed bands,
and the card gains a section naming its members and the edges between
them, as a template sentence.  Which kinds count is in the decision
below.  Look: if the cluster has a loop, both namespaces show
it; if it has none, a test fixture proves the finder.

- **Which edges make a loop.**  A loop is a cold-start risk: two
  parts that each need the other to come up.  Count
  reconciles from, depends on, managed by, stores on, and holds; leave
  out allowed to call, which allows without requiring.

### 15. Allowed to call

A new `policy/` module evaluates NetworkPolicies to pairs of
namespaces: a pod in one may reach a pod in the other on a port when
the target's ingress rules and the caller's egress rules both allow it,
with a pod that no policy selects allowed everything.  With a namespace
selected, the namespaces allowed to call it say "may call".  When no
policy selects the selected namespace's pods, nothing is tagged, and
the card says "No policy isolates it, so any pod may call it."  Only
the built-in NetworkPolicy is read; a CNI's own policy kinds are not.
Table-driven tests cover pod and namespace selectors, `ipBlock`, ports,
and an empty rule list.  Look: select a database with a policy, and
compare the tags with the policy's rules.

### 16. Holds

When the cluster serves `resource.k8s.io/v1`, a new `devices/` module
follows each pod's `status.resourceClaimStatuses` to its ResourceClaim,
the claim's allocation to a device, and the device to its ResourceSlice
and machine.  With a namespace selected, a machine strip names the
devices the namespace holds there, by driver and device; the pod page's
placement says what the pod holds.  Look: select a namespace whose pod
holds a USB radio, and its machine says so.

### 17. Edge kinds on and off

A row of the edge kinds that this cluster has, each with a checkbox,
on every page.  `?edges=` in the URL holds the kinds turned on, so a
view with only storage edges is a link.  All are on by default.  Look:
turn off "allowed to call" with a namespace selected, and the "may
call" tags go while the others stay.

### 18. The namespace as a matrix

A namespace where DaemonSets are most of the workloads draws as board 5
instead: one row per DaemonSet, one column per machine, and in each
cell whether its pod is ready, restarting, or not scheduled there by
its node selector.  Its single-replica Deployments list below as a row
of names.  Restarts count as they are; telling a reboot from a crash
needs the machine's uptime, which waits for kind knowledge.  Look:
open a namespace of DaemonSets and find a machine that a DaemonSet
skips by design.

### 19. Last log lines

The pod page's sidebar shows the
last lines of each container's log, read from the API when the page
loads, not streamed.  Lines that look like errors or warnings take the
same colors as health.  Look: open a pod that logged an error, and see
it.

- **The API's log tail.**  The design makes the Kubernetes API
  the default log source, with no resource of transect's own, but puts
  log sources in slice 3.  Build the tail here, since it
  needs only `pods/log`, and leave log stores and log patterns to
  slice 3.

## Not in this plan

- The cut (board 6): one service drawn from its front door to its
  storage.  It is the first board to need lines.
- A lost machine's blast radius (board 3).  It needs runs on, stores
  on, replica counts, and which volumes are attached to one machine,
  all of which this plan builds, so it can be the next plan.
- A custom kind through kind knowledge (board 9), the health of a
  custom kind from its own conditions, and every intent annotation.
- Authenticates through, which needs kind knowledge.
- The `calls` edge and the comparison with NetworkPolicies: slice 2.
- Alerts from Alertmanager.
- Search and "size parcels by."
- Splitting a namespace into several parcels.
- The code excerpts and recent commits in the comps' cards: they need
  the repository, not the API.
- The OCI image labels that link to the code an image runs: they need
  the registry.
