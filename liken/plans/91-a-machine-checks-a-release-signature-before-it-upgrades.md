# 91. A machine checks a release's signature before it upgrades

Milestone 91. Proposed 2026-11-02, as a stub. Nothing is built, and
the design below is a starting point, not a decision.

## The problem

A machine trusts a release through one digest. The `Cluster`'s
`spec.releases.catalog` names the sha256 of each release's
`release.yaml`, and `release.yaml` names the digest of every
artifact. So a machine installs exactly the bytes that the person who
wrote the catalog entry chose. It does not check who published those
bytes. A catalog entry copied from a wrong or compromised channel
installs as easily as a real one.

Each OS release already carries a Sigstore bundle,
`<file>.sigstore.json`, beside each of its files. The publish job signs
them keyless, as `component-liken.yaml` at the release's tag. A person
can check the bundles with `liken verify release`. A machine does not
check them.

## A starting design

`liken/verify` holds the checks that `liken verify release` runs: the
bundle of each file against the expected workflow identity, with
Sigstore's trust root pinned inside the binary. `machine-operator`
could run the same check on `release.yaml` after it downloads the
document and before it stages any artifact. A failed check would stop
the download and set a condition with the reason, the same way a
digest mismatch does now.

## Open questions

- Where the expected signer comes from. A deployment that builds its
  own releases signs them as a different workflow, or with a key. The
  identity could be a field of `spec.releases`, with `liken`'s own
  workflow as the default.
- Whether the check is required or opt-in, and what a release from
  before the signatures does under a required check.
- How the trust root moves. It is pinned in the binary, so a machine
  checks with the root of the release it runs. A rotation of
  Sigstore's root must reach the fleet before the old root expires.
- Whether `cluster-operator` checks a release when the catalog entry
  is added, so a person sees the failure on the `Cluster` before any
  machine downloads.
