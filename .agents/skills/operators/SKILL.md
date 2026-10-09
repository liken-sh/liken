---
name: operators
description: How the liken-sh operators keep their view of the cluster and their devices current. Covers the three guards every Kubernetes watch loop needs, the scenarios a watch loop must pass in a test, status writes, conditions and the Events they post, timers, device traffic, the node label that keeps a device DaemonSet off a node, where an operator runs and where its workloads run, the decision to watch through client-go with the shape of the reference port, and the manual pages that each kind of change must update. Use when writing or reviewing an operator's watch, reconcile pass, status write, condition, Event, backstop, timer, device polling, device DaemonSet, RBAC, or deploy base, and before committing any change to an operator or a CSI driver.
---

# Writing and reviewing operators

The organization's rule is in `AGENTS.md`, "Keep state current with
events": open the watch or subscription first, read the whole state
once, and open it again and read again when it fails. A timer is
correct only as a clock. This skill carries the detail that the rule
needs in an operator.

In 2026-09, reviews of seven hand-written watch loops found the same
faults again and again, and each loop needed several rounds to meet
the guards below. The organization then chose client-go's reflector
for every operator (see the decision at the end). Read this skill
before you write, change, or review a watch, and check it against
every guard and scenario here.

## The three guards of a watch loop

1. **Resume, do not list.** List first, then watch from the list's
   `resourceVersion`, with `allowWatchBookmarks=true` so that the
   version moves while nothing changes. When a watch closes, open the
   next one from the last `resourceVersion` it delivered. Do not list
   again. A watch with no `resourceVersion` replays every object first,
   so each such open costs as much as a list.
2. **List again only for a 410, and only once.** A `410 Gone`, as the
   HTTP response or as an `ERROR` event, lists again at once. If the
   watch that opens from that fresh list also gets a 410, wait out the
   backoff. A 410 that ends a watch which ran a second or longer
   counts as a first 410. After any other `ERROR` event, and after an
   event whose object or line does not decode, wait out the backoff,
   then list. On any error, close the stream at once: the server holds
   a watch open for minutes, and a loop that reads the stream to its
   end loses every event in that time.
3. **Decide the wait by the watch's life.** A watch that closes less
   than a second after the server accepted it is a failure, whatever
   it delivered, so the backoff applies and grows to its cap. A watch
   that ran a second or longer resets the backoff, even when it ended
   with an error. Measure the life from the moment the `200` arrived,
   not from the request: a slow dial, a slow refusal, and a slow `410`
   answer never ran.

## Scenarios every watch loop must pass

Test a loop against a scripted fake API server that can hold a stream
open, answer slowly, and cut a connection. Each scenario names the
correct result.

| Scenario | Correct result |
|---|---|
| A watch runs for minutes and closes cleanly | Reopen at once from the last version, with no list |
| A watch closes in under a second | Wait out the backoff, then reopen from the last version |
| A `410` response, then a watch that works | One list at once |
| A `410` on every version | Lists bounded by the backoff |
| A `410` after a watch that ran a second | One list at once |
| A non-410 `ERROR` event, stream held open | Close at once, wait, list |
| An object or a line that does not decode, stream held open | Close at once, wait, list |
| A connection reset after a watch that ran | Resume from the last version, with no list |
| A `403`, then the permission is granted | Back off while refused, then watch normally |
| A dead server (the dial fails) | The backoff grows to its cap |
| A slow `200`, then a normal stream for minutes | Every event delivered |
| A slow refusal or a slow `410` | The backoff still grows |

## Status, echoes, and the pass

- **Write status only on a change.** A status field that changes on
  every pass, such as a timestamp or a field the CRD schema drops,
  makes every pass write, and the write wakes every watcher. Compare
  against what the API server stores, not against what the code
  meant to write.
- **Do not wake for your own echo.** A watch delivers each write the
  operator makes, and a wake for it runs a second pass that finds
  nothing to do. `liken/kubernetes`'s `OwnWrite` remembers the
  `resourceVersion` the API server answered for the last write, and
  holds it until the answer arrives, because the watch can deliver the
  echo first. An update at any other version is another writer's
  change and wakes the loop.
- **Scope each watch to what the process owns.** A DaemonSet pod that
  watches every object of a kind wakes on every other node's writes.
  A CRD can declare `selectableFields`, so each pod lists and watches
  by field selector, for example `status.node=<machine>`.
