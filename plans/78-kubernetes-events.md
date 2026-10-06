# 78, Kubernetes `Event`s across `liken`

Proposed on 2026-10-06. Every component posts a Kubernetes `Event` for
each condition transition and for each action it takes on its own,
through one writer in the shared module `kubernetes/`. The work touches
`kubernetes/` and every operator and CSI driver in the repository.

## The problem

A research pass of 2026-10-06 read every component. Seven of thirteen
components write `Event`s, but most of what they write is an audit
trail of HTTP captures (`Captured` on a `Sink`, a `Display`, or a
`Player`). The state changes that a person debugs are mostly missing:

- No component except `observatory-operator` and the two CSI drivers
  posts an `Event` when its own resource changes state.
- `liken`'s machine operator and cluster operator write none. A
  `Machine` that goes `Lost`, rolls back a release, or refuses a disk
  leaves only a condition and a line in stdout.
- `equipment-operator`, `people-operator`, and `bluetooth-operator`
  write none. `library-operator` reads `Event`s and writes none.

A condition holds only its last transition. So the history of a
rollout, a reboot, a receiver that dropped off the network, or a
`Play` that failed is only in a container's stdout.

The code that exists is seven separate copies of the same core/v1
POST. Each one writes `count: 1` and never aggregates a repeat. A
refused CSI mount posts about 37 separate `Event` objects for each pod
in each hour, because the kubelet retries the mount with a backoff that
caps at about two minutes. `kubectl describe` shows each of them as its
own line, not as one line with `(x37 over 1h)`.

Five components declare their own `Condition` type: `liken/api`,
`observatory-operator/observatory`, `library-operator`,
`equipment-operator`, and `bluetooth-operator`. Each has its own
setter, and none of them posts an `Event`.

The research found four defects on the way:

1. `git-csi-driver` keeps fetch failures, push failures, and "upstream
   moved" in one `trouble` field. After one of them sets the field,
   the first failure of another kind posts no `Event` until a success
   clears it.
2. `audio-operator`'s `postEvent` writes `Normal` for every `Event`,
   so it cannot post a Warning.
3. `observatory-operator`'s test
   `TestRefusedStatusWritesAndEventsAreWrittenLater` refuses three
   `Event`s and asserts nothing about them. The writer never retried,
   so a refused `Event` was lost.
4. `observatory-operator` records no `Event` and no log line when a
   Ready reservation's runner creates an INDI server pod again, and a
   device's phase stays `Inventory` while the runner creates its pod
   again.

## The rule: condition, `Event`, or log line

| Channel | Holds | The question it answers |
|---|---|---|
| Status condition | the current state, which automation and `kubectl wait` read | "What is true now?" |
| `Event` | a transition or an action, with a time, for a person, at most a few per object per hour, deleted after an hour | "What happened to this object recently?" |
| Log line | every attempt, retry, and detail, kept as long as the logs are | "What did the program do, step by step?" |
| Metric | rates and counts, which an alert can read | "How often, and is it getting worse?" |

- **Conditions stay where they are idiomatic.** No condition moves to
  an `Event` unless it only records a past action. A status field that
  must outlast the `Event` TTL stays in status, for example
  `Machine.status.lastCrash` and `lastFailStop`.
- **Every condition transition posts one `Event`** with the
  condition's reason and message, through the shared setter. A
  transition is a new condition, a change of status, or a change of
  reason. A change of message alone posts nothing.
- **A one-off action that changes no condition posts one `Event`**:
  a pod created again, a reboot requested, a key minted, a device that
  appears or vanishes.
- **Nothing else posts an `Event`.** Readings, key presses, guide
  steps, volume asks, CEC messages, uevents, webhook calls, and each
  retry of a retry loop go to logs and metrics. A retry loop posts its
  first failure and its recovery.
- **Warning means a person may need to act.** Normal means an expected
  transition or an action the component took.
- **A reason** is UpperCamelCase, specific, and stable:
  `ReleaseRolledBack`, not `Failed`. An `Event` that marks a condition
  transition uses the condition's reason. A reason on an object that
  another component owns, such as a `Pod`, takes the component's
  prefix: `GitVolumeRefused`. The writer cuts a reason to 128 bytes.
- **A message** is plain text in the voice of `brand/voice.md`. It
  holds the values: before and after, the count, the error, the
  deadline. It never holds a token. The writer cuts a message to 1024
  bytes at a character boundary.

