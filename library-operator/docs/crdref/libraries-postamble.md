## On the bus

No pod the operator creates holds an API credential. The namespace's
catalog pod publishes one report per `Library` on `media-operator`'s
bus, under this operator's own topic base, `liken/library` by default,
and the operator folds each report into that `Library`'s status.
[The library bus](/docs/reference/bus/) is the contract of the whole
tree, and it gives every field of the report.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `libraries/{namespace}/{name}/status` | the catalog pod | yes | the library's report |
| `catalogs/{namespace}/availability` | the catalog pod | yes | `online` or `offline` |

### `status`

The report, as the operator writes it into the status: the counts, the
rows the last sweep removed, the two times, the gaps and the oldest
attempts per fact, and the last run of each worker. A scan `Job`
writes its `runs` row as its last catalog write and exits only when
this report names that `Job`, so a report is also the proof that the
catalog pod holds every row the `Job` wrote.

    {
      "titles": 412,
      "unidentified": 9,
      "lastWalk": "2026-08-29T21:04:11Z",
      "lastChange": "2026-08-29T21:04:11Z",
      "items": 412,
      "files": 2189,
      "walking": false,
      "removedLastSweep": 3,
      "runs": [
        {"worker": "scan", "job": "movies-scan-29310740",
         "started": "2026-08-29T21:03:02Z", "finished": "2026-08-29T21:04:11Z",
         "unidentified": 9, "removed": 3}
      ],
      "gaps": {"art": 4, "identity": 2},
      "oldestAttempts": {"identity": "2026-08-01T03:15:00Z"},
      "waiting": 1,
      "unresolved": 1,
      "fights": 0
    }

`walking` is true while a scan `Job` runs, and the operator's phase
follows it. `oldestAttempts` is what the operator reads `spec.refresh`
against: a refresh later than the oldest attempt is a fact with work
left, whatever `gaps` says.

### `availability`

`online` while the catalog pod's reporter runs. The reporter names this
topic as its MQTT Last Will with `offline` as the payload, so a pod the
kubelet killed reads `offline`, and every `Library` of the namespace
reads `Offline` with it. The reports it left behind stand: the counts
describe the catalog, and they hold until the next run replaces them.
One reporter serves every `Library` of the namespace, so there is one
availability topic per namespace and none per `Library`.