- **A field-selected list needs the `list` verb.** RBAC authorizes a
  list or a watch against `resourceNames` only when the request
  selects that one name, and the Role must grant `list` as well as
  `watch`.
- **A CRD change reinitializes its storage.** During a rollout that
  changes a CRD, the API server answers `429 storage is
  (re)initializing` for a short time. A loop must back off and retry.
- **Read once per pass, not once per object.** A pass that GETs each
  claim or pod it stands costs one request per object on every pass.
  One list per pass answers the same questions.

## Conditions and Events

Root plan 78 holds the rule and the reasons. In short:

| Channel | Holds | Question |
|---|---|---|
| Condition | what is true now, for `kubectl wait` and controllers | "What is true now?" |
| `Event` | a transition or an action, for a person, deleted after an hour | "What happened recently?" |
| Log line | every attempt, retry, and detail | "What did the program do?" |

- **What posts an `Event`.** Each condition transition posts one, with
  the condition's reason and message. A transition is a new condition,
  a new status, or a new reason; a new message alone posts nothing.
  Each action that changes no condition posts one: a pod created
  again, a reboot requested, a key minted, a device that appears or
  vanishes. Nothing else posts one: no reading, key press, guide step,
  or retry of a retry loop. A retry loop posts its first failure and
  its recovery.
- **Warning or Normal.** Warning means a person may need to act.
  Normal means an expected transition or an action the component
  took.
- **Reasons and messages.** A reason is UpperCamelCase and specific:
  `ReleaseRolledBack`, not `Failed`. A reason on an object another
  component owns, such as a `Pod`, takes the component's prefix. A
  message holds the values, never a token, in the voice of
  `brand/voice.md`. List a component's reasons in one file.
- **The writer.** Build one `events.Recorder` in `main` with
  `events.New(ctx, client, component, events.Options{})`, and post with
  `Normal` and `Warning`. A write never blocks or fails a pass: the
  recorder queues it, sends a failed write again twice, 10 seconds
  apart, and patches the count of a repeat within 10 minutes. A nil
  recorder posts nothing. An `Event` about a cluster-scoped object goes
  in `default`. Grant `create` and `patch` on `events`.
- **The condition type.** Declare conditions as `conditions.Condition`,
  with an alias where a component names its own type. Set one with
  `Recorder.SetCondition(object, &list, next, bad)`, where `bad` is the
  status that needs a person. A pass that composes a whole status and
  writes it later calls `conditions.Set` while it composes, and
  `Recorder.Transition` for each transition after the write lands, so
  a refused write posts nothing (`observatory-operator/status.go`).
- **The test.** Mount `eventstest.Events` in front of the fake API
  server with `Around`, run in a `synctest` bubble, and assert the
  type, reason, and message of each `Event` an object received
  (`About`). `Refuse` refuses the next writes, and `Expire` plays the
  TTL. The recorder sends a refused `Event` again after 10 seconds of
  the bubble's clock, so a test that refuses one sleeps past that
  before it asserts.

## Timers and devices

- **Name what a kept timer covers.** A backstop tick needs a comment
  at the timer that names each failure it covers and why no event
  covers it. A timer that is a clock (a deadline, a TTL, a backoff, a
  heartbeat) needs only a correct comment.
- **Traffic to a device is a cost even when it is cheap.** A TV
  switched its input while an operator scanned its CEC bus each
  minute. Ask a device only when a person's action or an announcement
  needs the answer, bound each question to one per event, and share
  one read in flight between every caller that needs the same fact.
- **A lost event wakes a full read.** A uevent socket that answers
  `ENOBUFS` dropped events. Wake the pass at once instead of waiting
  for the next event or the backstop.
- **Signal a child through its process handle.** A signal to a bare
  pid can reach a later process that reused it. `os.Process` holds a
  pidfd, and a signal after the process was reaped returns
  `ErrProcessDone`.

## A device DaemonSet stays off a node labeled none

A device operator's `DaemonSet` makes a pod on every node, and the pod
claims the node's hardware. On a node with no such hardware the claim
matches no device, and the pod stays `Pending` for good. Every such
`DaemonSet` ships this node affinity in its own `deploy/` manifest, so
a person keeps the pod off a node with one label:

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

