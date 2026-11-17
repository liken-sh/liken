# 09: Access

The owner asked for "security/access-oriented lenses or views".  Over
four rounds of comps he and a designer settled what the lens is: two
questions, each drawn at three sizes.

1. What may reach this port, and through what?
2. What may this account do, and where?

The cluster has a matrix for each, a namespace's page has both, and a
workload or an account has a drawing.  The final comps are in
`local/comps/access/final/`, with a designer's note beside them, and
they are the target the way the board comps were for slice 1.  They
draw one real cluster, so this plan names what they show in generic
terms.

The lens paints what is true so the owner can see the gaps himself.
It is not an alerting system.  It raises no finding, uses no problem
color, gives no score, and counts nothing as an issue.  Every sentence
is a plain fact in the present tense, and a permission reads "may",
never "can" or "could".  Where the people who run the cluster have
written a sentence about a fact, the sentence shows beside it with the
`intent` mark.  Nothing is labelled as undeclared.

## Built

Every stage is built.  The design holds every rule they set; in short:

1. **Access from RBAC, and the accounts matrix** (935e295).
2. **Ways in** (a7ddc2b).
3. **Declared gates** (02e5372).
4. **A workload's access** (b4e1067, f115dad, 3114291).  "Who may
   change it" reads an index the reduction builds (6ecc625), and the
   boxes keep their order from one push to the next (7e78e60).
5. **An account's access** (c8fe896, 387b5ef).  An applier whose kind's
   roles name no writer finds its controller by the image its
   knowledge names (72efccd), and an account bound to every verb ends
   one chain, not several (b99d67e).
6. **A namespace's access** (e241238), and every name on an access page
   opens its access page (0e5da83).
7. **Check the design**, in the commit that adds this section.  No
   access page of the real cluster says "can", "could", "risk", or
   "issue", and none draws a color but gray and the link's.

The owner's pass over stages 1 to 6 asked for five changes outside the
lens, each its own commit:

- Lists of API groups, DRA drivers, and hosts sort by the name's labels
  from the right, with one ordering (b4f3f7c).
- Rows of machines put the control plane first, with the machines
  band's border (b307a8e).
- A namespace card's Holds lists each held device under its holder
  (cd74b6b).
- The edge kinds leave the masthead for the page's main column, above
  the picture they filter (b0a9d2f, 24f32c3).
- A host-port row on Ways in names the workload that listens, and the
  port column keeps each port whole (d8ab2d1).

The examples gained the fields this plan added that public projects
have: Traefik's entry points and k3s's listener labels (c632e20).

## Decisions

### Access is read from RBAC objects alone

transect reduces Roles, ClusterRoles, RoleBindings, and
ClusterRoleBindings to capabilities itself, from the four whole
watches it already has, and the metadata watch on ServiceAccounts.  It
makes no SubjectAccessReview and needs no permission plan 07 does not
grant.  It never reads a Secret's data; a Secret shows by its name, as
the pod specs and Roles that name it give it.

RBAC does not show a webhook authorizer, the node authorizer, or an
admission webhook, and the API does not say whether an address answers
from the internet, or what a workload does with a request itself.
Each access page's card says so in gray lines under "Not read", once,
and nowhere else.

The reduction keeps every form a rule takes:

- `*` in verbs, API groups, resources, and subjects' names.
- Subresources: `pods/exec` and `pods/attach` are their own resources,
  `*/exec` is the `exec` of every resource, and `*` covers every
  resource and subresource.  `pods/*` is not a wildcard: the API
  server's matcher reads it as a resource of that literal name.
- `resourceNames`: a rule limited to names is kept and reads "by name",
  with the names on the drawing.  A rule that names Secrets lets the
  account `get` those, and `list` or `watch` them only through a
  `metadata.name` field selector, one at a time, so the drawing says
  whether it may list them.
- Aggregated ClusterRoles: the controller manager writes the
  aggregated rules into `rules`, so the role reads as any other, and
  its drawing names the roles its `aggregationRule` selects.
