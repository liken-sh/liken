# Pin CI executable inputs

Open supply-chain hardening problem. Review priority: medium. Certificate
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
the release bucket's TLS certificate. The repository does not establish
per-zone token restrictions, and this review did not inspect the live
credential.

External actions in the workflows and
[build-setup/action.yaml](../../.github/actions/build-setup/action.yaml)
use tags such as `actions/checkout@v7`, `actions/setup-go@v6`,
`actions/cache@v6`, and `j178/prek-action@v2`. Local `uses: ./...`
references are different: their implementation comes from the checked-out
repository and does not need an upstream action SHA suffix.

## Risk and evidence

HTTPS authenticates GitHub and protects transit. It does not keep a
release asset or action tag from being replaced at its origin. If that
happens, a later run can execute different code without a reviewed pin
change in this repository. Replacing the `lego` asset could expose the
cloud token; replacing an action affects the permissions and credentials
available to its job.

The missing checks and mutable references were verified in the workflow
files. No actual compromise was observed, and no workflow was executed
with live credentials. This is an origin-compromise and change-control
risk, not evidence of a TLS bypass.

A checksum fetched beside the executable at run time is not an independent
pin when an attacker can replace both. A reviewed digest committed here,
or a signature checked against an independently trusted key, adds an
integrity reference outside that download.

## Proposed safeguards

- Commit a reviewed SHA-256 for the `lego` archive. Verify it before
  extraction, and execute only the verified contents. A mismatch must
  fail before the credential-bearing step.
- Pin external actions by full commit SHA, with the human-readable version
  retained for maintenance. Keep local composite-action references local.
- Add a reviewable update process for both kinds of pin. Version reporting
  can follow [milestone 48](../completed/48-watching-the-pins.md), but
  action pins are outside that milestone's existing watched table.

Pinning does not make upstream code trustworthy by itself. The selected
bytes and subsequent pin updates still need review.

## Remedy scope

**Focused implementation safeguards.** These changes preserve the
certificate-renewal cadence and successful workflow behavior. They add
explicit verification and reviewed update points without changing the
OS release API or requiring a release-signing architecture.

Timeouts and update automation need implementation choices. Designing a
new trust root for signed OS releases is separate from pinning the code
these jobs execute.

## Tests needed

Verify that a modified archive fails before extraction and token use,
and that matching bytes proceed. Check that every external action
reference has a full commit SHA while local references remain valid.
Rehearse a pin update and the certificate handshake check in an authorized
workflow run; this documentation change does not run that rehearsal.
