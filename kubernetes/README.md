# kubernetes

The Go module that the `liken` operators use to read, write, and watch
Kubernetes objects. Each operator imports it through a `replace`
directive that points at this directory, so the operators always build
against the code in the tree. The module is not published.

- `apiclient` sends each read and write as one HTTPS request. It reads the pod's ServiceAccount token from disk on
  every request, because the kubelet replaces the token before it
  expires. It returns `ErrNotFound` for a `404` and `ErrConflict` for
  a `409`. When the API
  server responds `429`, the client waits as long as the response asks
  and sends the request again. If the `429` lasts longer than the
  client waits, the call returns `ErrThrottled`, and
  `RetryAfterSeconds` returns the wait that the response asked for. A
  client from `WithContext` cancels each request, and the wait after a
  `429`, when its context ends. A client from `WithWaitContext`
  cancels only the wait, so a request that is already sent runs until
  the API server responds.
- `memo` records the `resourceVersion` of the newest copy of each
  object that an operator wrote or read from the API server. An
  operator reads objects from a watch's store, and the store can hold
  a copy older than the operator's own last write, because the watch
  receives that write later. When the store's copy has
  another version, `memo` makes the operator read the object from the
  API server, so a pass does not act on a change it already made.
  `memo` also sends the requests whose versions it records: a read of
  one object, a list, a write, and a status write. When a status write
  gets a `409`, `SettleStatus` reads the object again and writes once
  more if the new copy still needs the change.
- `informer` watches a collection through client-go's reflector, keeps
  the collection in an in-memory store, and serves an operator's
  reads from that store. When another operator defines the kind and
  the cluster does not serve it yet, the store is empty until the kind
  appears. `WatchOne` follows one object by name, such as a CA
  `ConfigMap`. It calls your function with each new version of the
  object, and with nil when the object does not exist.
- `election` makes sure only one replica of a singleton operator
  acts. The replicas compete for a `coordination.k8s.io` `Lease`
  through client-go's leader election, and only the replica that
  holds the `Lease` creates objects or writes status.
- `events` posts core/v1 `Event`s about an operator's objects through
  `apiclient`. A post goes on a queue and returns at once, and a
  background goroutine writes the queue and retries a write that
  fails. When an `Event` repeats within 10 minutes, it increments the
  count on the existing `Event` instead of creating a new one.
  `Recorder.SetCondition` sets a condition and posts one `Event` when
  the condition changes.
- `conditions` defines the `Condition` type that the resources of every
  component report, in the shape of `metav1.Condition`. Its `Set`
  function changes `lastTransitionTime` only when the status changes.
- `events/eventstest` is a fake of the core/v1 events collection. A
  test serves it as part of its fake API server and reads back the
  `Event`s that the component posted.
- `apiservertest` serves a test's fake API server over in-memory
  connections, so the test can run on the fake clock of
  `testing/synctest`.

`apiclient`, `memo`, `events`, and `conditions` import nothing from
`k8s.io`, so a program that must not link client-go, such as the pod
build of `library-operator`, can use them. `informer` uses client-go's
reflector, dynamic client, and `rest` packages, and not the typed
clientset, the informer factories, or leader election. `make test`
checks both limits and runs every check that CI runs.

Root plan 78 gives the rules for conditions, `Event`s, and log lines.
The `operators` skill in `.agents/skills` at the top of the repository
gives the rules that each watch must follow, and why the operators use
client-go's reflector.
