---
name: operators
description: How the liken-sh operators keep their view of the cluster and their devices current. Covers the guards every Kubernetes watch loop needs, the scenarios a watch loop must pass in a test, status writes, conditions and the Events they post, timers, device traffic, the node label that keeps a device DaemonSet off a node, the replacement of a pod that the kubelet ended, the order of preference between Linux, Kubernetes, and code of the project's own, where an operator runs and where its workloads run, and the decision to watch through client-go, with references for the code of a watch and of a read from the cache. Use when writing or reviewing an operator's watch, informer, reconcile pass, read of a watched kind, status write, condition, Event, metric, backstop, timer, device polling, wait for a socket, replaced pod, device DaemonSet, RBAC, or deploy base. A new or changed condition, Event, or metric also changes the manual, so load the manuals skill with this one.
---

# Writing and reviewing operators

The rule for every component is in `AGENTS.md`, under "Keep state
current with events". Open the watch or subscription first, and then
read the whole state once. When the watch fails, open it again and
read the whole state again. A timer is correct only as a clock. This
skill has the detail that an operator needs to follow that rule.

In 2026-09, reviews of the hand-written watch loops found the same
faults again and again. Each loop needed several rounds of review
before it met the guards below. So the organization chose client-go's
reflector for every operator (see "Decision: every watch runs on
client-go"). Read this skill before you write, change, or review a
watch, and check the watch against every guard and scenario here.

## Which section to read

- **A watch, an informer, or a read of a watched kind:** "The guards
  of a watch loop", "Scenarios every watch loop must pass", and
  "Decision: every watch runs on client-go" with its references.
- **A status write or a reconcile pass:** "Status, echoes, and the
  pass".
- **A condition, an `Event`, or the reason of either:** "Conditions
  and Events".
- **A timer, a backstop, device traffic, or a wait for a socket:**
  "Timers and devices".
- **A pod that the kubelet evicted or rejected:** "A pod that the
  kubelet ended".
- **A lifecycle that the operator manages, or a workaround for an
  upstream fault:** "Linux first, then Kubernetes, then code of our
  own".
- **A device `DaemonSet` or its node affinity:** "A device DaemonSet
  stays off a node labeled none".
- **RBAC, a deploy base, or the scope of a kind:** "An operator runs
  in liken-system and watches every namespace".
- **A metric:** "Metrics".
- **Any change that a person sees:** "The manual changes with the
  code".

## The guards of a watch loop

A watch loop first lists the collection, and the list returns a
`resourceVersion`. Then the loop opens a watch from that version. The
API server accepts the watch with a `200` and sends each change as an
event. Later the server or the network closes the watch, and the loop
must decide whether to resume or to list again, and how long to wait
first. The guards cover those decisions, in that order.

1. **Resume, do not list.** List first, then watch from the list's
   `resourceVersion`. Set `allowWatchBookmarks=true`, so that the
   version moves forward while no object changes. When a watch closes,
   open the next one from the last `resourceVersion` that it
   delivered. Do not list again. A watch with no `resourceVersion`
   first sends every object as it is now, so each such open costs as
   much as a list.
2. **List again only for a 410, and only once.** A `410 Gone` can
   arrive as the HTTP response or as an `ERROR` event on the stream.
   When it arrives, list again at once. If the watch that opens from
   that new list also gets a `410`, wait out the backoff before you
   list. A `410` that ends a watch that ran for a second or longer
   counts as a first `410`, so the loop lists at once.

   Any other `ERROR` event, and an event whose object or line does not
   decode, needs a different response: wait out the backoff, then
   list. On every error, close the stream at once. The server keeps a
   watch open for minutes, and a loop that reads the stream to its end
   loses every event in that time.
3. **Decide the wait by how long the watch ran.** Measure the watch's
   life from the moment that the `200` arrived, not from the request.
   A slow dial, a slow refusal, and a slow `410` response are not
   watches that ran. A watch that closes less than a second after its
   `200` is a failure, whatever it delivered. So the backoff applies,
   and it grows to its cap. A watch that ran for a second or longer
   resets the backoff, even when it ended with an error.

## Scenarios every watch loop must pass

Test a loop against a scripted fake API server that can keep a stream
open, respond slowly, and cut a connection. Each row gives a scenario
and the correct result.

| Scenario | Correct result |
|---|---|
| A watch runs for minutes and closes cleanly | Reopen at once from the last version, with no list |
| A watch closes in under a second | Wait out the backoff, then reopen from the last version |
| A `410` response, then a watch that works | One list at once |
| A `410` on every version | Lists no more often than the backoff allows |
| A `410` after a watch that ran a second | One list at once |
| A non-410 `ERROR` event, stream kept open | Close at once, wait, list |
| An object or a line that does not decode, stream kept open | Close at once, wait, list |
| A connection reset after a watch that ran | Resume from the last version, with no list |
| A `403`, then the permission is granted | Back off while the server refuses, then watch normally |
| A dead server (the dial fails) | The backoff grows to its cap |
| A slow `200`, then a normal stream for minutes | Every event delivered |
| A slow refusal or a slow `410` | The backoff still grows |

## Status, echoes, and the pass

- **Write status only on a change.** Some status fields change on
  every pass, such as a timestamp or a field that the CRD schema
  drops. A field like that makes every pass write, and each write
  wakes every watcher. Compare the new status with the copy that the
  API server stores, not with what the code meant to write.
- **Do not wake for your own echo.** The watch delivers each write
  that the operator makes. If the operator wakes for that echo, it
  runs a second pass that finds nothing to do. `OwnWrite` in
  `liken/kubernetes` remembers the `resourceVersion` that the API
  server returned for the last write. The watch can deliver the echo
  before the response to the write arrives, so `OwnWrite` makes the
  watch wait until it has recorded the response. An update at any
  other version is a change by another writer, and it wakes the loop.
- **Scope each watch to what the process owns.** A DaemonSet pod that
  watches every object of a kind wakes on every write from every other
  node. A CRD can declare `selectableFields`, so that each pod lists
  and watches by field selector, for example `status.node=<machine>`.
- **A field-selected list needs the `list` verb.** RBAC allows a list
  or a watch against `resourceNames` only when the request selects
  that one name. The Role must grant `list` as well as `watch`.
- **Grant only the verbs that the code uses.** A grant that no code
  uses lets a stolen token read more. A person who reads the manifest
  also takes the grant as a statement of what the code does.
  `display-operator/plans/open-problems/rbac-grants-watches-that-no-code-opens.md`
  records the grants that remain. No unit test reads RBAC, so a run on
  the `lab` fleet is the proof that a removal is safe.
- **The Kubernetes API server refuses requests while a CRD changes.**
  During a rollout that changes a CRD, the API server reinitializes
  the storage of that kind. For a short time it responds to requests
  for the kind with `429 storage is (re)initializing`. A loop that
  gets this response from the API server must back off and retry.
  A `429` from any other HTTP server has other causes, and this rule
  does not cover it.
- **Read once per pass, not once per object.** A pass that sends a GET
  for each claim or pod that it creates costs one request per object
  on every pass. One list per pass gets the same information.

## Conditions and Events

The rule and the reasons are in root plan 78. In short:

| Channel | Records | Question |
|---|---|---|
| Condition | what is true now, for `kubectl wait` and controllers | "What is true now?" |
| `Event` | a transition or an action, for a person, deleted after an hour | "What happened recently?" |
| Log line | every attempt, retry, and detail | "What did the program do?" |

- **What posts an `Event`.** Each condition transition posts one, with
  the condition's reason and message. A transition is a new condition,
  a new status, or a new reason. A new message alone posts nothing.
  Each action that changes no condition also posts one: a pod created
  again, a reboot requested, a key minted, a device that appears or
  vanishes. Nothing else posts an `Event`: no reading, key press,
  guide step, or retry of a retry loop. A retry loop posts one `Event`
  at its first failure, and one at the first success after a failure.
  The attempts and the waits between those two post no `Event`, and
  they need no reason of their own. Write them to the log.
- **A new condition type.** `conditions.Set` counts a condition that
  is not in the list as a transition. So when a release adds a
  condition type, each object that exists gets the condition on the
  first status write after the upgrade, and it posts one `Event` for
  it. A person who upgrades sees one `Event` on every object of the
  kind.
- **Warning or Normal.** Warning means a person may need to act.
  Normal means an expected transition or an action the component
  took.
- **Reasons and messages.** A reason is UpperCamelCase and specific:
  `ReleaseRolledBack`, not `Failed`. When the `Event` is about an
  object that another component owns, such as a `Pod`, the reason
  starts with the prefix of the component that posts it. A message
  includes the values, and never a token. Write it in the voice of
  `brand/voice.md`.
- **Where a reason goes.** Each component lists the reasons of the
  `Event`s that no condition gives in one file, such as
  `audio-operator/eventreasons.go` or `firmware-operator/events.go`.
  Put the reason of a new `Event` of that kind there. The reason of a
  condition goes where the component already declares the reasons of
  its other conditions. In `audio-operator` and
  `firmware-operator`, the reasons of a condition are beside the code
  that sets the condition, such as `audio-operator/sinkstatus.go`. In
  `display-operator` and `equipment-operator`, `reasons.go` lists the
  reasons of conditions and of `Event`s together.
- **The writer.** Build one `events.Recorder` in `main` with
  `events.New(ctx, client, component, events.Options{})`, and post with
  `Normal` and `Warning`. A write never blocks or fails a pass. The
  recorder queues each write. It sends a failed write again twice, 10
  seconds apart, and it patches the count of a repeat within 10
  minutes. A nil recorder posts nothing. An `Event` about a
  cluster-scoped object goes in the `default` namespace. Grant
  `create` and `patch` on `events`. The numbers are the constants
  `attempts` and `retryWait` in `kubernetes/events/recorder.go`, and
  `window` in `series.go`.
- **The shutdown.** The recorder writes from its own goroutine, so the
  last write of an elected operator can be an `Event`. Pass
  `events.New` a context that outlives the last pass, such as
  `context.Background()`. After the last pass, and after every other
  writer has stopped, call `Recorder.Shutdown` with a context of
  `events.ShutdownTimeout`. It sends each queued `Event` once, and it
  returns after the last request has returned. Call `Lead.StepDown`
  only when `Shutdown` returns nil. An error means that a write can
  still be in flight, so the process exits and keeps the `Lease`.
- **The condition type.** Declare conditions as `conditions.Condition`,
  with an alias where a component names its own type. Set one with
  `Recorder.SetCondition(object, &list, next, bad)`, where `bad` is the
  status that needs a person. Some passes compose a whole status and
  write it later. Such a pass calls `conditions.Set` while it composes
  the status. After the write succeeds, it calls `Recorder.Transition`
  for each transition. So a write that the API server rejects posts
  nothing (`observatory-operator/status.go`).
- **The test.** Mount `eventstest.Events` in front of the fake API
  server with `Around`, and run in a `synctest` bubble. Assert the
  type, reason, and message of each `Event` that an object received
  (`About`). `Refuse` makes the next writes fail, and `Expire` plays
  the TTL. The recorder sends a failed `Event` again after 10 seconds
  of the bubble's clock, so a test that uses `Refuse` sleeps past that
  time before it asserts.

## Timers and devices

- **Name what a kept timer covers.** The section "Keep state current
  with events" of the root `AGENTS.md` has the rule. A timer that is a
  clock (a deadline, a TTL, a backoff, a heartbeat) needs only a
  correct comment. A timer that reads state again needs a comment at
  the timer, in one of these cases:
  - A backstop tick for a subscription. The comment names each
    failure that the tick covers, and why no event covers it.
  - A source that publishes no event, such as a file behind an HTTP
    URL. The timer is the only way to see a change, and the comment
    says why the source has no event. `people-operator`, which reads
    each picture source again on a timer, is in this case.
- **Traffic to a device is a cost even when it is cheap.** A TV
  switched its input while an operator scanned its CEC bus each
  minute. Send a device a query only when a person's action or an
  announcement needs the result. Send at most one query per event. When
  several callers need the same fact, let them share one read in
  flight.
- **A lost event starts a full read.** A uevent socket that returns
  `ENOBUFS` has dropped events. Wake the pass at once. Do not wait for
  the next event or the backstop.
- **Signal a child through its process handle.** A process can reuse
  the pid of a process that ended, so a signal to a bare pid can reach
  the wrong process. `os.Process` has a pidfd, and a signal after the
  process was reaped returns `ErrProcessDone`.
- **Wait for a socket with an inotify watch, then one look.** A
  program that waits for another program's socket first opens an
  inotify watch on the socket's directory. Then it looks once for the
  socket. So it does not miss a socket created between the two steps.
  `AGENTS.md` gives this pattern under "Keep state current with
  events". A loop that tries to connect, sleeps, and tries again is a
  timer that reads state again. A loop of this kind that remains has a
  record in its component's `plans/open-problems/`, such as
  `wireplumber-polls-for-the-pipewire-socket.md` in `audio-operator`.
  For an example of a directory watched with inotify, see `volumeSwaps` in
  `audio-operator/volumeswaps.go`.

## A pod that the kubelet ended

The kubelet can evict a pod for pressure on the node, reject it at
admission (such as `OutOfcpu`), end it in a graceful node shutdown, or
preempt it. No restart policy starts a pod again after any of these.
So the operator that owns the pod creates the next one, through the
rule in `kubernetes/endedpods`.

The next pod can be rejected again. A claim or `spec.nodeName` can pin
it to the same node, and after a rejection at admission no taint keeps
it away. In the work of 2026-11-19, a review found that a pod that the
operator replaced at once on that node was rejected again each time
the watch delivered an event. So the next pod waits
`endedpods.FirstWait` after the ended pod was created. The wait doubles
for each earlier ended pod, up to `endedpods.MaxWait`. The wait counts
from the creation of the ended pod, so a pod that ran longer than its
wait is replaced at once. Only a pod that ends young waits.

- **The count.** The operator stores the count of earlier ended pods
  in an annotation on the pod, in the operator's own API group, such
  as `library.liken.sh/tries` (`endedpods.Tries` and
  `endedpods.SetTries`). The count is not in memory, so an operator
  that restarts waits as long as the one before it.
- **The `Event`.** A replacement posts an `Event` only when
  `Pod.FirstOfLoop` is true. So a loop of rejections posts its first
  end, and not each retry. Put the kubelet's reason and message in the
  `Event`'s message with `endedpods.Words`.
- **What stays in the operator.** Each operator fills an
  `endedpods.Pod` from its own pod type. A rule that only one operator
  needs stays in that operator, such as the limit of tries in
  `firmware-operator`.

## Linux first, then Kubernetes, then code of our own

The rule is in `AGENTS.md`, under "Use what Linux and Kubernetes
already provide". Before an operator manages a lifecycle itself, look
for a mechanism in the kernel or in Kubernetes that does the work,
such as `ttlSecondsAfterFinished` on a `Job`. The comment at a
workaround for an upstream fault also says when the workaround can
go, so that a later session can remove it when upstream ships the
fix.

## A device DaemonSet stays off a node labeled none

A device operator's `DaemonSet` makes a pod on every node, and the pod
claims the node's hardware. On a node with no such hardware, the claim
matches no device, and the pod stays `Pending` for good. So every
such `DaemonSet` has this node affinity in its own `deploy/`
manifest, and a person keeps the pod off a node with one label:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: <operator group>/<hardware>
              operator: NotIn
              values: ["none"]
```

- **The key.** Use one key for each kind of hardware, in the
  operator's own API group, with the value `none`, such as
  `audio.liken.sh/sound-card`. A new `DaemonSet` gets a new key of the
  same form. To see the keys in use, search the `deploy/` manifests
  for `operator: NotIn`.
- **The default.** `NotIn` also matches a node that has no such label,
  as the Kubernetes page on set-based requirements says. So with no
  label, the `DaemonSet` makes a pod on every node, and the claim
  decides where the pod can start. Use one term with one expression. A
  second term is ORed with the first, and it lets the pod back onto a
  labeled node. A second expression narrows where the pod runs.
  `nodeSelectorTerms` is an atomic list, so when a cluster owner
  patches in a node affinity, the patch replaces the term. The guide
  says so.
- **The label.** A person declares the label in the node's `Machine`,
  in `spec.nodeLabels`. That field rejects only keys that start with
  `liken.sh/`, so it accepts a key in an operator's subdomain. A
  `Machine` accepts such a key only from `liken` 2026.09.28-002 on. An
  older release rejects it, so an install guide must also show how to
  set the label with `kubectl label`, for a cluster on an older
  release. The machine operator applies the label, and it removes the
  label when the key leaves the spec. A person can also set the label
  with `kubectl label node`, and `liken` leaves in place a label that
  it did not declare. When the label changes, the `DaemonSet`
  controller deletes or adds the pod.
- **The test.** A test in the operator reads the `DaemonSet` from
  `deploy/` and checks that it has the one term with its key, such as
  `audio-operator/nodeaffinity_test.go`.
- **The guide.** The install guide has the section "Keep the pods off
  nodes with no ...", in the same words as the guides of the other
  operators.

## An operator runs in liken-system and watches every namespace

A person who installs `liken` finds every operator in one place, and
the cluster owner chooses the namespaces of their own objects. So
every operator has the same layout:

- The operator's `Deployment` and `ServiceAccount` are in
  `liken-system`. Its RBAC is a `ClusterRole` and a
  `ClusterRoleBinding`, and it watches every namespace.
- The deploy base creates no namespace other than `liken-system`, and
  never sets the namespace of the owner's objects.
- The workloads of a namespaced object (pods, `Service`s,
  `ConfigMap`s, `Job`s, `ResourceClaim`s) run in that object's
  namespace, never in `liken-system`. So the owner's `ResourceQuota`,
  `NetworkPolicy`, Pod Security level, and RBAC grants in that
  namespace apply to them, and a reference by name in a spec resolves
  in that namespace.
- A kind is cluster-scoped only when it exists once per cluster or
  stands for one piece of hardware, such as `Receiver`, `Television`,
  `Keymap`, or `Person`. A kind that a person declares as a unit of
  use, such as `Library`, `Player`, or `Telescope`, is namespaced.
- Two namespaces can have objects with the same name. Key the
  operator's memory by namespace and name, or keep one copy of the
  state for each namespace, as `observatory-operator` does
  (`namespaces.go`).

Plan 15 of `observatory-operator` moved that operator to this layout
and records the reasons.

## Decision: every watch runs on client-go

Decided on 2026-09-27: every operator watches the API server through
client-go's reflector, in a lean form, and has no watch loop of its
own. The reason is maintenance. Before the decision, eight
repositories each had one hand-written loop. The loops were about
2,470 lines in all (1,530 of code), with 4,900 lines of tests for the
loops alone. The loops drifted apart, and each fix went into one
repository at a time. Upstream maintains and tests the reflector, so
the project does not have to maintain those loops. The rejected
alternative was one shared Go module with a loop that the project
wrote. That loop met the guards exactly and used no more memory, but
the project would have had to maintain the loop, its fake API server,
and a release that every operator must take.

Every operator watches through `kubernetes/informer`, in the Go
module `kubernetes/` at the top of the repository. That module also
has the client, the reads of a store, and the memo.
`liken/kubernetes/watch` has what the operators of `liken` add: the
wake handlers and the reads of a store. The CSI drivers run
client-go's informer with the typed clientset
(`git-csi-driver/listwatch.go`, `per-node-csi-driver/sweep.go`).

The reflector does not meet the guards above exactly. The
organization accepts the differences, because none of them loses an
event. The guards and the scenarios above still describe what any
watch must do, and a reviewer checks an operator's use of the
reflector against them. These each break a guard: a handler that
drops an object, a selector that does not follow its source, and a
loop that the operator writes around the informer.

- Read `references/client-go-watch.md` before you write or review an
  informer, its handlers, its imports, or the test of its handlers. It
  has the lean form, its measured cost, the differences between the
  reflector and the guards, and the code of a watch.
- Read `references/cache-reads.md` before you write or review a read
  of a watched kind, the memo of the versions that an operator wrote,
  or a status write that can get a `409`.

## Metrics

A long-running process serves Prometheus metrics on a port of its
own. Root plan 65, `plans/completed/65-prometheus-metrics.md`, is the
contract for the names, the labels, and the port, and it lists the
components that serve none. Read it before you add or rename a
metric, and copy a component that already serves metrics when you add
the first one to a component.

A new metric also needs a test that scrapes it, and a row in the
component's `docs/content/docs/reference/metrics.md`. Check the
dashboards and the `PodMonitor` in the component's
`deploy/monitoring/` for a panel or a rule that should use it.

## The manual changes with the code

A commit that changes what a person installs, runs, reads, or sees
changes the component's manual in the same commit. Load the `manuals`
skill before that commit. It lists the pages that each kind of change
makes wrong, such as a file of `deploy/`, a container, a metric, a
flag, a printer column, a condition, or the reason of an `Event`. It
also lists the targets that generate the reference pages and the
skills again.
