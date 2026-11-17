# 04: A lost machine, and the cut

Plan 02 built every edge the Kubernetes API gives and the elevations
that show them one end at a time.  This plan puts those edges to two
uses the comps draw: what a cluster loses when a machine goes (board
3), and one service drawn from its front door to its storage, with
lines (board 6).  Like plans 01 and 02, it is built in stages, and each
stage ends in something to reload and look at against a real cluster.

Board 3's outage is staged in the comps; its numbers are an example,
not a reading.  Board 9 (a custom kind through kind knowledge) is
skimmed here and left for slice 3.

## Decisions

- **The loss is a view of the cluster map, not an elevation.**  Board
  3 is the cluster map with its parcels colored by what the loss takes
  and its sidebar replaced by the loss.  So the loss is a query on `/`,
  as a selection is:

  | View | URL |
  |---|---|
  | the cluster, if one machine were lost | `/?lose=<machine>` |
  | the cluster, if several were lost | `/?lose=<machine>,<machine>` |
  | one workload's cut | `/namespaces/<namespace>/<resource>/<name>/cut` |

  `lose` and `select` do not combine in this plan: clicking a parcel
  in the loss view selects it and leaves the loss, and Escape returns
  to `/`.  A set of machines costs nothing over one, since the rule
  below counts pods off a set, and it answers "what if this rack's
  power goes."

- **One rule for a loss that is real and one that is not.**  A
  workload's state under a loss is its ready pods on machines outside
  the lost set, against the replicas it wants:

  | State | When |
  |---|---|
  | down | no ready pod outside the lost set |
  | short | some, but fewer than it wants |
  | untouched | as many as it wants, or none of its pods on the lost set |

  In the "what if" view, the pods on the lost machine are ready now and
  the rule discounts them.  When the machine is really gone, its pods
  are already not ready, and the replacements a Deployment starts
  elsewhere count as they become ready.  So the same rule draws the
  prediction on a healthy day and the recovery during an outage, with
  no second model of what Kubernetes will do.

- **DaemonSets and Jobs stand apart.**  A DaemonSet runs one pod per
  machine by design, so losing a machine never leaves it short; its
  pods there show as one line, "N DaemonSets' pods go with it."  A
  Job's pod there is lost and its CronJob runs again on schedule, so
  Jobs count in "N pods were here" and nowhere else.

- **Stranded pods, and how transect knows.**  A down workload either
  comes back on its own once Kubernetes evicts its pods, or it stays
  down until the machine returns.  It stays down when one of these
  holds, and the line names which:

  | Reason | Read from |
  |---|---|
  | its volume is on the machine's own disk | `storage::Backend::Machine` |
  | its block volume is attached to the machine | a VolumeAttachment for its PV on that machine, or, for an in-tree iSCSI volume that has none, a `ReadWriteOnce` claim mounted by its pod there |
  | it holds a device on the machine | `devices/`, a device whose slice is on that machine |
  | no other machine fits it | its node selector, required node affinity, and taints, against every machine outside the lost set |
  | it is a StatefulSet's pod | a StatefulSet does not replace a pod until the old one is deleted, which a lost kubelet never confirms |

  An NFS volume, or any `ReadWriteMany` claim, does not strand a pod.
  VolumeAttachment is a new reflector (`storage.k8s.io/v1`), cheap and
  cluster-scoped.  Whether the other machines have room for what moves
  is not in this plan (see the end).

- **The fit check moves to its own module.**  `map/namespace/matrix/fits.rs`
  answers "does this pod template fit this Node" for the DaemonSet
  matrix.  The loss is its second user, so it moves to a new
  `placement/` module, with its tests, and the matrix imports it.

- **The cascade goes one hop and names workloads.**  For each down
  workload, transect follows its incoming edges to what loses it:

  | Edge | Line |
  |---|---|
  | depends on, into the down workload | "<dependent> loses <workload>" |
  | routes to and selects, into it | "<host> stops answering" |
  | controlled by, into its custom resources | "<kind> <name> has no controller until it returns" |

  It stops after one hop.  Whether losing a dependency takes the
  dependent down too is something only intent says: one application
  fails without its cache, another runs slower.  A second hop drawn
  from the API alone would claim outages that do not happen.  A short
  workload does not cascade, since it still answers.

