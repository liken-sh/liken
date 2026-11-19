# RBAC grants watches that no code opens

Open bug, recorded on 2026-11-18. Checked against the code on
2026-11-19. `grep -rn "wakeWatch(\|informer.Source{\|informer.One{"
--include=*.go display-operator` finds watches on `pods`, `displays`,
`layouts`, the capture sidecar's pods, the `Secret` objects
`display-api-tls` and `display-capture-server` (`serving.go:164` and
`:228`), and the `ConfigMap` `extension-apiserver-authentication`
(`clientcerts.go:93`). It finds none on `resourceclaims` or on the
`ConfigMap` `display-api-ca`. The grants are at
`deploy/rbac.yaml:38` and `deploy/api.yaml:212`. No unit test reads
RBAC, so a run on the lab fleet proves the removal, as "What would
confirm it" says.

Two rules in the manifests grant `watch` on objects that no code in
display-operator watches. A grant that nothing uses widens what a
stolen token can read, and a reader of the manifest takes it as a
statement of what the code does.

- `deploy/rbac.yaml` grants the operator `get`, `list`, and `watch` on
  `resourceclaims` in every namespace. The operator lists claims in
  `listResourceClaims` (`claimholders.go`) and gets one in
  `GetResourceClaim`, and `watch.go` has no claim resource.
- `deploy/api.yaml` grants display-api `get`, `update`, and `watch` on
  the `ConfigMap` `display-api-ca`. display-api writes it with
  `ensureTrustAnchor` (`certs.go`), which gets and updates it, and
  opens no watch on it. The watches in `clientcerts.go` read another
  `ConfigMap`.

## Evidence

- `watch.go` names every resource that the operator and display-api
  watch: `displays`, `layouts`, `pods`, `secrets`, and `configmaps`.
- The comment above the `resourceclaims` rule names a get and two
  lists, and no watch.
- `grep -rn trustAnchorMap` finds one use, in `serving.go`, the call
  to `ensureTrustAnchor`.

## What would confirm it

Remove `watch` from each rule on the lab fleet, run the operator and
display-api through a claim, a capture, and a restart, and read their
logs for a `forbidden` error.

## What a fix has to decide

Whether each grant goes, or a watch is planned that needs it.