- Group subjects: a binding to `system:serviceaccounts`,
  `system:serviceaccounts:{namespace}`, `system:authenticated`, or any
  other group grants to every account in it without naming them.  A
  group or a user that no object stands for, such as Group
  `system:masters`, is an account of its own on the lens's pages.

### Six capabilities

| Capability | A rule grants it when it allows |
|---|---|
| read Secrets | `get`, `list`, or `watch` on `secrets` |
| exec into pods | `create` on `pods/exec` or `pods/attach` |
| run pods | `create` on `pods`, or on `jobs` or `cronjobs` in `batch` |
| change workloads | `create`, `patch`, or `update` on `deployments`, `statefulsets`, `daemonsets`, or `replicasets` in `apps`; `patch` or `update` on `jobs` or `cronjobs` |
| grant roles | `bind` or `escalate` on `roles` or `clusterroles`; `impersonate` on `users`, `groups`, or `serviceaccounts` |
| change what a GitOps tool applies | `create`, `patch`, or `update` on a kind whose kind knowledge declares `applies`, or one that declares `repository`, the source an applier fetches from |

The access table in plans/00-design.md is the spec now.  A
correctness review after the build changed several of its rows; this
table is the one stage 1 built.

The sixth comes from plan 08's knowledge, so a cluster with no GitOps
knowledge has an empty column and its card says which knowledge would
fill it.  A column's title gives its verbs on hover.

A cell is filled where a binding grants the capability, as a block
whose width says where: full for every namespace, short for some, with
the namespaces named beside it.  A cell is outlined where the
capability follows from a grant.

### What follows from a grant

No binding grants it, and the account may still do it, by one of these
steps:

- An account that may run pods or change workloads in a namespace may
  run a pod as any ServiceAccount there, since a pod names any
  ServiceAccount in its own namespace.  It may do what those accounts
  may.
- A pod an account starts may mount any Secret in its namespace, so
  read Secrets follows from run pods and from change workloads there.
- An account that may exec into a namespace's pods may read the Secrets
  those pods mount, by name.
- An account that may change what a GitOps tool applies may do what
  the tool's applier may, which is its controller's account, or the
  account the applier's object names when its knowledge says where
  (see `applies.account` below).
- An account that may grant roles and create bindings may bind any
  role to itself, so it may do what every role grants.

- An account that may exec into a namespace's pods may act as the
  ServiceAccount of each pod there that mounts its token.

The closure runs to a fixed point and keeps, for each capability that
follows, its first hop: the account or the applier it goes through.
The "through" column, the account's drawing, and a workload's "Who may
change it" all name that hop.

### The pages

| Page | URL | Shows |
|---|---|---|
| Ways in | `/access` | Every port that answers from outside the cluster, a row per way in, with its three gates and the workload it reaches |
| Accounts | `/access/accounts` | Every account a binding grants one of the six, a row each, with where |
| A namespace's access | `/namespaces/{ns}/access` | Every port its workloads serve with the ways in to each, and every account that may act there |
| A workload's access | `…/{name}/access` on any workload | What may reach each port it serves, what it runs as, holds, and may call, and who may change it |
| An account's access | `/namespaces/{ns}/serviceaccounts/{name}` | What runs as it, its bindings, what they grant, what follows, and who may run as it |
| A group's or a user's | `/access/accounts/groups/{name}`, `/access/accounts/users/{name}` | The same drawing for a subject that no object stands for |

`/access` opens on Ways in, and Ways in and Accounts are two chips in
the page's switcher row, as a machine's page has.  The masthead gains
an `access` link beside `kinds`.  Access is not drawn on the cluster
map.

The lens tab is named `access` on every page that has one: `overview ·
section · logs · access` on a workload and `overview · access` on a
namespace.  Any workload that serves a port has an access tab, and
"no way in" is a stated answer, drawn as a dashed box where the ways
in would be.  A ServiceAccount's page is its access drawing: its
overview and its access are one page, with no tab row.