- **The key.** One key for each kind of hardware, in the operator's
  own API group, with the value `none`. The keys in use are
  `bluetooth.liken.sh/bluetooth`, `equipment.liken.sh/cec`,
  `audio.liken.sh/sound-card`, and `display.liken.sh/display`. A new
  `DaemonSet` takes a new key of the same form.
- **The default.** `NotIn` also matches a node that has no such label,
  as the Kubernetes page on set-based requirements states. So with no
  label the `DaemonSet` makes a pod on every node, and the claim
  decides where it can start. Use one term with one expression: a
  second term is ORed with the first and lets the pod back onto a
  labeled node, and a second expression narrows where the pod runs.
  `nodeSelectorTerms` is an atomic list, so a cluster owner's patch
  that sets a node affinity replaces the term, and the guide says so.
- **The label.** A person declares it in the node's `Machine`, in
  `spec.nodeLabels`, which refuses only keys that start with
  `liken.sh/` and accepts a key in an operator's subdomain. A `Machine`
  accepts such a key only from `liken` 2026.09.28-002 on; an older
  release refuses it, so an install guide must also show the
  `kubectl label` way for a cluster on an older release. The machine
  operator applies the label, and removes it when the key leaves the
  spec. A person can also set it with `kubectl label node`; `liken`
  leaves a label it did not declare in place. When the label changes,
  the `DaemonSet` controller deletes or adds the pod.
- **The test.** A test in the repository reads the `DaemonSet` from
  `deploy/` and checks for the one term with its key
  (`nodeaffinity_test.go`).
- **The guide.** The install guide has the section "Keep the pods off
  nodes with no ...", in the same words as the other operators'
  guides.

## An operator runs in liken-system and watches every namespace

A person who installs `liken` finds every operator in one place, and
the cluster owner chooses where their objects live. So every operator
follows the same shape:

- The operator's `Deployment` and `ServiceAccount` are in
  `liken-system`. Its RBAC is a `ClusterRole` and a
  `ClusterRoleBinding`, and it watches every namespace.
- The deploy base creates no namespace other than `liken-system`, and
  never sets the namespace of the owner's objects.
- A namespaced object's workloads (pods, `Service`s, `ConfigMap`s,
  `Job`s, `ResourceClaim`s) run in that object's namespace, never in
  `liken-system`. The owner's `ResourceQuota`, `NetworkPolicy`, Pod
  Security level, and RBAC grants in that namespace then cover them,
  and a reference by name in a spec resolves there.
- A kind is cluster-scoped only when it exists once per cluster or
  names one piece of hardware, such as `Receiver`, `Television`,
  `Keymap`, or `Person`. A kind that a person declares as a unit of
  use, such as `Library`, `Player`, or `Telescope`, is namespaced.
- Names in two namespaces can be the same. Key the operator's memory
  by namespace and name, or keep one copy of the state for each
  namespace, as `observatory-operator` does (`namespaces.go`).

Plan 15 of `observatory-operator` moved that operator to this shape
and records the reasons.

## Decision: every watch runs on client-go

Decided on 2026-09-27: every operator in the organization watches the
API server through client-go's reflector, in the lean form below, and
deletes its hand-written loop. The reason is maintenance. Eight
repositories each held one loop, about 2,470 lines in all (1,530 of
code) and 4,900 lines of tests for the loops alone. The loops drifted
apart, and each fix landed in one repository at a time. Upstream
maintains and tests the reflector, so the project stops owning eight
loops. The choice set aside was one shared Go module with a loop of the
project's own. It kept the guards exactly and cost no memory, and the
project would have maintained the loop, its fake API server, and a
release that every operator must take.

**The lean form.** An operator imports only
`k8s.io/client-go/tools/cache`, `k8s.io/client-go/dynamic`, and
`k8s.io/client-go/rest`, and decodes each object into its own struct
with `runtime.DefaultUnstructuredConverter`. It never imports
`dynamicinformer`, `informers`, or `kubernetes` (the typed clientset).
Those link a client and an informer for every built-in kind, which
doubles the binary. The operator's own client for reads and writes
stays; only the watching moves.

**The measured cost.** `bluetooth-operator` plan 09 measured its port
on 2026-09-27. Each binary ran only its three watches against a k3s
v1.36.3 API server in Docker, with client-go v0.36.3, and linked every
package of the operator.

