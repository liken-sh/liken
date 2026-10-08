# 15, The operator watches every namespace

Proposed on 2026-10-08. Built on 2026-10-08 with design (a), all four
steps, and tested against the fake API server, which now serves lists
and watches of every namespace. The drill on liken-1 has not run yet.

`observatory-operator` runs in its own `observatory` namespace and
watches that namespace alone. Every other `liken` operator runs in
`liken-system`, watches every namespace with a `ClusterRole`, and runs
its workloads in the namespace of the object that asks for them. This
plan moves `observatory-operator` to that pattern, and writes the
pattern down in the `operators` skill so the next operator starts from
it.

## The problem

A person who installs `liken` operators finds nine of them in
`liken-system`. `observatory-operator` is the exception, so it is the
one a person looks for in another place, and the one whose
`kustomization.yaml` creates a namespace.

`media-operator` and `library-operator` let the cluster owner choose
where a `Library` or a `Player` lives, and run each workload beside
the object. The namespace then carries the owner's controls for those
workloads: a `ResourceQuota`, a `NetworkPolicy`, a Pod Security level,
and the RBAC grants that let a person edit the objects.
`observatory-operator` puts the owner's objects and its own pod in one
namespace, so the owner cannot grant a person the observatory without
also granting the operator's `Deployment`, and cannot run two
observatories, such as a simulator inventory beside the real one,
under one operator.

The rest of the design is already consistent. A kind that exists once
per cluster or names one piece of a machine's hardware is
cluster-scoped: `Receiver`, `Television`, `CECBus`, `Keymap`,
`MediaPreferences`, and `Person`. A kind that a person declares as a
unit of use is namespaced: `Library`, `Player`, `Play`. The 20 kinds of
the `observatory.liken.sh` group reference each other by name and
together make one observatory, which a person declares the way they
declare a `Library`. So they stay namespaced.

## The rule

The `operators` skill gets this rule, with the reasons above:

- An operator runs in `liken-system` and watches every namespace,
  with a `ClusterRole` and a `ClusterRoleBinding`.
- An operator's `kustomization.yaml` creates no namespace other than
  `liken-system`, and never sets the namespace of the owner's objects.
- A namespaced object's workloads (pods, `Service`s, `ConfigMap`s,
  `Job`s, `ResourceClaim`s) run in that object's namespace. A reference
  by name in a spec resolves in that namespace.
- A kind is cluster-scoped only when it exists once per cluster or
  names one piece of hardware.

## The design

### One observatory for each namespace

Today the operator holds one tree (`watch.go`, `snapshot`) and keys
its memory by bare name: `runners`, `faults`, `sites`, the server
connections, the claims, the runs, the activity, the lock memo, and
the driver memo. Two namespaces that each hold a `Telescope` named
`east` would collide in every one of those maps.

Two ways to fix it:

- **(a) One operator for each namespace.** The process keeps one set
  of watches over every namespace. Each namespace's tree reads only
  that namespace's objects, and the process runs one copy of today's
  `operator` struct for each namespace that holds an object of the
  group or an object the operator created. Each copy keeps its maps
  keyed by bare name. A copy starts with its namespace's first
  object, and stops when the last one is gone.
- **(b) Qualify every key.** One operator, one tree that holds every
  namespace, and every map keyed by `namespace/name`.

We chose (a). The reconciler, the runners, the locks, and the
procedures stay as they are, because each still reads one namespace's
tree, and the tests of one observatory keep their shape. A dome lock
or a mount lock never crosses a namespace, and (a) makes that true by
construction. (b) touches every lookup in about 20,000 lines and makes
each test name a namespace for no gain.

### Where each object goes

| Object | Today | After |
|---|---|---|
| The operator's `Deployment`, `ServiceAccount` | `observatory` | `liken-system` |
| RBAC | `Role` in `observatory` | `ClusterRole` and `ClusterRoleBinding` |
| The 20 kinds | `observatory` | any namespace the owner chooses |
| Device pods, INDI servers, guider pods, their `Service`s, `ConfigMap`s, `ResourceClaim`s | `observatory` | the namespace of their `Telescope` |
| A `job` action's `Job` | "the operator's namespace" | the namespace of the resource whose procedure runs it |
| `Event`s | `observatory` | the namespace of the object |

`serviceHost` already takes a namespace, so the INDI and PHD2
endpoints in status stay correct. The CRD descriptions that say "the
operator's namespace" change to "the resource's namespace", and the
generated reference pages follow.

A `Job`'s pod meets the restricted Pod Security level today, and the
device and guider pods do too, so a namespace with any Pod Security
level runs them. The comment in `jobs.go` that says the namespace
states no level becomes true of any namespace.

### The operator's own namespace

`main.go` reads `POD_NAMESPACE` today to choose the one namespace it
watches. After this plan the operator needs no namespace of its own,
so the variable goes away. The operator never runs a workload in
`liken-system`. Each log line names its namespace after the
operator's name, because two namespaces can use the same names.

## The migration

The deploy artifact creates the `observatory` namespace today, and a
cluster that applies it through Flux with pruning deletes what the
artifact no longer lists. If the next artifact drops `namespace.yaml`,
Flux deletes the `observatory` namespace and every object in it.

So the step that drops `namespace.yaml` also needs, in each cluster
that applied the old artifact, the namespace to move into the
cluster's own manifests first, or to carry the annotation
`kustomize.toolkit.fluxcd.io/prune: disabled` before the bump. The
guide for upgrading says this, and the release notes name it.

The old `Role` and `RoleBinding` in `observatory` are pruned with the
old artifact. Pods that the old operator created carry owner
references to their `Telescope` and the operator's labels, so the new
operator adopts them where they are, because the namespace does not
change.

## Steps

1. Split the stores by namespace and run one site for each namespace
   (design (a)). Tests: two namespaces that each hold an observatory
   with the same names run apart; a site starts on the first
   `Observatory` and stops on the last delete; a `job` action's `Job`
   lands in the resource's namespace.
2. Move the deploy: `ClusterRole`, `ClusterRoleBinding`, the
   `Deployment` and `ServiceAccount` in `liken-system`, no
   `namespace.yaml`, no `POD_NAMESPACE`. Update `deploy_test.go`.
3. Update the manual: the install guide (choose a namespace and
   create it), the upgrade note about pruning, the CRD descriptions,
   the example's header, and `AGENTS.md`.
4. Add the rule to the `operators` skill.

## The drill

On liken-1, which holds a simulator inventory in `observatory`: keep
the namespace from pruning, bump the operator, confirm the operator
runs in `liken-system` and reads the inventory, then create a
`Reservation` and confirm the device pods and the INDI server start in
`observatory` and the telescope reaches `Ready`. Then apply the
simulators again in a second namespace and run both at once.
