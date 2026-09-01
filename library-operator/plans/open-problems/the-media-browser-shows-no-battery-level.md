# The media browser shows no battery level

Open problem. The media browser is a `Player`'s idle screen where the
`Player` names this operator as its idle controller, and the idle view
it grows draws the unit's parts the way `media-operator`'s idle screen
does. A remote and a gamepad run on batteries, and neither screen
shows a level today.

The read is `bluetooth-operator`'s open problem, battery levels are not
reported, and the field is `media-operator`'s: a level per component on
the retained `Player` status, beside `connected`. When both exist, the
browser draws the level beside the part's name in its idle view, from
the same status the stock idle screen reads, and adds no read of its
own.
