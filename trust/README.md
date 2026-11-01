# trust

The CA certificates that `liken` trusts: the Mozilla CA program's root
certificates, as the curl project publishes them in dated snapshots at
<https://curl.se/docs/caextract.html>. One date pins them for every
part of the system:

- The OS build reads `VERSION` through `fetch.sh`, which verifies the
  snapshot against the sha256 that curl.se publishes beside it, and
  installs it as the machine's trust store.
- The `trust` image on `ghcr.io/liken-sh` holds the same file at
  `/etc/ssl/certs/ca-certificates.crt`, fetched by that checksum, and
  its notice at `/usr/share/doc/mozilla-ca/copyright`. Mozilla's
  certificate data is under the MPL 2.0, which requires that each copy
  state the license. Every image that verifies TLS copies both with
  `COPY --from=trust / /` and names `trust` in its `[depends]`.
- The image also holds `/usr/share/doc/liken/trust.cdx.json`, a
  CycloneDX record of the bundle that `image-sbom` writes from
  `pin.toml` during the build. syft finds no package in the bundle
  itself, and it copies the record's component into the SBOM of this
  image and of every image that copies it, with the purl and the
  license of `pin.toml`.

`trust` is a pinned component. Its version is the snapshot's date as
`YYYYMMDD`, and the label `sh.liken.upstream.mozilla-ca` carries the
date as curl names it.

## Bump the snapshot

`./latest.sh` reports the pin and the newest snapshot. `./latest.sh
--bump` writes the newest one into `VERSION`, `package.toml`, and the
`Dockerfile`. Then run `make workflows` at the top of the repository,
raise the revision of each pinned component that copies the bundle,
and run the smoke check. It fails when the three disagree, when the
image's bundle differs from the checksum curl.se publishes, or when the
bundle holds one of the roots of the compromised eMudhra intermediates
that `smoke/trust.sh` names.

A snapshot removes trust as well as adding it. Read which roots left
the bundle before you take the bump: a root that leaves is one that
every machine and every image stops trusting.

## Signing keys

`keys/` holds the upstream signing keys that more than one
`latest.sh --bump` checks before it copies a digest into a pin.
`keys/README.md` names each key, where it comes from, and the bumps
that read it. No image build reads `keys/`.
