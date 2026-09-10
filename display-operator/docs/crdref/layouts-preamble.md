A `Layout` divides a screen into regions. Each region is a rectangle
in fractions of the screen and a label selector, and it shows the
window of the first pod whose labels match. A `Display` names the
`Layout` it shows in `spec.layout`, and a `Display` that names none
shows every window fullscreen with the newest on top. The
[guide](/docs/guides/layout/) walks through a two-region screen with
pods from two namespaces.

The order of `spec.regions` is the stacking order, last on top. The
pods never learn where they are drawn: the `Layout` is the only place
the arrangement lives, so a moved region moves every screen that
names the `Layout` and changes no pod.
