# An `Exact` `Ingress` path matches as a prefix

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`add_ingress` in `crates/transect-network/src/addresses/index.rs`
records each path of an `Ingress` rule, but not its `pathType`.
`routes` in the same file then matches every path as a prefix that
ends at a segment boundary. So a rule with `pathType: Exact` and the
path `/foo` also reaches `/foo/bar` in `transect`'s model, and a URL
under `/foo/` can resolve to a workload that the ingress controller
does not send it to. The `Ingress` API matches an `Exact` path only
against the whole URL path.

No code in `crates/transect-network/src` reads `path_type`.

## What would confirm it

A unit test of `routed` or of the index's lookup with an `Ingress`
that has an `Exact` path `/foo` to one `Service` and a `Prefix` path
`/` to another. A lookup of `/foo/bar` returns the first `Service`.

## What a fix has to decide

- How to treat `ImplementationSpecific`, which the `Ingress` API leaves
  to the controller. Matching it as a prefix is the current behavior.
- Whether an `Exact` path takes precedence over a `Prefix` path of the
  same length, as the `Ingress` API specifies.