The API server deletes an `Event` one hour after its last write. k3s
does not change kube-apiserver's `--event-ttl`, and `liken` sets
nothing. An overnight fault is gone by morning, so the condition and
the logs must still carry the fact. A longer TTL is a separate
decision.

## The design

### The writer: `kubernetes/events`

The package writes core/v1 `Event`s through `apiclient`. It imports
nothing from `k8s.io`, so every build can link it, the pod build of
`library-operator` included.

```go
func New(ctx context.Context, client *apiclient.Client, component string, options Options) *Recorder

type Options struct {
	Instance string    // reportingInstance; empty means os.Hostname()
	Log      io.Writer // one line for each Event lost; nil means standard error
}

type ObjectReference struct {
	APIVersion, Kind, Namespace, Name, UID string
}

func (r *Recorder) Normal(object ObjectReference, reason, message string)
func (r *Recorder) Warning(object ObjectReference, reason, message string)
func (r *Recorder) Dropped() uint64
func (r *Recorder) Transition(object ObjectReference, c conditions.Condition, bad conditions.Status)
func (r *Recorder) SetCondition(object ObjectReference, list *[]conditions.Condition, next conditions.Condition, bad conditions.Status) bool
```

- **core/v1, no `eventTime`.** `kubectl describe` and `kubectl events`
  read core/v1. The strict checks of `events.k8s.io/v1`, which refuse
  a note longer than 1024 bytes, apply only when `eventTime` is set.
  Flux makes the same choice. Both API groups share one storage, so a
  reader of either group sees the `Event`.
- **The fields.** `involvedObject` holds the reference.
  `source.component` and `reportingComponent` hold the component name,
  such as `machine-operator`. `reportingInstance` and `source.host`
  hold the pod or node name. `firstTimestamp`, `lastTimestamp`, and
  `count` describe the series.
- **A cluster-scoped object's `Event`s go in `default`.** The API
  server requires `default` or `kube-system` for them. `kubectl
  describe` finds them there. `kubectl events --for` finds them only
  with `-n default` or `-A`, and each guide says so.
- **Aggregation.** The writer keys each `Event` on the object's UID
  (or its kind, namespace, and name when it has no UID), the type, the
  reason, and the message. A repeat within 10 minutes of the last one
  patches `count` and `lastTimestamp` on the existing `Event` with a
  merge patch. A repeat after that, or a patch that meets a `404`
  because the TTL deleted the `Event`, creates a new one. An LRU of
  4096 keys bounds the memory.
- **Best effort.** `Normal` and `Warning` put the `Event` on a queue
  of 256 and return at once. One goroutine, which ends with `ctx`,
  writes the queue. When the queue is full, the `Event` is dropped and
  counted (`Dropped`), and the first drop is logged. A failed write is
  sent again twice, 10 seconds apart, so an API server that restarts
  loses no `Event`. After the third failure, the writer logs one line
  and goes on. A write never blocks or fails a reconcile pass.
- **Nil is valid.** A nil `*Recorder` posts nothing, so a test or a
  program that has no API server can pass nil.
- **RBAC.** A component needs `create` and `patch` on `events` in each
  namespace it posts to, and in `default` for cluster-scoped objects.

The choice set aside was client-go's `tools/record` with a custom
sink. Its correlator has years of use upstream, and the `operators`
skill prefers upstream code for watches. But a minimal binary with the
`informer` dependencies grew from 14.8 MB to 23.2 MB when it added a
recorder, as the research measured, and `tools/record` could not join
the builds that must not link client-go. `tools/events` links the
typed clientset and fails the link guards of `kubernetes/informer`,
the `media-operator` pod build, and the `equipment-operator` node
build. The correlator is small and has no network protocol to get
wrong, so this trade differs from the watch decision, where the
hand-written loops failed in review.

### The condition type and the setter: `kubernetes/conditions`

```go
type Status string // True, False, Unknown

type Condition struct {
	Type               string    `json:"type"`
	Status             Status    `json:"status"`
	ObservedGeneration int64     `json:"observedGeneration,omitempty"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