| | Hand-written loop | client-go, lean form |
|---|---|---|
| Stripped binary (the image is `FROM scratch`, so the same) | 15.0 MB | 19.9 MB |
| Linked Go packages | 354 | 474 |
| RSS, idle, 50 objects | 16.0 to 16.9 MB | 21.0 to 22.4 MB |
| RSS, idle, 250 objects | 18.4 MB | 25.5 MB |

The earlier research, with one watch, measured 39.1 MB for a binary
that used `dynamicinformer`. A transform that removes
`metadata.managedFields` before the informer stores an object took the
RSS at 250 objects from 28.2 MB to 25.5 MB. A two-node test cluster
ran seven and four operator pods on its nodes that do not link
client-go yet. At 5 to 7 MB for each pod, the cost is about 20 to 50 MB
on each node, which matters on a 1 GB machine. The port removed 309
lines of code and 682 lines of tests, and added 264 lines of code and
326 lines of tests.

**The differences from the guards.** The reflector does not meet the
three guards above exactly. A reading of client-go v0.36.3's
`tools/cache/reflector.go` gives five differences:

1. After a `410`, it waits a backoff of 0.8 to 1.6 seconds before it
   reads the collection again.
2. A watch that closes in under a second with no event makes it read
   the collection again after the backoff. It does not resume.
3. A watch that delivered an event is never short.
4. It measures a watch's life from the request, not from the `200`.
5. Its backoff resets after two minutes with no failure, not after one
   watch that ran a second.

The organization accepts them because none of them loses an event.
Differences 1 and 2 cost one more read of the collection, or a read a
second later, and 3, 4, and 5 change only how long the reflector waits
during a fault. The reflector also reads with a streaming list
(`WatchListClient`, on by default in v0.36), and falls back to a plain
list when the API server refuses it.

The guards and the scenarios above still describe what any watch must
do, and a reviewer checks an operator's use of the reflector against
them. A handler that drops an object, a selector that does not follow
its source, and a loop of the operator's own around the informer each
break a guard.

**The migration order.** `bluetooth-operator` is done, and it is the
reference port (plan 09). The other operators follow one operator at a
time. `per-node-csi-driver` already runs a client-go informer, and
`git-csi-driver` already links client-go's typed clientset under its
hand-written loop. Each port measures its binary, its image, and the
idle RSS of its watches before and after, and records them in a plan.
Until an operator moves, its hand-written loop must still pass every
scenario above.

**The shared module.** Step 4 of plan 69 moves each operator's copy
of the client, the watch, and the cache into the Go module
`kubernetes/` at the top of the repository, one operator at a time.
`bluetooth-operator`, `audio-operator`, `display-operator`,
`equipment-operator`, `media-operator`, `library-operator`, and
`liken`'s machine operator, cluster operator, and CLI are on it.
`liken/kubernetes/watch` holds what `liken`'s two operators add: the
wake handlers and the reads of a store.

## Watch a collection with client-go

`kubernetes/informer` holds the skeleton below: `Start` runs one watch
of a `Source`, with the `Handler`, `Synced`, and `Reopened` of its
`Options`, and `Convert` and `Report` decode and log each object.
`bluetooth-operator` is the reference for the handlers:
`requestwatch.go` and `editwatch.go` hold them, and `watch_test.go`
runs them through the real reflector. `liken/kubernetes/watch` holds
`liken`'s handlers: `WakeOnChange`, `WakeOnEdit`, and `WakeOnContent`
for a collection whose `Transform` trims each object to the fields the
pass reads, and `WakeOnAnotherWritersChange` for an object whose
status the pass writes and another writer changes too.

- **Imports.** Allowed: `k8s.io/client-go/tools/cache`,
  `k8s.io/client-go/dynamic`, `k8s.io/client-go/rest`, and
  `k8s.io/client-go/util/workqueue` only when a queue for each object
  makes the code simpler. Forbidden: `dynamicinformer`, `informers`,
  and `kubernetes`. The client-go version follows the Kubernetes minor
  in `liken`'s `k3s/VERSION`: v1.36.x takes v0.36.x.
- **One informer for each collection.** Build a `cache.ListWatch`
  whose `ListWithContextFunc` and `WatchFuncWithContext` call the
  dynamic client, set the label selector in both, and pass it to
  `cache.NewInformerWithOptions` with `&unstructured.Unstructured{}` as
  the object type. Run it with `RunWithContext`, which returns after
  the last handler call. Pass a `Transform` that removes
  `metadata.managedFields`.