- **Where `provides` slots in.**  The cascade's line is a template over
  a `Loss { dependent, lost: Object }`.  In slice 3, `Loss` gains the
  lost workload's `<prefix>/provides` text, and the template prefers it:
  "loses office file conversion" in place of "loses <workload>", and
  the parcel's tag "no office file conversion" in place of "<workload>
  down".  Nothing in this plan reads the annotation, and every line
  works without it.

- **A machine that is really lost takes over the sidebar.**  When a
  Node's `Ready` condition is not `True`, `/` draws as board 3 for the
  set of machines that are not ready, with "What to know now" below the
  loss.  At 2am that is what the reader needs first.  The strip reads
  "Lost <minutes>" from the condition's `lastTransitionTime`.  A
  cordoned machine that is still ready is not lost.

- **What happens next is read from the pods.**  The "+5m" on board 3 is
  the `tolerationSeconds` of each pod's toleration for
  `node.kubernetes.io/not-ready` and `unreachable`, which admission
  sets to 300 by default.  The line uses the value most of the
  machine's pods carry and names the pods that differ.  For a machine
  that is really lost and has stranded pods, the sidebar shows the
  `node.kubernetes.io/out-of-service` taint as text, under "if it is
  really off."  transect runs nothing; the command is for the reader.

- **The control plane's majority is a guess, and says so.**  The API
  does not list etcd's members.  A lost control-plane machine reads
  "N of M control-plane machines remain", and "which keeps a majority"
  or "which loses the majority", counting each control-plane machine as
  one member.  The line is marked as a guess, as guessed edges are.

- **A `loss/` module.**  It takes the lost set, the pods, `Workloads`,
  the graph, `Storage`, `Devices`, and the Nodes, and returns the loss
  as data: each workload's state, each stranded pod's reason, the
  cascade's lines, and what happens next.  Every sentence is a template
  in `loss/sentence.rs`.  It is a pure function with table-driven
  tests, like every edge source.

