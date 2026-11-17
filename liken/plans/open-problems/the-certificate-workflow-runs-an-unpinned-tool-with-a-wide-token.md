# The certificate workflow runs an unpinned tool with a wide token

Open problem: supply-chain hardening of medium priority. The workflow
that renews the release channel's TLS certificate downloads `lego` by
version with no digest, and runs it with a token that can also write
the releases bucket. A replaced `lego` asset could expose that token.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

`.github/workflows/releases-cert.yaml` downloads
`lego_v5.2.2_linux_amd64.tar.gz` from the `go-acme/lego` GitHub
release, by version and with no digest. It extracts the binary and runs
it with `LINODE_TOKEN` set from the `RELEASES_CERT_TOKEN` secret. The
workflow and `liken.sh/terraform.tf` describe that token as scoped to
Domains and Object Storage read/write, so it can write the releases
bucket. The release upload key is scoped to that bucket alone. The
workflow uses the token for the DNS-01 challenge, and its next step
uses it again to install the bucket's TLS certificate. Nothing in the
repository limits the token to one zone. The review of 2026-09-11 did
not inspect the live credential.

HTTPS authenticates GitHub and protects the download in transit. It
does not stop someone from replacing a release asset at its origin. A
later run can then execute different code with no reviewed pin change
in this repository. A replaced `lego` asset could expose the cloud
token. A checksum fetched beside the executable at run time does not
help when an attacker can replace both files. A reviewed digest
committed here, or a signature checked against an independently trusted
key, gives an integrity check that does not come from that download.

## The fix

* Commit a reviewed SHA-256 for the `lego` archive. Verify it before
  extraction, and execute only the verified contents. A mismatch must
  fail before the step that uses the credential.
* Split `RELEASES_CERT_TOKEN` into two tokens, both declared in
  `liken.sh/terraform.tf`: one scoped to Domains, which only the `lego`
  step receives, and one scoped to Object Storage, which only the step
  that installs the certificate on the bucket receives. Each step then
  holds only the access its own work needs. The maintainer chose the
  split on 2026-10-04.
* Add a reviewable update process for the `lego` pin, as the
  `latest.sh` of every other pin gives (see "Pins and downloads" in the
  root `AGENTS.md`).

A pin fixes which upstream bytes run. It does not make them
trustworthy. The selected bytes and each later pin update still need
review. Certificate renewal keeps its schedule, and a successful run
behaves as it does now.

The tests verify that a modified archive fails before extraction and
before the token is used, and that matching bytes proceed. A rehearsal
in an authorized workflow run covers a pin update and the certificate
handshake check.
