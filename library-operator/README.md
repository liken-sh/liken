# library-operator

The media libraries of a cluster, expressed as Kubernetes resources,
and a browser for them on every screen. It runs on a
[`liken`](https://github.com/liken-sh/liken) cluster above
[`media-operator`](https://github.com/liken-sh/media-operator), which
owns the players, the plays, and the remotes. This operator owns what
there is to play.

A `Library` is one root of media on a share: a directory of films, a
directory of series, and later music, photos, and books. The operator
runs a scanner for each `Library` that reads the files and the
metadata beside them into a catalog, and it keeps that catalog on
every screen through a gossip sidecar, so a change on the share
reaches every browser within a second. The browser is a native
Wayland client that replaces the idle screen on a `Player`, lets a
person walk into a library, and starts a `Play` on the same `Player`.

The truth stays on the share. The catalog is derived and rebuildable,
and the files, the `.nfo` sidecars, and the artwork are what the
operator reads and, later, writes.

`plans/00-design.md` states the design. `plans/README.md` indexes the
plans that build it, in order.
