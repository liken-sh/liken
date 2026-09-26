# Pin CI executable inputs

Open supply-chain hardening problem, medium priority. Certificate
renewal downloads `lego` by version and executes it with cloud credentials
without checking a pinned digest or signature. External GitHub Actions
also use mutable version tags instead of commit SHAs.

## Current behavior

[releases-cert.yaml](../../.github/workflows/releases-cert.yaml) downloads
`lego_v5.2.2_linux_amd64.tar.gz` from the `go-acme/lego` GitHub release.
It extracts the binary and runs it with `LINODE_TOKEN` set from the
`RELEASES_CERT_TOKEN` secret.

The workflow and [Terraform configuration](../../liken.sh/terraform.tf)
describe that token as having Domains and Object Storage read/write
scope. The workflow uses it for DNS-01 challenges and installation of
the release bucket's TLS certificate. Nothing in the repository limits
the token to one zone. This review did not inspect the live credential.

External actions in the workflows and
[build-setup/action.yaml](../../.github/actions/build-setup/action.yaml)
use tags such as `actions/checkout@v7`, `actions/setup-go@v6`,
`actions/cache@v6`, and `j178/prek-action@v2`. Local `uses: ./...`
references come from the checked-out repository, so they do not need an
upstream action SHA suffix.

## Risk and evidence

HTTPS authenticates GitHub and protects the download in transit. It does
not stop someone from replacing a release asset or moving an action tag
at its origin. If that happens, a later run can execute different code
with no reviewed pin change in this repository. A replaced `lego` asset
could expose the cloud token. A replaced action gets the permissions and
credentials available to its job.

This review read the workflow files and found the missing checks and the
mutable references. It observed no compromise, and it did not run any
workflow with live credentials. The risk is a compromise at the origin
that no change review in this repository catches. It is not evidence of
a TLS bypass.

A checksum fetched beside the executable at run time does not help when
an attacker can replace both files. A reviewed digest committed here, or
a signature checked against an independently trusted key, gives an
integrity check that does not come from that download.

## Proposed safeguards

- Commit a reviewed SHA-256 for the `lego` archive. Verify it before
  extraction, and execute only the verified contents. A mismatch must
  fail before the step that uses the credential.
- Pin external actions by full commit SHA, and keep the human-readable
  version beside it for maintenance. Keep local composite-action
  references local.
- Add a reviewable update process for both kinds of pin. Version reporting
  can follow [milestone 48](../completed/48-check-and-update-dependency-pins.md), but
  action pins are outside that milestone's existing watched table.

A pin fixes which upstream bytes run. It does not make them trustworthy.
The selected bytes and each later pin update still need review.

## Remedy scope

The fix is a set of focused implementation safeguards. Certificate
renewal keeps its schedule, and a successful workflow run behaves as it
does now. The changes add explicit verification and reviewed update
points. They do not change the OS release API, and they do not need a
release-signing architecture.

Timeouts and update automation need implementation choices. A new trust
root for signed OS releases is a separate design from pinning the code
these jobs execute.

## Tests needed

Verify that a modified archive fails before extraction and token use,
and that matching bytes proceed. Check that every external action
reference has a full commit SHA while local references remain valid.
Rehearse a pin update and the certificate handshake check in an
authorized workflow run. This plan has not run that rehearsal.
