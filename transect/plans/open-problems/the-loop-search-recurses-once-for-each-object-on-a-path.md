# The loop search recurses once for each object on a path

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

`Search::visit` in `crates/transect-graph/src/edges/loops/components.rs`
finds the strongly connected components of the object graph with
Tarjan's algorithm, and it recurses once for each object on the current
depth-first path. Its doc says that the recursion can be as deep as the
number of objects in the graph. The graph holds the top owners that the
edge kinds in `COUNTED` join (`Graph::loops` in `loops/mod.rs`), and
the search runs while the model is built, on a thread of
`spawn_blocking`, whose stack has a fixed size. A large cluster with a long chain of edges can
overflow that stack and abort the process.

Nobody has measured how deep the path gets on a real cluster, or how
much stack one call uses.

## The reproduction

A test built a chain of 100,000 nodes, each with an edge to the next,
and called `components` on a thread with a stack of 2 MiB. It wanted
100,001 components. In a debug build the thread overflowed its stack
and aborted the test process:

```
thread '<unknown>' has overflowed its stack
fatal runtime error: stack overflow, aborting
```

No build with optimizations was tried, and its frames are smaller.

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should the search use a stack of its own in place of recursion, or
should the code state the largest graph that the recursion handles?

Whether to write the search with an explicit stack, which removes the
limit, or to state the largest graph that the current form handles.
