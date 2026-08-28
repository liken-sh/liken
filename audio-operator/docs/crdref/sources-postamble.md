## The name and the resting layer

A `Source` is named by the same rule as a `Sink`, and its `spec`
works the same way: the operator writes a declared field back only
where the hardware diverges from it, validates a control against
`status.capabilities`, and invents no value. The
[`Sink` reference](/docs/reference/sinks/) has the name table and
the rules in full.

The one difference is which controls attach. A `Capture` control,
`Input Source`, and a `Mic Boost` go to the card's sources, and a
`Playback` control goes to its sinks.