### The link rule bends on access pages

On an access page, an object's name opens its access page when it has
one, and its usual page otherwise.  A row of Ways in opens the access
of the workload it reaches; a row of either accounts matrix opens the
account; a workload's "runs as" box and each account under "Who may
change it" open the account; a box that stands for several accounts
opens the namespace's access; on an account's drawing a role opens the
role's page.  `links::Pages` gains the access pages, and one macro
beside `object_link` draws a name on an access page.

### Words

| Word | Means |
|---|---|
| way in | A host or an address, and a port, where a request from outside the cluster enters it: an Ingress host on a port its controller serves, a LoadBalancer or NodePort Service port, a host port, or a port of a pod on its machine's network |
| gate | Something between a way in and a pod: TLS, sign-in, or a NetworkPolicy |
| signs in through | A route sends each request to this object before the workload, read from the `authenticates through` edge |
| reaches | A way in leads to this workload |
| may | A binding grants it, a NetworkPolicy admits it, or no NetworkPolicy limits it |
| a binding grants it | A RoleBinding or ClusterRoleBinding names the account, or a group it is in, and the role holds the rule |
| follows | No binding grants it, and the account may do it by one of the steps above |
| runs as | The ServiceAccount a pod names |
| holds | What a pod mounts or claims: volumes, Secrets by name, devices, host paths, and its machine's network.  The app already uses the word for devices |
| may call | A call a NetworkPolicy admits, or that none limits: the `allowed to call` edge |

Page titles are the page's question: "What may reach each port from
outside the cluster", "What each account may do, and where", "What
may reach each port here" and "What each account may do here", "What
may reach it, and what it may reach" and "Who may change it", "What it
may do, and where" and "Who may run as it".

### Marks

| Mark | Means |
|---|---|
| filled dot | An object in the cluster puts this gate on this way, and its title names the object, such as the route's sign-in middleware |
| half dot | It puts the gate on some of the routes or pods the row stands for |
| open dot | No object visible to transect puts the gate there |
| declared dot | The gate is there and the API does not show it; a sentence the people who run the cluster wrote says so, shown with the `intent` mark |
| filled block | A binding grants the capability |
| outlined block | The capability follows from a grant |
| empty cell | No, or it does not apply |
| solid line | A route or a grant, which an object states |
| dashed line | A call a NetworkPolicy admits, or that none limits, as the section view draws callers |
| port | A small box on a workload's left edge, where the lines that reach that port end |
| outlined box | Something that follows from a grant |
| dashed box | A stated "none": "no way in", "nothing follows" |

The dotted line is not used: in the section view it means a guess, and
the lens draws no guesses.  The legend is one gray line under each
title, drawn with the marks themselves.  Every cell sits in a slot, as
the DaemonSet matrix's cells do, and every table and matrix keeps its
header row in view as the page scrolls (built before this plan,
c712b65).

### Sign-in and TLS the cluster's objects cannot show

A workload may sign its callers in itself, with OAuth in the
application, and nothing in the API says so.  An ingress controller
may redirect port 80 to 443 in its own configuration, which is not in
the API either.  Each gate therefore has a third state, declared:

- **Sign-in.**  A new intent annotation, `<prefix>/signs-in`, holds a
  sentence saying how the workload signs its callers in, such as
  `OAuth, in the app`.  It sits on the workload, or on a route object
  for that route alone, and is read like `why`: from the object, an
  overlay, or an Intent resource field `signsIn`.  A way in to a
  workload that declares it draws the declared dot, with the sentence
  on hover and beside the box in a drawing.  A filled dot, from an
  object that puts sign-in on the route, wins over a declared one.
