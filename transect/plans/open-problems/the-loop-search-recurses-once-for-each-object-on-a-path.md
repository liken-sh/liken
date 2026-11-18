# The loop search recurses once for each object on a path

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`Search::visit` in `crates/transect-graph/src/edges/loops/components.rs`
finds the strongly connected components of the object graph with
Tarjan's algorithm, and it recurses once for each object on the current
depth-first path. Its doc says that the recursion can be as deep as the
number of objects in the graph. The graph holds every object that
`transect` watches, and the search runs on a Tokio worker thread, whose
stack has a fixed size. A large cluster with a long chain of edges can
overflow that stack and abort the process.

Nobody has measured how deep the path gets on a real cluster, or how
much stack one call uses.

## What would confirm it

A unit test that builds a chain of 100,000 objects, each with an edge
to the next, and calls `components` on a thread with a 2 MiB stack. The
thread overflows its stack.

## What a fix has to decide

Whether to write the search with an explicit stack, which removes the
limit, or to state the largest graph that the current form handles.
