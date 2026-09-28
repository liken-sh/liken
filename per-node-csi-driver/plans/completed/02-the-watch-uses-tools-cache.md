# The watch uses tools/cache

Plan 02. Built on 2026-09-27. The numbers below come from a k3s API
server in Docker, not from liken-1. The drill that is still owed is at
the end of this plan.

## The problem

On 2026-09-27 the organization chose client-go's reflector for every
operator's watch, in the lean form that the `operators` skill in
`.agents` records. The lean form builds each watch with
`k8s.io/client-go/tools/cache` and never imports
`k8s.io/client-go/informers`, which links an informer and a lister for
every built-in kind.

The node plugin's sweep already ran on client-go, but through
`informers.NewSharedInformerFactory`. A check against the skill found
four differences from the reference ports, `bluetooth-operator` plan
09 and `git-csi-driver` plan 14:

* The binary linked `informers` and every lister.
* No transform removed `metadata.managedFields`, so the store held
  that field for every `PersistentVolume` in the cluster.
* `cache.WaitForCacheSync` polled the store until it held the first
  read.
* `per_node_csi_watch_restarts_total` counted each call of the
  reflector's watch error handler. A watch that the API server closed
  at its timeout called no handler, so the metric did not count what
  its reference row said: each time the watch closed and opened again.

The resync comment that the 2026-09-27 audit found wrong was already
fixed at `d8580c7`: the informer takes no resync, and the comment says
that a resync replays the store and reads nothing from the API server.

## The design

`sweep.go` builds one informer with `cache.NewInformerWithOptions`.
Its `ListWatch` calls the typed clientset, which the driver already
links for Events, and it is wrapped with
`cache.ToListWatcherWithWatchListSemantics`. A fake clientset declares
that it does not answer a streaming list, so a test reads a plain
list. A real clientset declares nothing, and the reflector reads with
a streaming list. The typed clientset hands each handler a
`*corev1.PersistentVolume`, so no object needs a conversion and none
can fail one.

* **The transform.** `dropManagedFields` removes
  `metadata.managedFields` before the informer stores an object.
* **The wait for the first read.** The sweep waits on
  `HasSyncedChecker().Done()` or the end of the run, whichever comes
  first. A run that ends first sweeps nothing and logs that the
  volumes did not sync, as before.
* **The restart count.** The `ListWatch`'s watch function counts each
  watch the API server accepts after the first. The reflector asks the
  API server to close each watch after 5 to 10 minutes, so a healthy
  node counts 6 to 12 restarts an hour. A refused watch is not
  counted, so a rate near zero on a running node is a watch the API
  server refuses. The driver's log line "the watch restarted" is gone.
  The reflector reports a failed list and a watch error it does not
  retry to klog on standard error. A refusal it retries, such as a
  refused connection or a `429`, it logs only at a klog verbosity the
  driver does not set.
* **The informer's life.** The sweep runs the informer on a goroutine
  it joins, so `follow` returns only after the last handler call.

These stay as they were:

* **The scope.** A field selector cannot reach `spec.csi.driver`, and a
  static `PersistentVolume` carries no label that the driver sets. So
  the watch takes every `PersistentVolume`, and `handlesOf` keeps this
  driver's handles.
* **The pass.** The pass reads the store, not the API server. A delete
  wakes it through a channel with one slot, so a burst of deletes
  costs one pass. The pass model is one full pass for each wake, and a
  work queue for each object would add nothing: the pass compares the
  store's handles with the copies on the disk in one read of each.
* **The tick.** The 10-minute tick stays, and its comment names the
  two failures that no event follows: a `removeCopy` that failed, and
  a copy that a pod still held when its `PersistentVolume` was
  deleted.

Requests to the API server, read from the code and the fake clientset:

| Event | Before | After |
|---|---|---|
| A pass, woken by a delete or by the tick | 0 | 0 |
| The start, against a real API server | 1 streaming list, which is a watch that stays open after the first read | the same |
| The start, against the fake clientset | 1 list, then 1 watch | the same |
| A watch that closes | 1 watch | 1 watch |

## Measurements

Each binary ran as the node plugin, on the laptop, against a k3s
v1.36.3+k3s1 API server in Docker, with a ServiceAccount token
mounted where `rest.InClusterConfig` reads it. The binaries were built
as the Dockerfile builds them (`CGO_ENABLED=0`, `-trimpath`,
`-s -w`). The cluster held 250 `PersistentVolume`s of this driver.
The RSS is `VmRSS` from `/proc/<pid>/status` 45 seconds after the
start, in two runs of each build.

| | `informers` | `tools/cache` |
|---|---|---|
| Stripped binary | 51,048,608 bytes | 48,484,512 bytes |
| Linked Go packages (`go list -deps .`) | 801 | 682 |
| RSS, idle, 250 `PersistentVolume`s | 37.6 to 38.1 MB | 35.6 to 36.0 MB |

The port removes 2.56 MB of binary, 119 packages, and about 2 MB of
RSS. Against the same server, the build of this change swept a copy
that no `PersistentVolume` named within 5 seconds after a
`PersistentVolume` was deleted, and kept the copy of one that exists.

## Tests

The tests of the shared informer's handler registration are gone,
because this informer registers its handler when it is built and has
no watch error handler. The tests that remain run the sweep through
the real reflector against the fake clientset.

| What | Test |
|---|---|
| Through the reflector: a watch that closes counts on `per_node_csi_watch_restarts_total` | `TestARestartedWatchCountsOnPerNodeCSIWatchRestartsTotal` |
| The informer stores no `managedFields` | `TestTheInformerStoresNoManagedFields` |
| Through the reflector: a deleted `PersistentVolume` loses its copy before the next tick | `TestADeletedVolumeLosesItsCopyBeforeTheNextTick` |
| A run that ends before the first read sweeps nothing | `TestADriverWhoseVolumesNeverSyncSweepsNothing` |
| The sweep ends with the driver's run | `TestTheSweepStopsWithTheDriversRun` |

## The drill still owed

On liken-1, with the driver on a build of this change:

1. Read the node plugin's working set on a 1 GB machine, and compare
   it with the build before this change.
2. Delete a per-node `PersistentVolume` whose pods are gone, and time
   how long its copy takes to leave each node.
