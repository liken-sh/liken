# 92. The toolkit checks a release's signature before it builds install media

Milestone 92. Proposed 2026-11-02, as a stub. Nothing is built, and
the design below is a starting point, not a decision.

## The problem

A person builds install media from a downloaded release. `liken fetch`
downloads the release into a channel directory and checks each
artifact against `release.yaml`, and its `-digest` flag pins
`release.yaml` to a catalog entry. Then `liken stick` and `liken
media` read the release directory and write a disk image or an
install archive. Every machine that boots the stick installs what is
in it.

No step in that path checks who published the release. `liken fetch`
does not download the Sigstore bundles, `<file>.sigstore.json`, that
each OS release carries beside its files. `liken stick` and `liken
media` trust whatever is in the directory they read, so a release
directory that someone changed after the fetch builds a stick as
easily as a real one. A person can run `liken verify release` by
hand, but nothing requires it.

## A starting design

`liken fetch` downloads the bundle of `release.yaml` and of each
artifact, and checks them with `liken/verify` before it reports
success. It keeps the bundles in the release directory, beside the
files they sign. `liken stick` and `liken media` check the bundles of
the files they read from that directory, with no network, before they
write any output. A failed check stops the build and names the file.

## Open questions

- Whether a release from before the signatures can still build media,
  and with which flag.
- Whether a deployment that builds its own releases, as the lab does
  with `make release`, signs them, or names a different signer, or
  skips the check with a flag that the output records.
- Whether the check in `liken stick` repeats the digest check of
  `liken fetch` or trusts it. Media can be built long after the fetch,
  from a directory on another disk.
- How this shares the expected signer and the trust root with
  [milestone 91](91-a-machine-checks-a-release-signature-before-it-upgrades.md),
  so a stick and an upgrade accept the same releases.
