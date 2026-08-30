# library-operator

The media libraries of a cluster, expressed as Kubernetes resources,
and a browser for them on every screen. It runs on a
[`liken`](https://github.com/liken-sh/liken) cluster above
[`media-operator`](https://github.com/liken-sh/media-operator), which
owns the players, the plays, and the remotes. This operator owns what
there is to play.

A `Library` is one root directory of media on a volume: a directory
of films, a directory of series, a music collection, a photo archive.
The operator runs a scanner for each `Library`. The scanner reads the
files and the metadata beside them into a catalog, and a gossip
sidecar replicates that catalog to every screen within a second of a
change. The browser is a native Wayland client. It replaces the idle
screen on a `Player`, lets a person walk into a library, and starts a
`Play` on that `Player`.

The volume stays the source of truth. The files, the `.nfo` sidecars,
and the artwork are what the operator reads and writes. The catalog
is derived and rebuildable.

`plans/00-design.md` states the design. `plans/README.md` indexes the
plans that build it, in order.
