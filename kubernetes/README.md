# kubernetes

This is the Go module that each `liken` operator imports to work with
Kubernetes. With it, an operator reads, writes, and watches objects,
posts `Event`s, sets conditions, and elects the one replica that acts.
Operators build their reconcile passes on these packages, without
controller-runtime. The packages are small, so a program that must
stay small links only the packages that it uses.

The module contains the code that each operator needs in the same
form, and the fakes that tests use. It defines no resource of its own
and no reconcile loop. Each operator has its own types and its own
logic.

Each operator and CSI driver, and the OS's `machine-operator` and
`cluster-operator`, import the module through a `replace` directive in
their `go.mod`. So each one builds against the code in this tree. The module is not
published. The watches use client-go's reflector, which the upstream
Kubernetes project maintains.

## The packages

- **`apiclient`** sends each read and write to the API server as one
  HTTPS request. It uses only `net/http` and `encoding/json`, and no
  package from `k8s.io`.
- **`memo`** records the `resourceVersion` of each object that an
  operator wrote or read. While the watch's copy of the object has a
  different version from the recorded one, a pass reads the object
  from the API server.
- **`informer`** uses client-go's reflector to keep an operator's store
  of a collection up to date, and a pass reads from that store.
  `WatchOne` watches one object by name.
- **`election`** uses a `coordination.k8s.io` `Lease` so that only one
  replica of a singleton operator acts at a time.
- **`events`** posts core/v1 `Event`s about an operator's objects.
  `Recorder.SetCondition` sets a condition and posts an `Event` for
  each transition.
- **`conditions`** defines the `Condition` type that every component's
  resources report, and `Set`, which updates a list of conditions.
- **`endedpods`** has the rule for a pod that the kubelet ended. The
  rule sets how long the operator waits before it creates the next
  pod. The count of earlier ended pods is stored in an annotation.
- **`apiservertest`** runs a fake API server for a test, over in-memory
  connections, so the test can run on the fake clock of
  `testing/synctest`.
- **`events/eventstest`** fakes the core/v1 events collection, and
  **`election/electiontest`** fakes the `Lease` of an election.

Each package's doc comment describes it in full. Root plan 78 has the
rules for conditions, `Event`s, and log lines. The `operators` skill
has the rules that each watch follows.

MIT. See [LICENSE](LICENSE).