- **TLS.**  The `IngressController` resource gains `entryPoints`, the
  ports its controller serves an Ingress on:

  ```yaml
  spec:
    entryPoints:
      annotation: example.io/entrypoints
      default: [web, websecure]
      ports:
        - {name: web, port: 80, redirectsTo: websecure}
        - {name: websecure, port: 443, tls: true}
  ```

  `annotation` is the Ingress annotation that names entry points,
  `default` the entry points an Ingress without it is served on, and
  each port says whether it serves the Ingress's TLS and whether the
  controller redirects it to another entry point.  A port that
  redirects to a TLS port draws the declared TLS dot, titled "the
  ingress controller redirects port 80 to 443, as IngressController
  {name} declares", since the redirect is the controller's
  configuration and not the Ingress's.  Without `entryPoints`, an
  Ingress is served on 80, and on 443 when it has `spec.tls`, the
  convention the Ingress API documents.

### Recognizing a distribution's load balancer listeners

Some distributions answer a LoadBalancer Service's ports with a
DaemonSet of their own whose pods hold host ports.  Those pods are the
Service's listeners, not ways in of their own.  A listener is
recognized two ways, and neither names a distribution in code:

1. its top owner has an owner reference to the Service;
2. a new cluster-scoped `LoadBalancerController` resource says how a
   listener names its Service, in the shape kind knowledge's `applies`
   uses:

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

A recognized listener's host ports fold into its Service's row, and
the Service's row counts the machines whose listeners answer.  A
`LoadBalancerController` that matches no workload gets the `Matched
False` condition on `/resources`, as other knowledge does.

### Who an applier applies as

An applier such as a Kustomization may apply as a ServiceAccount its
object names, not as its controller's account.  Kind knowledge's
`applies` gains `account`, the field that holds the name, read in the
applier's own namespace; with no such field, or an empty value, the
applier applies as its controller's account, found by controller
attribution.  The account an applier applies as is part of the
GitOps fields the trimmed watch keeps.

### Intent beside a fact

| Fact | Sentence |
|---|---|
| an account's bindings | the ServiceAccount's `why` |
| a workload on its machine's network, with a host port, a host path, or a privileged container | a new annotation, `<prefix>/host-access`, saying why, read like `why` |
| a workload that signs its callers in | `<prefix>/signs-in` |
| a port the ingress controller redirects | the `IngressController` that declares `redirectsTo` |

### Narrow windows

On an access page the card drops below the drawing or the matrices
below 1400px, where a drawing would otherwise zoom under 85%.

## Stages

Each stage ends in something to reload and look at, on the fixture
with `--fixture` and then once on the real cluster, names the files it
adds under `local/resources/`, untracked, by what they hold, and names
what it changes in `plans/00-design.md`.  Each stage grows the fixture
so that a text golden covers every page it adds, and the fixture crawl
in `cargo test` checks its links.

The order differs from the order of the pages in the designer's note,
for three reasons.  The capability model can only be checked on a
page, and a matrix is the plainest page to check against `kubectl auth
can-i --list`, so the model and the accounts matrix land together.
Ways in come before a workload's drawing, because the drawing's left
side is that workload's rows of Ways in.  And the declared gates come
right after Ways in, so no page ever draws two-state gates that a
later stage rewrites.

### 1. Access from RBAC, and the accounts matrix

`access/` reduces the four RBAC kinds to the six capabilities for
every subject, by namespace, and runs the closure of "follows",
keeping each first hop.  Table tests cover each form above: each verb,
`*` in each position, each subresource form, `resourceNames`, an
aggregated role, a RoleBinding to a ClusterRole, each group, a User,
and each step of the closure, including a chain of two hops and a
cycle.  The fixture loader reads Roles and RoleBindings, which it skips
today.

`/access/accounts` draws the matrix with three runs, each run's heading
saying what its rows share in words: accounts bound to a role that
grants every verb on every resource; accounts from which that follows,
with the heading naming the account they go through once; and every
other account granted one of the six.  Kubernetes' own controllers
fold into one line that opens to them: an account whose every binding
names a role labelled `kubernetes.io/bootstrapping: rbac-defaults`.
Two gray lines count the accounts bound only to roles that grant none
of the six and the ServiceAccounts no binding names.  One line says
whether any of the six reaches a group every account is in.  A
ServiceAccount's `why` shows beside its name.  The card describes the
lens and holds the "Not read" lines.

