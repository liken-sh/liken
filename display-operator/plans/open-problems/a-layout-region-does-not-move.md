# A layout region does not move

Open problem. A `Layout` places each claim in a fixed rectangle, so a
picture in picture or a camera in a corner cannot glide to a new place
or size when a press asks for it. When a person edits the `Layout`, a
region that states an enter fade glides to its new rectangle over the
fade's milliseconds, and a region that states none jumps. The pass
sends the module a `move` for a placed surface whose rectangle changed
(`enterTransition` in `display-operator/placementpass.go`), and the
module sends the client one configure at the start and glides the view
on the animation engine (`weston/layout/liken-layout.c`). So the move
borrows the enter fade's length, and a region has no length or curve
for a move of its own.

The compositor already has what the motion needs. The animation engine
in `liken-layout.c` advances on each repaint of the output, and the
arranger inside each region tweens surfaces through
`liken_arrangement_v1`
([plan 31](../completed/31-an-arranger-inside-each-region.md)). Step 5
of [root plan 85](../../../plans/completed/85-motion.md) is to run the
regions of a `Layout` on the same engine.

No workload needs it yet, and the design waits for one. The design
must answer these questions:

- Who starts the motion. A `Layout` change goes through the API server
  and `display-operator`'s pass, which root plan 85 rejected as too
  slow for a press. A motion that a person starts with `kubectl` can
  take that path. A motion that a press starts cannot.
- Which field states the length and the curve of a region's move, or
  whether every region move uses one length from `brand/motion.md`.
- What a region's arranger sees while its region moves: the region's
  final size at once, or each size along the way. The glide of an
  edited `Layout` gives the final size at once, in its one configure.
