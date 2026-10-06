## The name and the resting layer

A `Source` is named by the same rule as a `Sink`, and its `spec`
works the same way: the operator writes a declared `volume` and
`mute` when the declaration changes and when the endpoint appears,
writes a declared control back only where the hardware diverges from
it, validates a control against `status.capabilities`, and invents no
value. A `Source` has no `status.session`, and its `spec.volume` is a
number, the level alone. The
[`Sink` reference](/docs/reference/sinks/) has the name table and
the rules in full.

The one difference is which controls attach. A `Capture` control,
`Input Source`, and a `Mic Boost` go to the card's sources, and a
`Playback` control goes to its sinks.

## Events

The operator posts a `Normal` `Event` on a `Source` for each change
of its `Connected` and `Ready` conditions, with the condition's own
reason and message, and a `Warning` when `Ready` becomes `False` while
`Connected` is `True`. It posts `SpecRefused` when the `spec` states
a value the endpoint does not take, and the capture API posts
`Captured` for each recording a caller takes. The `Event`s are in the
`default` namespace. The [`Sink` reference](/docs/reference/sinks/#events)
describes each reason.
