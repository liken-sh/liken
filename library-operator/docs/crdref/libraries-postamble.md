## On the bus

The scanner pod holds no API credential. It publishes its report on
`media-operator`'s bus, under this operator's own topic base,
`liken/library` by default, and the operator folds the report into
the `Library`'s status.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `libraries/{namespace}/{name}/status` | the scanner | yes | the scanner's report |
| `libraries/{namespace}/{name}/availability` | the scanner | yes | `online` or `offline` |

### `status`

The report, as the operator writes it into the status: the two counts
and the two times.

    {
      "titles": 412,
      "unidentified": 9,
      "lastWalk": "2026-08-29T21:04:11Z",
      "lastChange": "2026-08-29T21:04:11Z"
    }

### `availability`

`online` while the scanner runs. The scanner names this topic as its
MQTT Last Will with `offline` as the payload, so a scanner the kubelet
killed reads `offline`. The report it left behind stands: the counts
describe the volume, and they hold until the next walk replaces them.