- **Convert, and report what does not convert.** A handler receives an
  `*unstructured.Unstructured`, or a `cache.DeletedFinalStateUnknown`
  tombstone for an object deleted while the watch was down. Unwrap the
  tombstone, decode with `runtime.DefaultUnstructuredConverter`, and
  log an object that does not convert, with its kind and name. Never
  drop one silently. A tombstone can hold no copy at all. Then forget
  the object by the tombstone's key, or wake the pass.
- **A handler wakes the pass.** An operator that runs one full pass for
  each wake needs no queue. The handler sends on a channel with one
  slot, with a `select` that has a `default`, so a burst of events
  makes one wake. When the pass can read a collection before the
  informer's first read does, the watch also wakes the pass once when
  the informer has synced, so an edit between the two reads is not
  lost. Wait on `HasSyncedChecker().Done()`, not `WaitForCacheSync`,
  which polls. Join that goroutine before the channel closes.
- **Filter the operator's own status writes.** `UpdateFunc` receives
  the copy the informer held and the new copy. Wake only when
  `metadata.generation`, `metadata.deletionTimestamp`, or
  `metadata.uid` differ between them. A write to the status
  subresource changes none of them. After a gap in the watch, the
  informer reports each difference from what it held as an add, an
  update, or a delete, so the same rule covers the gap. The UID is in
  the compare because an object deleted and created again with the
  same name during the gap arrives as an update.
- **Restart a watch whose selector changes.** A selector is fixed for
  the life of an informer. When its source changes, such as the
  address of the radio the pass reads, cancel the informer's context,
  wait until `RunWithContext` has returned, and start a new informer
  with the new selector (`followPeripherals` in `editwatch.go`).
- **Follow one object by name with `informer.WatchOne`.** It selects
  the object with the field selector `metadata.name`, which is how RBAC
  matches a list and a watch against a Role's `resourceNames`. It hands
  the owner each version, and nil when the object does not exist,
  including at the end of a first read that finds nothing. The API
  pods of `audio-operator`, `display-operator`, and `media-operator`
  follow their certificate Secrets and CA ConfigMaps this way.
- **Test the handlers, not the reflector.** Point the dynamic client
  at a server from `kubernetes/apiservertest` that answers a streaming
  list: an `ADDED` event for each object, then a `BOOKMARK` whose
  annotations hold `k8s.io/initial-events-end: "true"`, then the
  script's events on the same stream. Test the wake rule through that
  server, and leave the reflector's faults to upstream. The server
  answers over in-memory connections, so the test runs in a
  `synctest` bubble and waits out a backoff on the fake clock.

## Read from the cache

A pass reads the objects a watch holds from the informer's store, not
from the API server. A settled pass then sends the API server no read
of a watched kind. `kubernetes/informer` holds the reads.
`kubernetes/memo` holds the memo and the requests whose answers it
notes (`ReadFresh`, `ReadList`, `Written`, and `SettleStatus`), and imports nothing
from `k8s.io`, so a program that must not link client-go can link
it.

- **The rule.** Read one object with `informer.ReadOne` and a list with
  `informer.List`. A store answers only when it is ready
  (`informer.View.Ready`): it holds the whole first read, and the API
  server accepted a watch and forbade none since. Before that, and
  after a `401` or a `403` on a watch until the API server accepts one,
  read and list from the API server. `informer.List` sends that list
  itself and notes the version of each object it answers, because the
  store can become ready at a copy older than the list: the watch's
  first read can come from the API server's watch cache. A list that
  an operator sends itself notes nothing, and the next pass acts on the
  older copy. The list states no `resourceVersion`, so etcd answers it.
  `library-operator` is the one
  exception: it reads its member pods, Players, MediaPreferences,
  Plays, people, claims, volumes, stood pods, progress pods, and nodes
  only from their stores, and has no read of them from the API server.
  While their watch is forbidden, the reflector lists each again after
  each backoff, which grows to between thirty and sixty seconds, and
  the pass reads that list (`library-operator/watch.go`, `items`). A watch that fails for
  another reason, such as a refused connection while the API server
  restarts, leaves the store ready, so the operator keeps its local
  work going from the store. `liken`'s operators set
  `informer.Options.UnreadyOnWatchError` on every watch, so any failed
  watch stops the store until the API server accepts a watch again: a
  machine must not stage a rollout or reboot from a store that missed
  the writes made while its reflector waited out a backoff.
  `ReadOne` reads an object the store does not hold from the API
  server. A copy that does not convert is read from the API server
  too, so a list leaves out no object. `liken`'s watches each select
  exactly the objects a pass reads, often one object by name, so its
  reads (`liken/kubernetes/watch`) answer from a ready store that an
  object it does not hold, and that the memo has not noted, does not
  exist, the same way `library-operator`'s `readStood` does.
