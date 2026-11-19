# An `Exact` `Ingress` path matches as a prefix

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

`add_ingress` in `crates/transect-network/src/addresses/index.rs`
records each path of an `Ingress` rule, but not its `pathType`.
`routes` in the same file then matches every path as a prefix that
ends at a segment boundary. So a rule with `pathType: Exact` and the
path `/foo` also reaches `/foo/bar` in `transect`'s model, and a URL
under `/foo/` can resolve to a workload that the ingress controller
does not send it to. The `Ingress` API matches an `Exact` path only
against the whole URL path.

No code in `crates/transect-network/src` reads `path_type`.

## The reproduction

A test added an `Ingress` for `exact.example.com` with the `Exact` path
`/outpost` to the `Service` `sign-in` and the `Prefix` path `/` to the
`Service` `web`, and resolved a container value of
`https://exact.example.com/outpost/start`. It wanted the workload
`web`, and failed with:

```
https://exact.example.com/outpost/start reaches [Object(Object { group: "apps", kind: "Deployment", namespace: Some("shop"), name: "sign-in" })], want web because the Exact path /outpost matches only the URL path /outpost
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should an `ImplementationSpecific` path keep matching as a prefix, and
should an `Exact` path win over a `Prefix` path of the same length?

- How to treat `ImplementationSpecific`, which the `Ingress` API leaves
  to the controller. Matching it as a prefix is the current behavior.
- Whether an `Exact` path takes precedence over a `Prefix` path of the
  same length, as the `Ingress` API specifies.
