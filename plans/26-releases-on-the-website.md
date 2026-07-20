# The public release channel

Milestone 26 — Done: the channel and CI publishing. Release pages
wait for the website's return.

Public releases (milestone 22) need a public home. The open question
here was where the bytes should live, and the answer turned out to be
a principle, not a preference: the update channel must not live on
anything it updates. The original sketch had the liken.sh cluster
serving the channel, but this is circular. Machines upgrade themselves
from the channel, so a cluster serving its own updates could never be
rescued by one, and a dead cluster would take down the means of its
own reinstallation with it.

So the channel is object storage, under its own name:

* The bytes live in a Linode Object Storage bucket named
  `releases.liken.sh`, named for the domain because that is how
  Linode's custom-domain TLS finds a bucket. It is declared in
  liken.sh/terraform.tf, alongside the DNS and the credentials.
* Machines and people fetch
  `https://releases.liken.sh/<version>/release.yaml` and the artifacts
  beside it, over real HTTPS. A scheduled workflow
  (.github/workflows/releases-cert.yaml) acts as the ACME client,
  because object storage has no ACME client of its own.
* Publishing is CI's job (.github/workflows/release.yaml). Pushing a
  version tag builds the bundle, smoke-boots the same tree, and
  uploads the result. A release that someone's laptop assembled is
  exactly what this digest discipline exists to rule out. Verification
  works the same way whether a fleet or a person is downloading: the
  release document's digest, printed by the publishing run, is the
  value that a Cluster's catalog commits to.
* The channel is linear, and it announces itself through exactly one
  mutable object: `channel.yaml` at the root, naming the newest
  published version. (`liken bundle` maintains it, and the release
  workflow uploads it last.) This pointer is advisory by design. A
  cluster polls it to learn that something newer exists, but adopting
  a release still requires the digest-pinned catalog entry, so a
  tampered pointer can misstate what exists but can never change what
  a machine installs. Beyond that one pointer, the channel does not
  list itself: objects are public-read, but the bucket refuses
  anonymous listing.

What remains for the website, when it returns, is a release page: the
place where a person learns what changed, through changelogs that are
written or derived, and which links into the channel rather than
hosting it. Signatures stay deferred with the hardening tier, and will
land exactly here when they arrive.
