# trust

The CA certificates that `liken` trusts: the Mozilla CA program's root
certificates, as the curl project publishes them in dated snapshots at
<https://curl.se/docs/caextract.html>. One date pins them for every
part of the system:

- The OS build reads `VERSION` through `fetch.sh`, which verifies the
  snapshot against the sha256 that curl.se publishes beside it, and
  installs it as the machine's trust store.
- The `trust` image on `ghcr.io/liken-sh` holds the same file at
  `/etc/ssl/certs/ca-certificates.crt`, fetched by that checksum. Every
  image that verifies TLS copies it with
  `COPY --from=trust /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt`
  and names `trust` in its `[depends]`.

`trust` is a pinned component. Its version is the snapshot's date as
`YYYYMMDD`, and the label `sh.liken.upstream.mozilla-ca` carries the
date as curl names it.

## Bump the snapshot

`./latest.sh` reports the pin and the newest snapshot. `./latest.sh
--bump` writes the newest one into `VERSION`, `package.toml`, and the
`Dockerfile`. Then run `make workflows` at the top of the repository,
raise the revision of each pinned component that copies the bundle,
and run the smoke check, which fails when the three disagree or when
the image's bundle differs from the checksum curl.se publishes.

A snapshot removes trust as well as adding it. Read which roots left
the bundle before you take the bump: a root that leaves is one that
every machine and every image stops trusting.