Fixture: `access.yaml` with ServiceAccounts in two namespaces, a
ClusterRole with every verb on every resource bound to one of them, an
operator's ClusterRole that may change workloads in its namespace, a
Role with `resourceNames` on Secrets, an aggregated ClusterRole, a
RoleBinding to `system:serviceaccounts:{namespace}`, a User subject,
and a ClusterRole that may patch the fixture's applier kind.  Golden:
`accounts`.  Look: the fixture's matrix, then the real cluster's
against `kubectl auth can-i --list --as` for three accounts: one bound
to every verb, an operator, and one limited by name.  Files: overlays
with `why` on the ServiceAccounts the cluster means to be powerful,
such as an ops console's.  Design: access from RBAC, the six
capabilities, and "follows".

### 2. Ways in

`/access` draws a row per way in, read from:

- Ingresses: a row per rule host and port its controller serves the
  Ingress on, from `entryPoints` or the default above.  TLS is filled
  when `spec.tls` names the host and the port serves TLS.  Sign-in is
  the Ingress's `authenticates through` edge, naming the object.  The
  backend Service's selector gives the workload.  Several routes for
  one host and port are one row, with a half dot where only some put a
  gate on it.
- Route kinds that kind knowledge declares through `routes` (a field
  this stage adds: the paths of a route's hosts and backends), read the
  same way.  A route kind with no `routes` knowledge is named in one
  gray line under the table, so a kind the lens did not read shows.
- Services of type LoadBalancer or NodePort: their ports, the addresses
  in `status.loadBalancer.ingress` counted as machines' addresses when
  they are, and the workload behind the selector.  The ingress
  controller's own Service is named in a line under the table, with
  the count of hosts that enter through it, and is not a row.
- Pod specs: a container port with `hostPort`, and every container port
  of a pod with `hostNetwork: true`, or "any port it opens" when it
  declares none, each counted over the machines its pods run on.
  Listeners recognized as above fold into their Service's row.

The NetworkPolicy gate is filled when a policy with `policyTypes:
Ingress` selects the workload's pods, half when it selects some, and
open when none does or when the pods are on their machine's network.
Rows that share their gates form a run, whose heading says them in
words ("Hosts with TLS and a NetworkPolicy, and no sign-in"); hosts
come first, then LoadBalancer Services, NodePort Services, host ports,
and pods on their machine's network.  A line under the table names the
namespaces that hold pods and no NetworkPolicy that limits calls in.
Rows link to the workload's overview until stage 4 adds its access
page.  The masthead gains its `access` link, and the switcher its two
chips.

`IngressController` gains `entryPoints`, and `LoadBalancerController`
is added, each validated with conditions on `/resources`.

Fixture: the existing Ingress on two entry points, a second Ingress
for the same host with sign-in through a middleware, a LoadBalancer
Service with a listener DaemonSet that labels name and one that an
owner reference names, a NodePort Service, a host port, and the
workload on its machine's network from the `hostNetwork` fix (0cf94bb).
Golden: `ways-in`.  Look: every Ingress host the cluster serves has a
row; the distribution's listener pods are not rows; a LoadBalancer
Service counts the machines that answer for it.  Files: `entryPoints`
on the cluster's `IngressController`, and a `LoadBalancerController`
for the distribution's service load balancer.  Design: ways in, the
gates, `routes`, `entryPoints`, and `LoadBalancerController`.

### 3. Declared gates

The declared dot, its hover sentence, and the `intent` mark beside it.
`signs-in` joins the intent annotations and the Intent resource as
`signsIn`, and `redirectsTo` on an entry point draws the declared TLS
dot on the port it redirects.  A run's heading says "declared" in
words where a gate is declared ("Hosts with TLS and a NetworkPolicy,
and sign-in the workload declares").

Fixture: `signs-in` on one workload behind an Ingress, and a redirect
on the fixture's `IngressController`.  Golden: `ways-in` changes.
Look: two workloads that declare a sign-in draw the declared sign-in
dot with their sentence; the port-80 rows of
redirected hosts draw the declared TLS dot.  Files: overlays with
`signs-in` on each workload that signs its callers in itself, and
`redirectsTo` on the `IngressController`.  Design: the declared state,
`signs-in`, and `redirectsTo`.

### 4. A workload's access

`…/access` on every workload that serves a port, and on every
workload a way in reaches, with the lens tab.  The drawing reuses the
section view's line routing (`map/section/lines/`) and its box macros
with a layout of its own, computed on the server as the section's is:

- **From outside**: the workload's rows of Ways in, each drawn from its
  box through the ingress controller and the Service to the port, with
  its gates as dots on the line.  With none, a dashed box says "no way
  in".
- **From inside**: who may call each port, from the allowed-to-call
  evaluation, by port, each caller dashed.  With no policy on the pods,
  one box says "any pod"; on its machine's network, one box says "any
  pod, at its machine's address", with the line "no NetworkPolicy
  applies to a pod on its machine's network".
- **Its ports**: the container ports, as port boxes on the workload's
  left edge, and the Services that select it.
- **Runs as**: `spec.serviceAccountName`, with one line on what its
  bindings grant, or "no binding names it".
- **Holds**: claims, Secrets by name (from volumes, projected volumes,
  `env[].valueFrom.secretKeyRef`, and `envFrom`), devices, host paths,
  its machine's network, host PID, and a privileged container or an
  added capability, with `host-access` beside the ones it explains.
  The pod trim keeps `securityContext.privileged` and
  `securityContext.capabilities.add` of each container, which it drops
  today; the commit message gives the store's memory before and after.
- **May call**: NetworkPolicies with `policyTypes: Egress` that select
  it, or "any address".
- **Who may change it**: the accounts a binding grants `patch` or
  `update` on its own resource in its namespace, with exec into its
  pods named on the same line; the controller that reconciles it, for
  a custom kind; the applier that applies it (the `reconciles from`
  edge) and who may change that applier; accounts
  bound to a role with every verb, in one box; accounts with the same
  line in one box that names them; and one outlined box counting the
  accounts for which changing it follows, which opens the namespace's
  access.

This stage also finishes the `hostNetwork` fix on the caller side: a
pod or namespace selector in a policy does not admit a pod on its
machine's network as a caller, since its traffic comes from its
machine's address, and an `ipBlock` that holds that address does.  The
target's "from inside" draws such callers as "the machines'
addresses", naming the workloads on a machine's network that call from
there.  A namespace whose every other workload a rule admits still
reads "every pod in" it.

On access pages the card drops below the drawing under 1400px.

Fixture: the storefront's access, with two ways in and a policy that
names its callers; the database, which nothing outside reaches; the
workload on its machine's network, with `host-access`; and a host path.
Goldens: `workload-access`, `no-way-in`, and `machine-network-access`.
Look: a workload with an Ingress, a database with no way in, and a
workload on its machine's network, each against `kubectl get
networkpolicy` and its pod spec.  Files: `host-access` overlays on the
workloads that use their machine's network.  Design: a workload's
access page, the link rule's exception, holds, and the caller side of
`hostNetwork`.

### 5. An account's access

A ServiceAccount's page becomes its drawing, left to right: the
workloads whose pods run as it; the account, with its `why`; each
binding's role and where it holds; what each role grants; and what
follows.  A grant that is one of the six carries a filled block and
the capability's name.  Every other rule is a template from its verbs
and kinds: "read" for `get`, `list`, and `watch`; "write" for the
verbs that change; "patch {Kind}s"; "read {count} Secrets here, by
name"; "every verb, every kind".  The follow boxes are the closure's
chain, a box per hop, ending at the role the last account is bound to.
With nothing following, a dashed box says "nothing follows" and which
of the six it may not do anywhere.  "Who may run as it" is one sentence
counting the other accounts that may run pods or change workloads in
its namespace, linking the namespace's access.

`/access/accounts/groups/{name}` and `/access/accounts/users/{name}`
draw the same for a group or a user; a group's left box names who is
in it when the API says (`system:serviceaccounts:{namespace}` is that
namespace's accounts), and otherwise that the API does not list its
members.

`applies.account` lands here: the GitOps knowledge reads it, and a
follow hop through an applier names the account it applies as.

Fixture: the accounts of stage 1, one of them with nothing following,
and the applier with an `account`.  Goldens: `account`,
`account-nothing-follows`, and `group`.  Look: an ops console's account
draws its chain through the applier; an operator limited by name draws
"nothing follows"; Group `system:masters` has a page.  Files:
`applies.account` on the applier's kind knowledge.  Design: an
account's page, the ServiceAccount page change, and `applies.account`.

### 6. A namespace's access

`/namespaces/{ns}/access`, with the namespace's lens tab, holds both
matrices:

- **Ports**: a row per way in, beside the workload it reaches, with
  the three gates, and a row "no way in" for a workload that serves
  ports nothing outside reaches.  Two more columns name the
  NetworkPolicies that limit calls in and say whether any limits calls
  out; on a pod on its machine's network the first says no
  NetworkPolicy applies to it.
- **Accounts**: one row per account that may act in the namespace,
  linked, with the six and "through".  Accounts with the same marks
  sit together with no rule between them.

One gray line under the accounts states the namespace's Pod Security
`enforce` label and what it allows, such as "carries no Pod Security
enforce label, so a pod here may be privileged, use its machine's
network, and mount its machine's paths".

Golden: `namespace-access`.  Look: a namespace with a workload on its
machine's network, one with a database behind policies, and one with
no policy at all.  Files: none.  Design: a namespace's access page.

### 7. Check the design, and stop

Read `plans/00-design.md` against the code: every page, word, mark,
resource, field, and annotation this plan names exists with that name,
and every one the code reads is in it.  Read every access page of the
real cluster for the words "can", "could", "risk", and "issue", and for
any color other than gray and the link's.  The orchestrator runs the
one full crawl.  Then stop for the owner's pass.  Look: the design and
the code agree.  Files: none.  Design: whatever disagreed.

## Decided

1. **Exec and the pod's token.**  "Follows" includes it.  An account
   that may exec into a namespace's pods may act as the
   ServiceAccounts those pods run as, through the mounted token, and
   the page states it as a fact with its chain.  It holds only where a
   pod in that namespace mounts a token, which it does unless
   `automountServiceAccountToken` turns it off.
2. **A sentence on a way in.**  A way in carries no intent sentence of
   its own.  The route's own `why`, on its page, is enough.

## Built differently

Stage 1 found these against the code and the API server's own rules:

- Kubernetes' own controllers fold by their bindings, not their roles:
  a binding Kubernetes ships carries `kubernetes.io/bootstrapping:
  rbac-defaults`, and so does `cluster-admin`, which a GitOps tool's
  own binding names too.  Folding by role would hide such a tool.
- A subject's name has no `*`; the API server matches subject names
  literally.  A User named `system:serviceaccount:{namespace}:{name}`
  is read as that ServiceAccount.
- The applier's account is the account RBAC lets write the applier
  kind's status, as controller attribution reads it, until
  `applies.account` lands in stage 5.
- A grant limited by name takes no step of "follows".

Stage 2:

- `routes` in kind knowledge is not built.  A route kind's hosts are
  not plain fields (Traefik's IngressRoute writes them in a match
  expression), its port is its gateway's, and the cluster holds no
  route objects to check a design against.  The gray line names every
  custom kind named `…Route` that holds objects, since none is read.
- A listener is recognized by its DaemonSet's owner reference to the
  Service, not its top owner's, since a DaemonSet's pods walk up to the
  DaemonSet.
- An Ingress that routes only to the workloads the routes' sign-in
  objects ask is the sign-in's own callback: a line, not a row, as the
  comp shows the sign-in's outpost.
- A LoadBalancer Service's row counts its listeners' machines, and,
  without listeners, the machines' addresses its status gives, or the
  addresses themselves when they are no machine's.

Stage 3:

- On a controller that redirects port 80 through a catch-all route at
  the lowest priority, as Traefik's `redirections` does, an Ingress
  served on port 80 wins over the redirect and answers in plain HTTP,
  so on such a controller every port-80 row is drawn unredirected.  `redirectsTo` declares a
  redirect that wins over an Ingress's own route; a cluster whose
  redirect does not leaves it out.

Stage 4:

- A Service that selects the workload and is on no way's path is drawn
  as its own box with its line to the ports it sends to, not named in
  the workload's box, so each name on the drawing is a link.
- "Who may change it" reads who may `patch`, `update`, or `delete` the
  workload's kind from an index the reduction builds once, beside the
  six capabilities, and not from the RBAC objects on each render.
- The line under the drawings that opens the namespace's access waits
  for stage 6, which draws that page.
- The commit gives no measured memory: the build brief keeps measured
  numbers out of commit messages.  The report holds them.

Stage 5:

- The ServiceAccount's old sections, "Run as it" and "Bound by", are
  the drawing's first and third columns, with each binding on its
  role's hover.
- A role's other rules read as "read", the verb, or "write" with the
  verbs, before the kinds; a sentence such as "hold its Lease" in the
  comps needs a meaning the API does not give, so the template names
  the kind instead.
- An account that may change appliers follows to the account each
  applier in those namespaces applies as, so a grant in a namespace
  that holds no applier takes no step.  `applies.account` takes a
  `default` beside its `field`, the account an applier applies as when
  the field is empty, for a tool whose default is not its controller's
  own account.

Stage 6:

- The link rule lives in the access pages' own `Look`, beside the
  pages, and not in `links::Pages`, which every other page reads.
- A namespace's accounts matrix folds Kubernetes' own components into
  a line, as the cluster's does, and its ports matrix names a workload
  with no declared port that a Service sends to as serving "a port a
  Service sends to".
- "Calls in limited by" names every policy that selects the pods for
  ingress, a deny with no rules among them, which the evaluation now
  keeps beside the policies that limit calls out.
- A workload's access tab ends with a line that opens its namespace's
  access, which stage 4 left for this stage.

Stage 7:

- The closing check loads each access page once on the fixture and
  once on the real cluster, with the pages the owner's five changes
  touched, and runs no crawl; the orchestrator runs the one full crawl
  after the reviews.
- The design named the edge kinds' row as part of the masthead and
  search's tie-break as kind then name; it now places the row above
  each picture and breaks a tie between kinds of one name by API group.
- The signs-in and host-access annotations hold a cluster's own
  sentences, so no example carries them; the examples show
  `entryPoints`, `LoadBalancerController`, and `applies.account`, which
  a project would ship.

## Not in this plan

- Access drawn on the cluster map, and a matrix of accounts against
  every namespace, which the owner set aside.
- Findings, scores, and counts of issues.
- Verifying access with a SubjectAccessReview.
- Whether an address answers from the internet.
- Secret pages, built from what names a Secret.  A Secret's name stays
  text, as the design says.
- An access tab on a machine, for the ports that answer on it.
- Hardening detail that does not grant host or extra access:
  `runAsNonRoot`, a read-only root, seccomp, and the mounted token.
- A sentence on a group or a user, which no object can carry.
- A CNI's own policy kinds, which the allowed-to-call evaluation does
  not read, as today.
- Vulnerability reports.
- A link from any object a GitOps tool applies straight to its source
  file in the repository, where today's Code section links only the
  directory.  Kustomize can record each object's file in an origin
  annotation (`buildMetadata: [originAnnotations]`), and a later plan
  would read it where it is present.
