# The sidecar keeps a leaf that a replaced CA signed

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

display-api signs the capture sidecar's leaf with its own CA, and its
client to the sidecar trusts only that CA. `sidecarLeafUsable` in
`certs.go` decides whether the leaf in the `display-capture-server`
`Secret` can stay. It checks the SAN and the renewal time, and its
comment says that it does not check which CA signed the leaf. So when
display-api mints a new CA, for example after someone deletes the
`display-api-tls` `Secret` that holds the CA, the old leaf passes the
check and stays.
The API then refuses the sidecar's certificate on every node, and
captures fail with a 503 until the old leaf is due for renewal.

## Evidence

- `certs.go`, `ensureServingMaterial` mints a new CA when
  `display-api-tls` does not exist.
- `serving.go`, `startAuthority` calls `ensureSidecarLeaf` with that
  CA, and `ensureSidecarLeaf` returns early when `sidecarLeafUsable`
  is true.
- `keepSidecarLeaf` uses the same check on each watch event.
- `forward.go`, `dialFault` maps a `tls.CertificateVerificationError`
  to a 503: "did not present a certificate this API trusts".

## What would confirm it

A test in `serving_test.go` against `apiservertest`: start the
authority, delete `display-api-tls`, start it again, and check that
the sidecar's `Secret` holds a leaf that the new CA signed.

## What a fix has to decide

The check can verify the leaf against the CA that the API holds now,
or compare the `ca.crt` that `ensureSidecarLeaf` already writes into
the sidecar's `Secret`. When the API runs with no CA key, as with a
certificate that cert-manager issued, it writes no sidecar leaf, and
the fix must keep that case as it is.
