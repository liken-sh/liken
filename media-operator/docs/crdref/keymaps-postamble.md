## No status

Nothing reports on a `Keymap`, so it has no status subresource, and
`kubectl get keymaps` shows each table's age. The operator compiles
a table before it creates any pod. A `press` or `action` name
outside the vocabulary above fails the `Play` that uses the table,
and the reason appears on that `Play`'s status.

## On the bus

Each `Keymap` owns one retained topic on the
[bus](/docs/reference/bus/), under the cluster's topic base:

    keymaps/<name>

The topic drops the namespace segment because a `Keymap` is
cluster-scoped. The operator is the only writer. It compiles the
table's names down to numbers and publishes the whole table as one
JSON array, retained, so a translator reads the current table the
instant it connects, and a `Keymap` edit reaches a running
translator with no pod restart. The example above compiles to:

    [{"type": 1, "code": 304, "value": 1, "action": "pause"},
     {"type": 1, "code": 311, "value": 1, "action": "seek", "amount": 30,
      "repeatDelay": 400, "repeatInterval": 300},
     {"type": 3, "code": 16, "value": 1, "action": "right"}]

Each row is one binding: an evdev event `type`, `code`, and `value`
on the left, an `action` and its `amount` on the right. A button
compiles to `EV_KEY` (type 1) with value 1, the press alone. An axis
compiles to `EV_ABS` (type 3) with the value the entry states.
`repeatDelay` and `repeatInterval` are milliseconds, and both are
absent on a binding that fires once. The translator matches numbers
and parses no name.

The operator republishes a topic only when the compiled table
differs from the last one it wrote, because a new subscriber reads
the retained value from the broker. A `Keymap` that does not compile
publishes nothing and leaves the last-good table in place, so a
broken edit does not empty a running translation, and the operator
logs the failure. When a `Keymap` is deleted, the operator clears the retained
value with an empty publish, so a deleted table leaves nothing
behind on the bus.
