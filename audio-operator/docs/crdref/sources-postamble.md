## The name and the `spec`

A `Source` has the same name rules as a `Sink`, and its `spec` works
in the same way. The operator writes a declared `volume` and `mute`
when the declaration changes and when the endpoint appears. It writes
a declared control to the hardware only when the hardware differs from
it, checks each control against `status.capabilities`, and writes no
value that you did not declare. A `Source` has no `status.session`,
and its `spec.volume` is a number, the level alone. The
[`Sink` reference](/docs/reference/sinks/) has the name table and the
rules in full.

A card's controls are split between its endpoints. A `Capture`
control, and a control that selects, boosts, or sets the automatic
gain of an input, such as `Input Source`, `Mic Boost`, or
`Auto Gain Control`, goes to the card's `Source`s. A `Playback`
control, and any other control, goes to its analog and USB `Sink`s.

## Events

The operator posts a `Normal` `Event` on a `Source` for each change of
its `Connected` and `Ready` conditions, with the condition's reason
and message. It posts a `Warning` when `Ready` becomes `False` while
`Connected` is `True`. It posts `SpecRefused` when the `spec` sets a
value that the endpoint does not accept. The capture API posts
`Captured` each time it returns a recording to a caller. The `Event`s
are in the `default` namespace. The
[`Sink` reference](/docs/reference/sinks/#events) describes each
reason.
