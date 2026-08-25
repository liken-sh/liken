## How a Play resolves it

Each field settles on its own, `Play` then `Player` then the
default. The first tier that states a field wins it, and a field no
tier states resolves to nothing. For the two lists, omitting the
field means the tier states nothing, and an empty list is a
statement: a
`Play` with `audioLanguages: []` states no preference and overrides
the tiers below it.

The operator resolves the tiers when it creates a `Play`'s pod, and
the resolved languages become mpv's `--alang` and `--slang`
arguments. It also reports the resolved values on every `Play`'s
status, so `kubectl get play` shows what the three tiers settled
on. The operator watches `MediaPreferences`, so an edit refreshes
that resolved record on a running `Play`'s status within one pass;
the running player keeps the arguments it started with, and the
edit reaches the next `Play`'s pod.

## No status

A `MediaPreferences` is a table a person writes and nothing reports
on, so there is no status subresource, and `kubectl get
mediapreferences` shows the subtitle mode and the age.

## Not on the bus

`MediaPreferences` is the one resource of this operator with no
topics on the [bus](/docs/reference/bus/). Nothing at run time
subscribes to a preference: the operator resolves the three tiers
when it builds a pod, and the settled values travel into the pod as
arguments and environment, and onto the `Play`'s status as the
record of what resolved.
