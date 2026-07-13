# The public release channel

Milestone 26 — Done: the channel and CI publishing. Release pages
wait for the website's return.

Public releases (milestone 22) need a public home. The open question
here was where the bytes live, and the answer turned out to be a
principle, not a preference: **the update channel must not live on
anything it updates.** The original sketch had the liken.sh cluster
serving the channel, which is circular — machines upgrade themselves
from the channel, so a cluster serving its own updates could never be
rescued by one, and a dead cluster would take the means of its
reinstallation down with it.

So the channel is object storage, under its own name:

* The bytes live in a Linode Object Storage bucket named
  `releases.liken.sh` — named for the domain because that is how
  Linode's custom-domain TLS finds a bucket — declared in
  liken.sh/terraform.tf alongside the DNS and the credentials.
* Machines and people fetch
  `https://releases.liken.sh/<version>/release.yaml` and the
  artifacts beside it, over real HTTPS. A scheduled workflow
  (.github/workflows/releases-cert.yaml) acts as the ACME client,
  since object storage has none of its own.
* Publishing is CI's job (.github/workflows/release.yaml): pushing a
  version tag builds the bundle, smoke-boots the same tree, and
  uploads — a release someone's laptop assembled is exactly what the
  digest discipline exists to rule out. Verification is the same
  story whether a fleet or a person is doing the downloading: the
  release document's digest, printed by the publishing run, is what a
  Cluster's catalog commits to.
* The channel does not enumerate itself: objects are public-read but
  the bucket refuses anonymous listing. Discovery is the Cluster
  document's catalog — an index is a website concern, not a channel
  concern.

What remains for the website, when it returns: a release page — the
place a person learns what changed, with changelogs written or
derived — that links into the channel rather than hosting it. And
signatures stay deferred with the hardening tier, landing exactly
here when they come.