- **The cut is anchored on a workload.**  Board 6's crumb ends at the
  service, and a service in this map is a workload with its front door.
  A workload with no Ingress still has a cut: the spine starts at its
  Service, or at the workload itself.  The cut opens from a link in the
  workload page's front-door row, and its crumb reads `cluster /
  <layer> / <namespace> / <workload> / cut`.

- **Lines are SVG, generated on the server, for the cut alone.**  Plan
  02 left this open until the cut.  The cut is a fixed layout: a known
  set of boxes in known rows and columns, with no wrapping.  So the
  server can compute each box's position and each line's path from the
  same layout, and render the boxes as absolutely positioned HTML and
  the lines as one `<svg>` beneath them, both in one fragment.  The
  alternatives each cost more:

  | Option | Cost |
  |---|---|
  | a graph library in the page | moves the layout into the browser, so positions change between visits, and adds a client dependency |
  | a script that measures boxes and draws lines | needs client code, and redraws after every pushed swap, so lines flicker |
  | CSS borders and pseudo-elements | draw a straight spine, but not a fan from one claim to three machines, or a dashed side line with a label |
  | server SVG on a fixed layout | box text has to fit a fixed size, cut short with the full text in its title |

  Boxes stay HTML so their text, links, and `hx-get` work as on every
  other page.  The cut is drawn at a fixed width, as board 6 is, and
  scrolls sideways in a narrower window.  The cluster map and the other
  elevations stay HTML with no lines.  This answers the design's open
  question for the cut, and stage 9 records it in `plans/00-design.md`.

- **The cut's rows are the map's bands.**  Spatial memory is the
  reason for the bands, so the cut keeps them, top to bottom: Front
  door, then each layer that holds a box in the cut in the map's order,
  then Volumes, Machines, and Ground.  Each box sits in the row of its
  own band, so a caller in another layer sits in that layer's row.
  Volumes is a row of its own, where board 6 put the claims in a
  platform layer's row, so the layout does not depend on the cluster
  having a layer of that name.

- **The spine and the sides.**  The middle column is the spine: the
  Ingress and its Service in the front-door row, the workload, its
  claims grouped one box per backend, the machines its pods run on, and
  the backends in the ground row.  The side columns hold what touches
  the spine from elsewhere: callers on the left (depends on, into the
  workload), and on the right what it depends on, what it holds, what
  it reconciles from, and its controller.  A row holds at most three
  boxes on a side and a fourth that reads "+N more"; the machines row
  holds at most four machines and then one box, "on N machines," so a
  DaemonSet's cut stays one screen.

- **Line styles follow board 6's legend.**  Solid lines are structure:
  routes to, selects, stores on, runs on, holds, and reconciles from.
  Dashed lines are callers: depends on.  A guessed edge, such as a
  controller found among several candidates, draws dotted.  Each line
  carries its edge kind as its label.  Board 6's "calls" label is the
  observed `calls` edge, which waits for slice 2; until then the dashed
  lines are configured addresses and read "depends on."  The edge row
  from plan 02 turns kinds off here as everywhere, and an off kind
  drops its lines and its side boxes.

- **The cut's second lines are evidence, not intent.**  Board 6's
  "tells it to rescan" and "syncs progress" are `<prefix>/why` and
  `provides` text, which waits for slice 3.  Until then a side box's
  second line names its kind and namespace, and a spine box's names
  what it is: "1 replica · ready", "3 claims · 266Gi", "runs the pod".

- **New module `map/cut/`**, with its view model, `layout.rs` for rows,
  columns, and coordinates, `lines.rs` for the SVG paths, its template,
  and its CSS, as the other elevations have.  The layout and the paths
  are pure functions of the boxes, so their tests check coordinates
  without rendering a page.

## Stages

### 1. If a machine were lost

`/?lose=<machine>` draws the cluster map with a sidebar that lists the
loss: a head, "If <machine> were lost: N pods run there," then "Down
until it returns" and "Running short," each as one line per namespace
naming its workloads, such as "a database: its only instance" or "a web
application: 2 of 3."  Namespaces one replica short past the first
three fold into "and N more namespaces one replica short."  DaemonSets
show as one line.  Adds the `loss/` module with the rule from the
decisions and its tests: a Deployment with all its pods there, with
some, with none; a StatefulSet of one; a DaemonSet; a Job; a set of
two machines.  Look: open the loss for a machine, and check its
counts against `kubectl get pods -A -o wide --field-selector
spec.nodeName=<machine>`.

### 2. The map under a loss

In the loss view, parcels color by the loss on top of today's health:
a namespace with a down workload takes the failure fill, one with a
short workload the warning outline, and an already-failing namespace
keeps its color.  Each tag names what the loss takes, such as
"<workload> down" or "1/2."  The lost machine's strip draws as board
3's: its name, "if lost," and "N pods run here," with no bars.  Each
machine's name in the machines band links to its loss, and Escape or a
click on empty band returns to `/`.  Look: lose a machine that runs a
single-replica workload, and its parcel fills; lose one that runs
nothing but DaemonSets, and the bands stay gray.

### 3. Stranded: volumes and devices

The "Down until it returns" list splits by whether a workload comes
back: "starts elsewhere after eviction" or "stuck," with the reason.
This stage reads three of the reasons: a local volume on the machine,
a block volume attached to it, and a device held there.  Adds the
VolumeAttachment reflector, found by discovery like the other optional
kinds, and the access modes on each claim to `storage/`.  Look: lose a
machine that runs a single-instance database on an iSCSI volume, and
it reads "stuck: its iSCSI volume is attached to <machine>"; lose one
whose pod holds a USB radio, and it reads "stuck: it holds <device>
there."

### 4. Stranded: placement, and what happens next

The other two reasons: no machine outside the lost set fits the pod's
template, and a StatefulSet's pod.  `map/namespace/matrix/fits.rs`
moves to `placement/` first, unchanged, with the matrix reading it from
there.  The sidebar gains "What happens next": eviction after the
pods' not-ready toleration, which replicas start elsewhere, and which
stay stuck.  Look: lose a machine that a workload is pinned to by node
selector, and it reads "stuck: no other machine fits it"; the eviction
line's minutes match a pod's tolerations in `kubectl get pod -o yaml`.

### 5. The cascade

For each down workload, the sidebar names what loses it, one hop out,
with the three lines from the decisions: dependents by depends on,
hosts that stop answering, and custom resources left with no
controller.  The dependents' parcels take the warning outline and a
tag "loses <workload>."  The workload card gains "If it goes down,"
from the same lines for that one workload alone, so its page answers
the question without a loss view.  Look: lose the machine a database's
only instance runs on, and the application configured with its address
reads "loses <database>"; open the application's database's page, and
its card says the same.

### 6. A machine really lost

When a Node is not ready, `/` draws the loss for every machine that is
not ready, with "What to know now" below it.  The head reads
"<machine> is lost · N minutes," the strip reads "Lost Nm," and a lost
control-plane machine adds the majority line.  For stranded pods, the
out-of-service taint shows as text.  As replacements become ready
elsewhere, workloads leave "Down" and "Running short" on the next
push, and the loss clears when the machine is ready again.  Look:
during a machine's reboot, such as an upgrade's reboot turn, open `/`
and watch the loss appear, shrink as Deployments recover, and clear
when the machine returns.  A unit test with a not-ready Node covers the
same path on a quiet day.

### 7. The cut: boxes

`/namespaces/<namespace>/<resource>/<name>/cut` (board 6), without
lines.  The spine's boxes in their rows: the Ingress (host and class)
and its Service (type, port, endpoints), the workload (replicas and
ready), the claims (one box per backend, with count and total size),
each machine its pods run on, and each backend in the ground row.
Rows that hold no box are left out.  Adds `map/cut/` with `layout.rs`
and its tests: a workload with no Ingress, one with three machines, a
DaemonSet on more than four, claims on two backends.  The workload
page's front-door row links to the cut.  Look: open the cut of a web
application, and every box matches a column of its workload page.

### 8. The cut: lines

The spine's lines, as one SVG under the boxes: routes to, selects,
stores on from the workload to each claims box, from each claims box
to the machines that mount it, and from each machine to the backends
its claims name.  Each line has an arrowhead and its edge kind as a
label.  Adds `map/cut/lines.rs`, whose tests check each path's ends
against the boxes' edges.  Look: a workload with one pod draws one
straight spine; one with pods on three machines fans from its claims
to each machine and back to one backend.

### 9. The cut: sides

The side columns: callers on the left, and on the right what the
workload depends on, the devices it holds in the machines row, the
Flux object it reconciles from, and its controller, each in its own
band's row, with dashed, solid, and dotted lines by kind.  A row folds
past three boxes.  The legend line from board 6 sits above the cut, and
the edge row turns kinds off.  `plans/00-design.md`'s open question on
SVG records the answer from this plan's decision.  Look: open the cut
of a media server that another application is configured to call,
and the caller sits in its own layer's row with a dashed line; turn
off "depends on," and the caller and its line go.

### 10. The cut: card and live

The workload card from its page sits beside the cut, with "If it goes
down" from stage 5.  The cut renders as one block and swaps whole on a
push, so a pod moving to another machine moves its line.  Look: delete
a pod of a workload with replicas while its cut is open, and the
machines row and its lines change without a reload.

## Not in this plan

- `<prefix>/provides`, and every intent annotation: slice 3.  The
  cascade's `Loss` and its template are where `provides` lands.
- A cascade past one hop, which needs intent to say which losses take
  a dependent down.
- Whether the remaining machines have room for the pods that move: a
  sum of requests against free allocatable is easy, but a true answer
  is bin packing with affinity, and a wrong answer is worse than none.
- A drain view, which is a loss without the stranded pods that a
  graceful shutdown releases.
- etcd's real membership, which the API does not give.
- Selecting a namespace inside the loss view.
- A custom kind through kind knowledge (board 9).  Its radial layout of
  relations around one object is a second fixed layout that can reuse
  `map/cut/lines.rs`, once kind knowledge gives it the relations.
- Lines on the cluster map or the namespace page, which wrap and so
  have no positions on the server.
- The `calls` edge on the cut's dashed lines: slice 2.
- Authenticates through, the code excerpts and commits in board 6's
  card, and image provenance (plan 03).
