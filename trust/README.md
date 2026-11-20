# trust

`trust` is the set of CA certificates that every part of `liken`
uses to verify TLS. They are the root certificates of the Mozilla CA
program, which the curl project publishes as dated snapshots at
<https://curl.se/docs/caextract.html>. The OS and every image use the
snapshot of one date, so changing what `liken` trusts is a single
bump that you can review.

`trust` pins the bundle, checks it against the sha256 that curl.se
publishes beside it, and ships it with the notice that its license
requires. `trust` does not choose the roots. Mozilla's CA program
chooses them, and `trust` uses each snapshot exactly as curl
publishes it. The smoke check, `smoke/trust.sh`, fails when the
bundle contains a root of a compromised authority that the script
lists, so that snapshot does not ship.

The OS build and the images use the same pin. Each component that
copies the bundle lists `trust` in the `[depends]` of its
`package.toml`, so a bump of `trust` releases each of those
components too. The directory also contains the signing keys that
the bumps of more than one component check.

## The parts

- `VERSION` and `fetch.sh` are what the OS build uses. `fetch.sh`
  reads the date in `VERSION` and downloads the snapshot by the URL
  and the digest in `pin.toml`. The OS image installs the snapshot as
  the machine's trust store.
- The `trust` image on `ghcr.io/liken-sh` contains the same bundle at
  `/etc/ssl/certs/ca-certificates.crt`, which is where OpenSSL,
  GnuTLS, libcurl, and Go look for it. It also contains the notice at
  `/usr/share/doc/mozilla-ca/copyright`. Mozilla's certificate data is
  under the MPL 2.0, which requires each copy to include the license.
  An image that verifies TLS copies both files with
  `COPY --from=trust / /`.
- The image also contains `trust.cdx.json`, a CycloneDX record of the
  bundle, which `image-sbom` writes from `pin.toml`.
  syft finds no package in the bundle itself. So syft copies the
  component in this record into the SBOM of this image and of every
  image that copies the bundle.
- `keys/` contains the upstream signing keys that more than one
  `latest.sh --bump` uses. Each of those bumps verifies a signature
  with the key before it copies a digest into a pin.
  [`keys/README.md`](keys/README.md) lists each key, where it comes
  from, and the bumps that use it. No image build reads `keys/`.

`trust` is a pinned component. Its version is the date of the
snapshot as `YYYYMMDD`. The label `sh.liken.upstream.mozilla-ca`
records the date in the format that curl uses. A new snapshot can
remove roots as well as add them. When a root leaves the bundle,
every machine and every image stops trusting it.

MIT. See [LICENSE](LICENSE). The certificate data is Mozilla's, under
the MPL 2.0, as `copyright` says.