- **The memo.** A store's copy can be older than the operator's own
  last write, because the watch delivers the write a moment later, and
  later still while the watch is down. A pass that acts on that copy
  acts again on a change it already made: it opens a pairing window
  again, sends a receiver a setting again, or skips a status write
  that the older copy hides. So `memo.Versions` records the
  `resourceVersion` of each object's newest copy that the operator
  wrote or read. A store's copy at another version is read from the
  API server once, and then the store answers again. Compare versions
  only for equality. Send every request whose answer the memo notes
  through `memo.Versions.Send`, which holds one request for an object
  at a time, so two goroutines that write one object note their
  answers in the order the API server gave them. A failed request notes
  that the operator holds no current copy, because a write whose answer
  was lost may have landed. A store that holds the whole collection
  (`informer.View.Whole`) also lists each object the operator created
  that the store does not hold yet, so a pass does not create it twice.
  Do not set `Whole` on a store with a selector: an object the operator
  wrote can be outside the selection.
- **A 409.** A write from a copy that another writer changed gets
  `409 Conflict`. `informer.SettleStatus` then reads the object from
  the API server, composes the status again from the fresh copy, and
  writes once more if the fresh copy still needs it. A `404` means the
  object is gone, and the caller handles it as absent. A server-side
  apply states no `resourceVersion` and gets no `409`, so an operator
  that writes only by apply needs no retry, and still notes each
  answer.
- **When a direct GET stays.** Read from the API server, and say why
  at the call, when no watch holds the kind (a `ResourceClaim` a pass
  reads by name, a `ResourceSlice`), when the read must see another
  writer's write at once (the owner `Adapter` of a new `Peripheral`,
  whose UID the store can hold from an object deleted since), and for
  a read once at start, before the watch opens.
- **The reference files.** `kubernetes/informer/cache.go` and
  `cache_test.go` (the reads, a list that meets a create, and a list
  before the store is ready),
  `kubernetes/memo/requests_test.go` (the status write after a `409`),
  `kubernetes/memo/memo_test.go` (the memo itself), `bluetooth-operator/objectcache_test.go` (the memo
  across three kinds and a store that follows its selector),
  `display-operator/objectcache.go` (two goroutines that write one
  kind), `audio-operator/endpointwatch.go` (stores scoped by field
  selector), and `equipment-operator/objectcache.go` with
  `cecbus_client.go` (writes by server-side apply through
  `memo.Written`, whole stores, and the creates they must not
  repeat). The test `TestAPassDoesNotActOnACopyOlderThanItsOwnWrite`
  in each operator fails without the memo.

## The manual changes with the code

Each component's manual is in `docs/content`, and the skills in its
`skills/` are generated from the guides there. A reader installs and
runs the component from the manual alone, so a manual that lags the
code gives a wrong command, not a missing detail. An audit in
2026-10 found the same kinds of drift in most components. Each one
came from a change that updated the code and left the manual for
later. So update the manual in the commit that changes the code.

The generated pages, the CRD and API references and the skills, are
current when you run the component's generators. The hand-written
pages drift. Check these for each kind of change:

| Change | Pages to update |
|---|---|
| A file added to or removed from `deploy/kustomization.yaml` | Every file list in the install guide: the raw-URL install, the GitOps example, and the removal steps |
| A container, `Deployment`, or `DaemonSet` added or removed | The install guide's description of what runs, and the development-build pin, which names every image |
| A metric added, renamed, or removed | `reference/metrics.md` |
| A flag on the operator or the `kubectl liken` plugin | The guide that runs the command |
| A printer column, condition, or `Event` reason | Every sample of `kubectl get` or `kubectl describe` output that shows it |
| A behavior the operator adds on its own, such as a recovery or a retry that a person can see | The guide where a reader looks when the behavior surprises them |
| A field or topic removed from the bus or the API | Every page of another component that names it |

After you edit a guide, run the component's `skills` target and its
docs tests. CI fails when the committed skills differ from what the
generator writes.
