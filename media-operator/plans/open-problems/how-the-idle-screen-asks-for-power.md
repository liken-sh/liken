# How the idle screen asks for power

Plan 09 gives the idle screen a panel that follows demand: the panel is
on while any holder asks for power, and it sleeps when the last holder
lets go. The display-operator's plan 07 counts the holders. This problem
is the other half, on the media side: how the standing idle pod raises
and drops its own power request over time, without dropping the draw
claim that keeps it on the screen.

The draw claim must stand the whole time. It is what puts the idle
surface on the compositor, so the pod holds it from the moment the pod
starts until the pod ends. The power request must move on a different
clock. The pod wants power while a person is near and the screen shows
status, and it wants the panel to sleep after a quiet stretch. So the
request turns on and off many times across one long-lived draw claim.

Two shapes answer this, and each has a cost.

The first is a small power claim the pod takes and releases. The pod
holds the steady draw claim, and on top of it takes a second, power-only
claim when its active window opens, then deletes that claim when the
window closes. The display-operator already counts prepared claims, so
this reuses the count with no new field and no new watch. The cost is
claim churn: the pod creates and deletes a `ResourceClaim` object on
every sleep cycle. The cycle is minutes to hours per screen, so the
churn is slow, but it is a write to the API server for each edge.

The second is a desired-power field the display-operator reads. The pod
writes its wanted power state to the API, on the `Player` or on its own
claim, and the display-operator watches that value and actuates the
panel. No object is created or deleted on the timer. The cost is a new
read across the operator boundary: the display-operator, which today
reads only claims and the card, would watch a media-side value.

The trade is claim churn against cross-operator coupling. The first
keeps one mechanism, the DRA count, and adds no new path between the
operators, at the price of slow object churn. The second keeps the API
quiet at runtime, at the price of a value the display-operator must read
from the media layer.

This owes its own design pass, because the answer sets a contract
between the two operators and touches how the display-operator learns
what a screen should do. Until it is decided, the idle screen draws with
the panel on, and the sleep behavior waits.
