A `Layout` divides one screen into regions, so that pods from several
namespaces can share a monitor. Each region has a rectangle, given as
fractions of the screen, and a label selector. The region shows the
windows of the first claim to arrive whose pods' labels match the
selector and that no earlier region shows. A `Display` uses a `Layout`
when `spec.layout` names it. A `Display` with no `spec.layout` shows
every window fullscreen, with the newest on top. The
[guide](/docs/guides/layout/) shows a screen with two regions and
pods from two namespaces.

The order of `spec.regions` is the stacking order, and the last region
is on top. Only the `Layout` holds the arrangement, and the pods do
not know where their windows are drawn. So when you change a region,
every `Display` that uses the `Layout` changes, and no pod changes.