func Set(list *[]Condition, next Condition) (transitioned bool)
func Find(list []Condition, conditionType string) (Condition, bool)
```

The type has the JSON shape of `metav1.Condition`. `Set` keeps the
Kubernetes rule that makes `lastTransitionTime` meaningful: the time
moves only when the status changes. It answers true for a new
condition, a new status, or a new reason.

The type is in its own package, apart from the writer, because API
type packages such as `liken/api` declare it in their resources. Such
a package then links only `time`, not an HTTP client. The writer
imports `conditions`, and `Recorder.SetCondition` joins the two: it
calls `Set`, and on a transition posts one `Event` with the
condition's reason and message. The caller passes `bad`, the status
that needs a person. A transition to `bad` is a Warning, and any other
is Normal. A caller whose condition is bad only for some reasons, such
as a `Ready` that is `False` both while a device starts and when it
fails, passes `bad` only for the failing reasons.

A caller that composes its whole status and writes it later calls
`Set` while it composes, keeps each transition, and calls `Transition`
for each one after the API server accepts the write. A status write
that the API server refuses then posts nothing, and the next pass
finds the same transition again.

### Each component adopts it

The recipe for each component:

1. Replace the component's `Condition` with an alias:
   `type Condition = conditions.Condition`, and its status type with
   `type ConditionStatus = conditions.Status`, with the constants
   bound to `conditions.True`, `conditions.False`, and
   `conditions.Unknown`. This changes no CRD when the JSON shape is
   the same. Where it differs, report the difference and keep the
   component's type. The research found these differences:
   `liken/api`, `library-operator`, and `equipment-operator` mark
   `reason` and `message` `omitempty`; `equipment-operator` holds
   `lastTransitionTime` as a string; `bluetooth-operator` holds
   `status` and `lastTransitionTime` as strings and has no
   `observedGeneration`. An `omitempty` reason differs only when the
   reason is empty, which `metav1.Condition` forbids.
2. Replace the component's own setter with `Recorder.SetCondition`,
   or with `conditions.Set` and `Recorder.Transition` after the write.
3. Build one `Recorder` in `main` with the program's context, and
   replace each hand-built `Event` POST with `Normal` or `Warning`.
4. Post the one-off actions that the research lists for the component.
   List the component's reasons in one file of constants, and name
   them in its troubleshooting guide.
5. Grant `create` and `patch` on `events`.
6. Test: the component's fake API server mounts
   `eventstest.Events` in front of its own handler, and each test
   asserts the `Event`s its key transitions post, in a `synctest`
   bubble.

### Tests: `eventstest.Events`

`kubernetes/events/eventstest` is a fake of the core/v1 `events`
collection. A test mounts it in front of its own fake API server with
`Around`, serves both through `apiservertest`, and runs in a
`synctest` bubble. The fake answers each create and merge patch of an
`Event` the way the API server does, and refuses an `Event` about a
cluster-scoped object outside `default` and `kube-system`. `List` and
`About` answer what it holds, `Refuse` makes it refuse the next writes
with a `503`, and `Expire` deletes every `Event`, the way the TTL does.
The writer's tests and each component's tests use it, so no component
keeps its own copy of an `Event` store.

The fake is in its own package next to the writer, and not in
`apiservertest`, because `apiclient`'s tests import `apiservertest`
and the writer imports `apiclient`. A fake in `apiservertest` that
holds the writer's `Event` type makes an import cycle.

## The rollout

- **Wave 1:** this plan; `kubernetes/events`, `kubernetes/conditions`,
  and `kubernetes/events/eventstest`; the section on `Event`s in the root
  `AGENTS.md` and the `operators` skill; and `observatory-operator`,
  which posts the most `Event`s today. It also fixes defects 3 and 4.
- **Wave 2, in parallel, one agent for each group:**
  - `liken/` (machine-operator and cluster-operator), the largest gap.
  - `git-csi-driver` and `per-node-csi-driver`, with defect 1.
    Aggregation ends the 37 refusals an hour. Both move from
    client-go's fake clientset to `apiservertest` in their `Event`
    tests.
  - `audio-operator`, `display-operator`, and `media-operator`, with
    defect 2.
  - `equipment-operator`, `people-operator`, `library-operator`, and
    `bluetooth-operator`.

The research of 2026-10-06 listed the moments to post for each
component, and each Wave 2 change starts from that list. Each wave
records here what it built and what it measured. The plan closes into
`completed/` when every component is done.

## Wave 1, built on 2026-10-06

- **`kubernetes/`** gained `events`, `conditions`, and
  `events/eventstest`, at 100% statement coverage each. The module's
  coverage floor rose from 98% to 98.5% (measured 98.6%). The link
  guard in its `Makefile` now holds `events` and `conditions` to no
  import from `k8s.io`, as it holds `apiclient` and `memo`.
- **The writer's tests** run in `synctest` bubbles: an Event's fields,
  a cluster-scoped object in `default`, three repeats as one `Event`
  with `count: 3` and a fourth after 11 minutes as a new one, a repeat
  after the TTL, a refused create and a refused patch sent again, a
  write that fails three times logged once, a full queue that drops
  four of 261, a cut reason and message, the 4096-series limit, and a
  context that ends during the retry wait. With the window set to zero,
  three of them fail.
- **`observatory-operator`** writes through the recorder, aliases its
  `Condition` and `ConditionStatus` to the shared types, and posts each
  condition transition of a device, a `Telescope`, an `Observatory`, a
  `Guider`, an `OpticalTrain`, and an `OpticalTube` after the status
  write lands. `ParentFound=False` and a `Ready` that fails are
  Warnings. Its coverage measured 95.7% against a floor of 95%.
- **Its defects.** A recreated INDI server pod posts `PodCreated` on its
  `Telescope` or `Observatory` and logs a line. A device whose pod a
  Ready reservation's runner creates again is `Starting`, which the CRD
  describes as "the device's pod starts", in place of `Inventory`,
  which the CRD describes as "no reservation needs the device". A
  device whose pod is not created yet during activation is still
  `Inventory`; changing that needs a decision on what `Inventory`
  means before a reservation is Ready. The test of refused `Event`s
  now refuses two and shows that every `Event` arrives.

The build changed four points of the design as it was first settled:

- `events.New` takes the context that ends its goroutine, and an
  `Options` value, in place of `New(client, component)`.
- The fake of the `events` collection is in `events/eventstest`, not
  in `apiservertest` (the section on tests gives the import cycle).
- A new condition posts an `Event` too, so a resource's first status
  write posts one `Event` for each condition it reports.
- A `Reservation`'s conditions post nothing through the setter. Its
  runner already posts one `Event` for each step's end and each phase,
  with the step's summary, and each change of its `Ready` and
  `SafeToPowerOff` conditions comes with one of those. A component
  whose own `Event`s already cover each transition with more detail
  may do the same, and says so where it writes the status.

## Wave 2, built on 2026-10-06

- **`liken/` (machine-operator and cluster-operator).** `liken/api`
  aliases `Condition` and `ConditionStatus` to the shared types. Both
  CRDs require a reason of at least one character and accept an empty
  message, so the JSON on the wire is the same. `init` reads these
  types, and its link guard now admits `kubernetes/conditions`, which
  imports only `time`. Each operator posts each condition transition
  after its status write lands (`api.Transitions` compares the written
  list with the stored one). A Machine transition is a Warning when
  the phase it argues for is `Blocked`, `Degraded`, or `Unknown`, and
  its message starts with the condition's type and status, because
  five convergence conditions share reasons such as `Converged`.
  Ready's reason is the phase word, so Ready's transitions are the
  phase changes and no `PhaseChanged` exists. The survey's
  `SpecRejected` and `ReleaseRolledBack` are the transitions to
  `StagingRejected` and `RejectedLastBoot`, by the rule that a
  transition keeps the condition's reason. The other Events are
  `MachineJoined`, `KernelCrashed`, `BootRefused`, `Cordoned`, and
  `Uncordoned` from the machine operator, and `MachineLost`,
  `RebootTurnGranted`, `RebootTurnReclaimed`, `FluxDeployKeyMinted`,
  and `FluxEngineSeeded` from the cluster operator. The three
  verdicts on a Machine name the action, not the condition's reason
  (`HeartbeatStale`, `DisruptionBudgetAllows`), because the cluster
  operator writes them on an object that another program owns. Both
  operators hold `create` and `patch` on `events` in `default` only.
  Coverage measured 88.9% for machine-operator and 86.3% for
  cluster-operator, against floors of 86%.
- **`git-csi-driver` and `per-node-csi-driver`.** Both keep the typed
  clientset, because their watches need it: the arming, the demand
  watch, and the webhook in `git-csi-driver`, and the sweep in
  `per-node-csi-driver`. So `kubernetes/events` does not remove
  client-go from them, and the choice was between it and
  `tools/record` on the clientset they already link. Measured with
  `-s -w`, `kubernetes/events` adds 45 KB to `git-csi-driver` (48.89 MB
  to 48.93 MB) and 36 KB to `per-node-csi-driver`, and `tools/record`
  adds 172 KB to `git-csi-driver`. Neither driver has a link guard. The
  drivers build the clientset and the recorder on one HTTP client from
  `rest.HTTPClientFor`, so both read the token the same way. Both
  drivers post on a `Pod` or a `PersistentVolumeClaim`, which have no
  conditions of this project, so neither declares a `Condition`. A
  test that the kubelet refuses a mount 37 times in an hour finds one
  `Event` with `count: 37`, in each driver. `git-csi-driver` keeps a
  field for each fault, so a push that fails after a stage found that
  upstream moved posts `GitVolumePushFailed` (defect 1); a test with
  the old shared field fails. It adds `GitFetchRecovered`, and
  `GitVolumeUpstreamMoved`, `GitVolumeRefDeleted`, and
  `GitVolumeAbandonedWork`, which a stage finds before the driver has
  found the claim, so the arming posts them on the claim when it finds
  it. Its node plugin gained `patch` on `events`. Coverage is 100% in
  both, against floors of 100%.
- **`audio-operator`, `display-operator`, and `media-operator`.** Each
  container that posts builds one recorder, and each component
  aliases its condition type to `conditions.Condition`
  (`EndpointCondition`, `DisplayCondition`, and both of
  `media-operator`'s). Each CRD accepts the shared shape unchanged: a
  date-time string, an empty message, and no `observedGeneration`.
  Each posts every condition transition with the condition's own
  reason after its status write lands, so the survey's `Connected`,
  `Disconnected`, `PanelDetached`, `ScreenAway`, and `ScreenPresent`
  are the condition reasons, such as `SpeakerConnected` and `NoPanel`.
  `display-operator` keeps `NoPanel` because `media-operator` reads it.
  `Captured` moved to the recorder in all three, so a repeat by the
  same caller patches one `Event`'s count. `media-operator` no longer
  names the `Event` after the request id, and the log line keeps the
  id. Each component's reasons are listed in one file.
  - `audio-operator` fixes defect 2: `postEvent` is gone. It adds
    `LayoutWriteFailed`, `SpecRefused`, `PipeWireLost` and
    `PipeWireRecovered`, and `BluetoothUnavailable` and
    `BluetoothAvailable`. A fault posts its first failure and its
    recovery. `PipeWireLost` posts on the first failed graph read,
    not at the exit that follows the third, because the recorder has
    no flush and an `Event` queued before `os.Exit` is lost. A `Ready`
    that goes `False` is a Warning only while `Connected` is `True`,
    so a TV turned off is Normal. Coverage measured 80.1% on CI's
    tests, and the floor rose from 78% to 79%.
  - `display-operator` adds `WriteUnconfirmed`, `ModeChanged`,
    `CompositorKilled` on every `Display` of the node, and
    `PanelStandbyFailed`. The reason, not the status, decides Warning
    or Normal: `Down`, `Hung`, `Ambiguous`, and `LayoutNotFound` are
    Warnings, and `Retained` is Normal. The operator's grant is a
    `Role` in `default`, where the `Event`s about a cluster-scoped
    `Display` must go. Coverage measured 82.1% against a floor of 82%.
  - `media-operator` adds `PlaybackStarted`, `PlaybackFinished`,
    `PlaybackFailed`, `InvalidSpec`, `PodRecreated`, `ResumeBackoff`,
    `Restarting`, and `Running` on a `Play`; `Superseded` and
    `Retired` on the `Play` and its `Player`; `ReceiverWriteFailed`
    and `ReceiverWriteRecovered` on a `Player`; and `KeymapRefused` on
    a `Remote`. A `Play` whose `Player` does not exist stays `Pending`
    and posts nothing. The `Screen` condition's bad status is
    `Unknown`, so `NoDisplay` is a Warning and `PanelAway` is Normal.
    The operator's `ClusterRole` gained `create` and `patch` on
    `events`. Coverage measured 87.4% against a floor of 87%. A
    separate fix ends a race in the remote reader: it chose at random
    between a cancelled batch and the events still in its channel, and
    lost a press that a node read before it closed, about once in
    3,000 runs. The reader now reads the channel until it closes, and
    its six tests run in `synctest` bubbles.
