# The media browser shows no battery level

Open problem. The media browser is a `Player`'s idle screen where the
`Player` names this operator as its idle controller, and the idle view
it grows draws the unit's parts the way `media-operator`'s idle screen
does. A remote and a gamepad run on batteries, and neither screen
shows a level today.

The read is `bluetooth-operator`'s plan 06, which puts the level on
the `Peripheral`, and `media-operator` folds it onto the retained
`Player` status as `battery` per component, beside `connected`. The
browser draws the level beside the part's name in its idle view, from
the same status the stock idle screen reads, and adds no read of its
own. What remains is the drawing.
