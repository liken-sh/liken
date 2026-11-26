# 21, The agent system

Proposed on 2026-11-26.  Not built.

Revised on 2026-11-26 after reviews against the code of `transect` and
of each project.  This form keeps the first build to what the code can
take in about a day, and it moves the rest to "What waits".

This plan draws the second case of [00-design.md](00-design.md), "a
system of several services that work as one".  These projects make the
system:

- `simmer` starts a turn of an agent as a `Job` when an event arrives.
- `wicket` takes webhooks and serves them to subscribers as events.
- `lanyard` gives a pod its credentials.
- `gofer` gives a pod a browser.
- `midden` keeps the transcript of each turn.

`transom`, which carries a question from a program to a person, takes
part too, and ships no knowledge yet.  Each project ships the
knowledge of its own kinds and programs, and `transect` draws the
system from that knowledge and the live cluster.

## The problem

The case section of 00-design found that this system is mostly legible
from the cluster, because custom resources declare nearly all of it.
`transect` still cannot draw it.

**The projects connect through tokens in pod templates.**  `simmer`
gives a turn no credentials (its README), and it knows nothing of
`midden` (`simmer/plans/operator.md`, "What simmer does not know
about").  The cluster owner connects the projects in a `Mandate`'s
`spec.template` and a `Duty`'s.  Each template mounts a projected
`ServiceAccount` token for an audience, such as `lanyard` or `midden`,
and each program that receives such a token checks it with a
`TokenReview`.  The `simmer` operator adds a token for the audience
`simmer` to each pod that it makes.  `gofer`'s webhook adds a token for
`gofer` when the API server admits a pod with the label
`gofer.guid.foo/profile`, and it refuses a pod whose template asks for
that token itself (`gofer/inject/rules.go`).  A turn's pod is a `Job`
that runs only while the turn runs, so most of the time no pod shows
any of these tokens.  `transect` reads projected tokens only from
running pods, in `transect-access/src/access/running.rs`, and only to
ask whether the API server accepts them.

**No knowledge describes a program.**  `wicket` and `midden` define
no custom resource, so no `KindKnowledge` can describe them.  A `KindKnowledge` is named
`<plural>.<group>`, and only its `controller.image` finds a program.

**A derived `ServiceAccount` shows no owner.**  The operator chooses a
`Duty`'s account by a rule in `session/duty.go`.  The label in
`spec.serviceAccount` gives `simmer-<mandate>--<label>`.  Without a
label, a `Duty` with `spec.grants` runs as an account of its own name.
Otherwise it runs as the `Mandate`'s account, which the `Mandate`
records in `status.serviceAccount`.  The operator sets an owner
reference from each account that it makes to its `Duty` or `Mandate`,
and `transect`'s ownership index, `transect-cluster/src/cluster/owners.rs`,
leaves `ServiceAccount`s out.  So no page says which `Duty` an account
is for.  On a home cluster, the accounts of the hooks have the
`Mandate`'s cluster-wide `view` grant, though a hook only posts a
commit status, and no page puts that grant beside what the hook does.

**The order of the work has no form.**  00-design says that the order
of the interactions is a claim of the author, and that a sequence
diagram explains this system where a pipeline did not.  `transect`
draws only sketches, and an `Elevation` requires a grid.

**A failed turn leaves no `Job`.**  A `Session` records each turn in
`status.turns`: its before hook, the turn, and its after hook, each
with an outcome and a reason.  The `Job`s are deleted when their time
to live ends, so the status is the only record in the cluster.

## What stays as 00-design settled it

- `transect` calls no language model, and every sentence is a template
  or stored text.
- Each fact ships with the project that owns the kind or the program
  that it describes.  A project's files name only its own kinds and
  programs, core kinds, and audience strings.  So `lanyard`'s files
  never name `simmer`, and `simmer`'s files name no other project.
- A part that the knowledge expects and the cluster lacks is drawn
  empty.  An audience that no loaded knowledge accepts is drawn as not
  known, a third state apart from healthy and empty.
- An expectation that is met is shown as met.
- Each elevation has a name, a question, an author, and a URL of its
  own, and the reader starts from the namespaces.
- A pipeline does not explain this system (00-design, "The agent
  system, through the primitives"), so this plan draws none.

## The first build

Each step below is built and proved in this order.  Each depends only
on the steps above it.

### 1. A `ServiceAccount` shows its owner

`owners.rs` adds `ServiceAccount`s to the ownership index, from the
metadata store that `transect` already keeps for them.  The page of
each derived account then names the `Duty` or the `Mandate` that owns
it, as the page of a `Pod` names its `ReplicaSet`.  This needs no
knowledge.

The operator chooses the owner in `labeled` of `mandate/grants.go`.  A
`Duty` owns an account only when it is the one `Duty` that runs as the
account's label and it does not name the label, which is the case of a
`Duty` with `spec.grants` and no `spec.serviceAccount`.  A label that a
`Duty` names in `spec.serviceAccount` or in `spec.hooks.serviceAccount`
gives an account that the `Mandate` owns.  So the account of a hook,
such as `simmer-<mandate>--review-status`, names its `Mandate` and not
its `Duty`.

It does not say which of a `Duty`'s templates runs as the account,
because the operator records that nowhere.  The native fix is in
`simmer`: a `Duty` could record its turns' account and its hooks'
account in its status, as a `Mandate` does.  The plan suggests that to
`simmer` and does not depend on it.

### 2. Where a kind keeps its pod templates

A `KindKnowledge` takes `templates`.  Each entry names a field that
holds a pod template, or a part of one, and the audiences that the
kind's controller adds to every pod that it makes from that field.

```yaml
# Duty
templates:
  - {path: spec.template, use: turn, merge: spec.mandate, injects: [simmer]}
  - {path: spec.hooks.before, use: hook, injects: [simmer]}
  - {path: spec.hooks.after, use: hook, injects: [simmer]}
```

- `path`: the field.  It can be a raw object, as a `Duty`'s template
  is.  `transect` keeps the whole object of a kind whose knowledge has
  `references`, so the field is in its cache.
- `use`: a word that the pages show for the pods made from this field,
  such as `turn` or `hook`.
- `injects`: the audiences that the controller adds.  The list is
  constant.  The `simmer` operator also adds a token for each
  `McpServer` that equips a turn, and that rule waits.
- `merge`, optional: a reference of the same document whose object's
  template of the same `use` is merged into this one.  A `Duty`'s turns
  run from its own template and its `Mandate`'s, so a `Duty`'s turn
  template presents the audiences of both.  `transect` follows
  `spec.mandate` with `knowledge::follow`, the one place that follows a
  reference.  The hooks have no `merge`, because the operator does not
  merge the `Mandate`'s template into a hook's pod
  (`simmer/api/v1alpha1/duty.go`, `session/hook.go`).
- `runsAs`, optional: a field that names the `ServiceAccount` that
  these pods run as.  A `Mandate` states `status.serviceAccount`.  A
  `Duty` states none, because of step 1.  A template with `runsAs`
  runs as that account alone.  A template without it runs as each
  account that its holder owns.

`transect` leaves out a document with an `Invalid` condition when an
entry has no `path` or no `use`, when an audience of `injects` is
empty, or when `merge` is not the path of a `references` entry that
names a kind.  It gives `Unresolved` when the knowledge of the merged
kind states no template of the same `use`.

`transect` reads a template's audiences from
`spec.volumes[].projected.sources[].serviceAccountToken.audience`, with
the code of `running.rs`, which moves to `transect-graph` so that the
access lens and the edge code both call it.  An audience of the API
server is not counted.  A template presents its own audiences, its
`injects`, the audiences of a template it merges, and the audiences that
a program injects by label (step 3).  The templates of `Deployment`s,
`StatefulSet`s, `DaemonSet`s, and `CronJob`s are read the same way,
with no knowledge, and their pods run as the template's
`serviceAccountName`.  A built-in template that presents no audience
besides the API server's is left out.

### 3. A program

A `ProgramKnowledge` describes a program that a project ships.

```yaml
apiVersion: transect.liken.sh/v1alpha1
kind: ProgramKnowledge
metadata:
  name: gofer
spec:
  summary: Keeps a browser's logins from one pod to the next, for the pods of each BrowserProfile.
  image: guid.foo/gofer
  accepts:
    - audience: gofer
      verb: loads and saves the browser state of a BrowserProfile
      grants: {kind: BrowserProfile, group: gofer.guid.foo, path: "spec.serviceAccounts[]"}
  injects:
    - podLabel: gofer.guid.foo/profile
      audience: gofer
      names: {kind: BrowserProfile, group: gofer.guid.foo}
```

- `metadata.name`: the program's name, which the pages show.
- `summary`: as in a `KindKnowledge`.
- `image`: one image name or a list, with no tag and no digest, which
  would match one release only.  A container runs the program when the
  name of its image is the entry's name, or ends with the entry's name
  on a path-segment boundary.  A name is the image reference with its
  tag and digest removed, read as Docker's reference format reads it: a
  first segment that contains a `.` or a `:`, or that is `localhost`,
  is a registry host, a name with no host is on `docker.io`,
  `index.docker.io` is `docker.io`, and a one-segment path on
  `docker.io` is in `library`.  The entry is read the same way.  So the
  entry `guid.foo/gofer` matches `atcr.io/guid.foo/gofer:1.4` and
  `guid.foo/gofer:1`, and `nginx` matches `nginx:1.27` and not
  `ghcr.io/library/nginx`.  Two registries that serve one path do not
  match each other's entries, and a mirror that adds a prefix in front
  of the name still matches.
  Containers match, and so do init containers with `restartPolicy:
  Always`, the native sidecars.  A plain init container runs to its end
  before the pod starts, so it serves nothing: a `Console`'s pod copies
  the `simmer` binary in one.
- A workload whose top owner's kind states `templates` runs no program
  for a caller, because its pods are made from those templates and so
  are the callers.  `gofer`'s webhook adds a sidecar of the image
  `guid.foo/gofer-sidecar` to a turn's pod, and `gofer`'s entry does not
  match that image.
- `accepts`: each audience whose tokens the program takes.  `verb` is
  the phrase of the edge and of a step to the program.  `grants`,
  optional, names the kind whose objects say what the program gives to
  which `ServiceAccount`, and the field that names the account.  The
  kind must be one of the same project's, and its `KindKnowledge` must
  state a `references` entry at that path with `kind: ServiceAccount`.
- `injects`, optional: an audience that the program adds at admission
  to each pod with the label `podLabel`.  A template that carries the
  label in its `metadata.labels` presents the audience.  `names`,
  optional, says that the label's value names an object of that kind in
  the pod's namespace, so the template refers to that `BrowserProfile`.
  `names` has no `path`, because it reads the label's value.

The label's reference is in `ProgramKnowledge`, not in the
`BrowserProfile`'s `referencedBy`, because the label means something
only because `gofer`'s webhook acts on it, and a `referencedBy` entry
reads annotations of whole objects and not the labels inside a
template.

The match is by image because the image name is the one fact that
the program's author chooses and that every cluster shares.  It fails
when a cluster mirrors an image under another path, or sets the image
by a kustomize replacement.  A `KindKnowledge`'s `controller.image`
keeps its own match in this build, which removes the registry host and
also accepts a repository that ends with the entry.  Moving it to this match is a change of its
own, because it can change which workload controls a kind.

`transect` leaves out a document with an `Invalid` condition when it
has no `image`, when an `accepts` entry has no `audience` or no `verb`,
when a `grants` has no `path`, or when an `injects` entry has no
`podLabel` or no `audience`.  It gives `Unresolved` when `grants` or
`names` names a kind with no loaded `KindKnowledge`, or when `grants`
names a path that the kind's knowledge does not state as a
`ServiceAccount` reference.  The program stays in use without that
`grants` or `names`.  `transect` cannot tell which project ships a
kind, so it does not check that `grants` names a kind of the same
project.

### 4. The edge from a template to a program

For each audience that a template presents, `transect` draws an edge of
a new kind, `Kind::Presents`, from the holder of the template to each
workload that runs a program that accepts the audience.  The holder is
the object whose field holds the template: a `Mandate`, a `Duty`, a
`Deployment`, or a `CronJob`.  The edge's label is the program's `verb`
and the audience, such as "asks for a credential, as `lanyard`".  Its
provenance is the program's knowledge, the audience, and the template's
path.  The page of a holder that is a custom resource has a section,
"Presents tokens", with the audiences of each template, one line for
each program's `verb` and audience with the workloads at the other
end, and the pods that the holder owns that run now with one of the
tokens, as "seen on `<pod>`".  Most of the time no turn runs, and the
edges are drawn from the templates alone.

When no loaded knowledge accepts an audience, the same section lists it
under "presents a token that no loaded knowledge accepts".  The page of
a built-in holder, such as a `CronJob`, has no such section in this
build.

The page of a program's workload shows the program's summary, each
audience that it accepts with its `verb`, and its callers: the holders
at the other end of its `Presents` edges.  A workload that runs the
images of several programs has rows for each.  So the page of `lanyard`
lists each `Mandate`, each `Duty`, and each `CronJob` whose template
asks it for credentials.

### 5. What a `ServiceAccount` can obtain

The page of a `ServiceAccount`, where the access lens already draws
its RBAC, gains these sections.  Each one is present only when its
knowledge is loaded.

- **Owned by**: the `Duty` or `Mandate` from step 1, the account's
  controlling owner.
- **Runs as it**: the templates whose `runsAs` names it, each with its
  `use`.  The drawing of its RBAC already shows the pods that run as it
  now.
- **May run as it**: the templates that state no `runsAs`, whose holder
  is the account's controlling owner or merges the template of that
  owner, one line for each field with its holders.  simmer gives a
  `Duty`'s pods an account of the `Duty` or of its `Mandate` by a rule
  that no field records, so the page says "may".  A template with an
  exact account waits for simmer to record the account of each of a
  `Duty`'s templates in its status.
- **May present**: the audiences of those templates that the templates
  of "Runs as it" do not present.
- **Presents**: the audiences of those templates, each with the
  program that accepts it, or "not known".
- **Granted by**: for each loaded `accepts` with `grants`, the objects
  whose field names this account, each with the status of the
  condition that its kind's knowledge reads for health.
- **RBAC**: as the access lens draws it now.

The page does not judge a grant.  It puts these sections above the
drawing of the account's RBAC.  The account of a hook is owned by its
`Mandate` (step 1), so its page shows no hook template until `simmer`
records which account each of a `Duty`'s templates runs as.  It still
shows the `Badge`s that name it beside its `view` grant.

### 6. A sequence on the page of a `Mandate`

An elevation takes `sequence` in place of `sketch`.  `Elevation`
becomes a choice of a grid or a sequence (and, in step 7, rows).  A
sequence's lanes are roles, with the fills that a role has today,
relative to the page's object.  Its steps are interactions in the
author's order.

```yaml
# Mandate
roles:
  signals:   {reference: "spec.purview[keys]", label: Signals, stack: true}
  operator:  {controller: true, label: the simmer operator}
  duties:    {namedBy: {kind: Duty, path: spec.mandate}, stack: true}
  sessions:  {namedBy: {kind: Session, path: spec.mandate}, stack: true}
  turns:     {namedBy: {kind: Session, path: spec.mandate}, owns: Job, label: turn and hook Jobs, stack: true}
  allowance: {reference: spec.allowance}
  tools:     {reference: "spec.tools.mcp[]", label: MCP servers, stack: true}
elevations:
  - name: the loop
    question: How does an event become a turn, and what does a turn call?
    sequence:
      lanes: [signals, operator, sessions, turns, tools]
      steps:
        - {from: signals, to: operator, verb: delivers an event, state: {role: signals, condition: Connected}}
        - {from: operator, to: sessions, verb: wakes the Session that holds the event's key, state: {role: sessions, columns: [Phase, Waiting]}}
        - {from: operator, to: sessions, verb: or starts a Session for a Duty whose purview matches, state: {role: duties, condition: Ready}}
        - {from: operator, to: operator, verb: holds the turn while the budget is spent, state: {role: allowance, condition: Holding, good: "False", said: has room}}
        - {from: operator, to: turns, verb: runs the before hook, the turn, and the after hook as Jobs}
        - {from: turns, to: tools, verb: calls tools, state: {role: tools, condition: Ready}}
```

The roles use the fills of plans 14 and 16, and these additions:

- **`[keys]`**, a path step that reads each key of a map.
  `spec.purview[keys]` reads the names of the `Signal`s that the
  purview lists.  It uses brackets, because plan 16 relies on a path
  that never contains a brace.
- **`controller: true`**, a fill of the object that the page's object
  is controlled by, which `controller` in `map/workload/mod.rs` finds.
- **`owns: <Kind>`**, which replaces each part of the role with the
  objects of that kind that it owns, from the ownership index.  The
  index keeps `Job`s as metadata, which is enough for a part.

A step has `from` and `to`, which are lanes and can be the same lane,
and `verb`.  `state` is optional, in one of these forms:

- `{role, condition, good, said}`: the step is met when that condition
  of each part of the role has the status `good`, which defaults to
  `"True"`.  Each other part marks the step, with its reason.  `said`,
  optional, is what the good status means, and the row writes it in
  place of the condition: "has room 1/1" for an `Allowance` whose
  `Holding` is `False`.  Without it, a good status of `False` reads
  "not Holding".
- `{role, columns}`: the step shows how many parts of the role have
  each value of the printer columns, and judges nothing.  A `Session`'s
  state is its phase and what it waits on, and a finished `Session` is
  not a fault, so the routing step shows counts.

A step with no `state` says that no signal names it, as "A pattern
view" in 00-design asks: "transect reads no state for this step".

A lane's head names its parts when it has at most six.  A lane with
more names only the parts that are not healthy, and counts all of them
by the reason of their health, as "168 in all: Finished 166, healthy
2", so a `Mandate` with hundreds of `Session`s keeps its steps at the
top of the page.

For each audience that the `Mandate`'s templates present, with its
`Duty`s' turn templates, `transect` adds a lane at the right for each
program that accepts it, with one step from `turns` labelled with the
program's `verb`.  A program that fills a stated lane gets no added
lane, so `simmer`'s own audience adds nothing.  An audience that no
loaded knowledge accepts adds a dashed lane with the audience as its
label.  The added lanes follow the order of the audiences' names.

```
 Signals  operator  sessions  turns     tools  | lanyard   gofer    midden   transom
 [=]x3    [ ]       [=]x2     [=]x0     [=]x2  | [ ]       [ ]      [ ]      [- -]
  |-------->|                                    delivers an event         Connected 3/3
  |         |-------->|                          wakes the key's Session   Running 1, Finished 1
  |         |-------->|                          or starts one for a Duty  Ready 4/4
  |         |<-'                                 holds while spent         Holding False
  |         |------------------>|                runs hooks and the turn   no signal
  |         |                   |------>|        calls tools               Ready 2/2
  |         |                   |------------------->|                     asks for a credential
  |         |                   |----------------------------->|           loads browser state
  |         |                   |-------------------------------------->|  ships its transcript
  |         |                   |- - - - - - - - - - - - - - - - - - - - - - - ->| not known
```

The lanes right of `|` come from the knowledge of other projects.
`[=]x2` is a stack of two parts, and the counts are invented.  The
`Signal` lane stands for each `Signal`'s source, `wicket` or Jetstream,
so `wicket` has no lane of its own here.

A fault in a sequence gives `Invalid`: a lane or a step's end that is
not a role, a `state.role` that is not a role, an elevation with both
`sketch` and `sequence`, or `owns` on a role that has `elevation`.

### 7. The turns of a `Session`

An elevation takes `rows` in place of `sketch`: a table made from a list
in the object's status.

```yaml
# Session
elevations:
  - name: its turns
    question: What did each turn do, and how did it end?
    rows:
      from: "status.turns[]"
      header:
        - {label: phase, path: status.phase}
        - {label: waiting on, path: status.waitingOn}
        - {label: pending events, path: status.pendingEvents}
      columns:
        - {label: before hook, path: before.outcome, failed: [Failed]}
        - {label: turn, path: outcome, failed: [Failed]}
        - {label: Job, path: job, logs: true}
        - {label: after hook, path: after.outcome, failed: [Failed]}
        - {label: why the Session ended, path: after.reason}
        - {label: cost, path: usage.costUSD, format: dollars}
```

- `from`: a path with `[]`, and each item is one row.
- `header`: values of the object, shown above the table.
- `columns`: values of each item, by a path relative to the item.  An
  absent value is an empty cell.
- `format`, optional: `time` writes an RFC 3339 time as a short date
  and time in UTC, and `dollars` writes a number with a dollar sign and
  cents.  The raw value is in the cell's hover, and a value that does
  not parse is written as it is.
- `failed`, optional: the values that mean an item failed, such as
  `Failed`, which the table draws in the color of a fault.
- `logs: true`: the value is the name of a `Job`, and the cell links to
  that `Job`'s lines in the `LogSource` when one is loaded.  The link
  opens the logs lens of the newest pod of the `Job`, so it works while
  the `Job` has a pod.  A `Job` that its time to live deleted is shown
  by its name with no link, and a link by the `job-name` label to the
  lines of a gone pod waits for a query that the logs lens does not
  make yet.

A `rows` elevation with no `from`, or a `from` with no `[]`, gives
`Invalid`.

### 8. Objects of a kind side by side

The page of a kind, `/kinds/<plural>.<group>`, lists the objects of a
kind with its printer columns.  For a kind whose knowledge has
`templates`, it adds one column for each audience that any object's
templates present, named for the program that accepts it or for the
audience when none does.  Each cell is a filled socket when the object
presents the audience, and an empty socket when it does not.

On a home cluster, the page of `Mandate`s then shows which `Mandate`s
present no `midden` audience, so their transcripts are not kept.  That
is the finding that 00-design's case section made by reading the
manifests.  No object says why, and the column does not judge it.

## What each project ships

Each project adds a `transect/` directory, as `media-operator` has.  The
builder confirms each image repository from the project's deploy base
or `ko-build.sh`.  The programs of a project are in
`transect/programs.yaml`.

- **`simmer`**: `kinds.yaml`, a `KindKnowledge` for each of its kinds
  in the group `simmer.foo`, with `summary`, `healthy` by `Ready`,
  `controller`, and `references`.  A `Stop`'s `spec.session` is an
  object of `namespace` and `name`, so its reference reads both.  A
  `Mandate` has `templates` with `runsAs: status.serviceAccount`, the
  roles and the loop of step 6.  A `Duty` has the `templates` of step 2.
  A `Session` has the turns of step 7.  `programs.yaml`, the
  `ProgramKnowledge` `simmer`, which accepts `simmer` with the verb
  "reads its events, its keys, and its transcript", and no grant.  The
  operator serves the data plane, so its `Deployment` is the callee.
- **`lanyard`**: `kinds.yaml`, for `Badge`, `GitHubApp`,
  `GiteaAccount`, and `AtprotoAccount`, where a `Badge` references
  `spec.serviceAccounts[]` as `ServiceAccount`.  `programs.yaml`, which
  accepts `lanyard`, "asks for a credential", granted by `Badge`
  `spec.serviceAccounts[]`.
- **`gofer`**: `kinds.yaml`, for `BrowserProfile`, which references
  `spec.serviceAccounts[]` as `ServiceAccount`.  `programs.yaml`, the
  `ProgramKnowledge` of step 3.
- **`wicket`**: `programs.yaml`, which accepts `wicket`, "subscribes to
  the events of its paths", with no grant.  `wicket`'s audience can be
  configured and defaults to `wicket`, and its grants are lists per
  path in its configuration, which `transect` does not read.
- **`midden`**: `programs.yaml`, which accepts `midden`, "ships its
  transcript", with no grant.  `midden` admits pods by namespace, in
  `MIDDEN_POD_USERS` (`midden/config/config.go`), and the knowledge does
  not state it.

The projects' directories are in their own repositories, and a test of
`transect` reads nothing outside this one.  So the fixture's
`agent-knowledge.yaml` has the same forms, and its tests check that
they load and draw the edges.  A test that loads each project's
directory, and another that checks the lanes that the loop adds, wait
for a copy of each directory under `examples/`.

## How the first build is proved

The tests are in the crates, against YAML and the fixture cluster, as
every test of `transect` is.  The fixture gains a `Mandate` whose
template presents audiences, with no running turn, and invented names.

On a home cluster, read-only, with each project's `transect/` directory
linked into `local/resources/`:

- the page of a hook's account names its `Duty`, its hook templates,
  and its `view` grant;
- the page of `lanyard` lists its callers while no turn runs;
- a `Mandate`'s loop adds the lanes of `lanyard`, `gofer`, and `midden`,
  and a dashed lane for `transom`;
- with the link of `lanyard` removed, only that lane changes, to a
  dashed lane;
- a failed `Session` shows its turns after its `Job`s are gone;
- the page of `Mandate`s shows the column of `midden`.

## Which open positions of 00-design this settles

- **The form in which an author writes an arrangement**, in part: a
  sequence and rows are forms beside the sketch, over the same roles,
  and another project's knowledge can add a lane to a sequence.
- **What makes parts of the same sort**, in part: the objects of one
  kind, compared by the audiences that their templates present.
- **Where the reader finds a system that spans namespaces** stays open.
  This build answers it from the page of a `Mandate` and the page of a
  program's workload, and "What waits" has a page for a whole program.

## What waits

- **The pod of a turn and its tokens**, the next view: a sketch of one
  turn's pod, with its containers and each token that it carries.
- **A page for each program**, `/programs/<name>`, with the namespaces
  of everything that the program and its kinds make or that calls it,
  computed from the caches.  It needs role fills that are not relative
  to one object, and elevations at a URL with no object.
- **The `McpServer` audiences.**  The operator adds a token for the
  `spec.audience` of each `McpServer` that equips a turn, and not to a
  hook.  `injects` then needs a form that follows a reference and reads
  a field.  A `KindKnowledge`'s `accepts`, for the server at an
  `McpServer`'s URL, goes with it.  On a home cluster, a generic
  authenticating sidecar takes `midden`'s MCP audience, not `midden`,
  and its knowledge belongs to its own project.
- **Readings from Prometheus on a step**, such as
  `simmer_events_total`, `lanyard_requests_total`,
  `gofer_admissions_total`, and `midden_intake_requests_total`.  A
  reading shows only what its labels allow, so unowned events show per
  `Signal`, and no page claims which scopes nothing owns.
- **Grants in a program's configuration**, such as `midden`'s
  namespaces and the globs of an authenticating sidecar.
- **A loopback address**, such as an `McpServer` at
  `http://127.0.0.1:8931/mcp`, which is a container of the turn's own
  pod and not a `Service`.
- **A call that a `NetworkPolicy` refuses**: a mark on a `Presents`
  edge with no `AllowedToCall`, which is how a namespace without the
  policies' label shows.
- **Admission**: a webhook with `failurePolicy: Fail` makes the creation
  of each labelled pod depend on `gofer`.
- **Topics**: `wicket`'s paths, which are prefixes, shared by
  `Signal`s, and absent for a Jetstream `Signal`, in a `ConfigMap` in a
  format that `wicket` owns.
- **`Console`s**, which are outside a `Mandate`'s loop.
- **A name template with fields**, such as
  `simmer-{spec.mandate}--{spec.serviceAccount}`.  Step 1 makes it
  unnecessary for accounts, and it waits for another use.
- **The next time a scheduled `Duty` is due**, so that a `Duty` that
  has not run yet does not read "never ran".

## Open questions

- Should a `ProgramKnowledge` also match by a label, for a cluster that
  mirrors or retags an image under another path?
- Should a `KindKnowledge`'s `controller` name a `ProgramKnowledge` in
  place of an image, so that a project states its image once?
- A `Mandate`'s template is merged with a shared component when the
  cluster's manifests are built, and the live object shows the result.
  Should a page show which `Mandate`s share it?
