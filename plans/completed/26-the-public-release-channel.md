# The public release channel

Milestone 26 — Completed. Public releases live in an object-storage
bucket at releases.liken.sh, and CI publishes to it.

Public releases (milestone 22) need a public home. The update channel
must not live on anything that it updates. An earlier plan had the
liken.sh cluster serve the channel, but that arrangement is circular.
Machines upgrade themselves from the channel, so a cluster that serves
its own updates cannot be rescued by one, and a dead cluster takes down
the means of its own reinstallation.

The channel is object storage, under its own name:

* The bytes are in a Linode Object Storage bucket named
  `releases.liken.sh`. The bucket has the name of the domain, because
  that is how Linode's custom-domain TLS finds a bucket.
  liken.sh/terraform.tf declares it, with the DNS and the credentials.
* Machines and people fetch
  `https://releases.liken.sh/<version>/release.yaml` and the artifacts
  beside it, over HTTPS. A scheduled workflow
  (.github/workflows/releases-cert.yaml) is the ACME client, because
  object storage has no ACME client of its own.
* CI publishes (.github/workflows/release.yaml). A push of a version tag
  builds the bundle, smoke-boots the same tree, and uploads the result.
  The digest discipline exists to rule out a release that someone
  assembled on a laptop. Verification is the same for a fleet and for a
  person: the publishing run prints the release document's digest, and a
  Cluster's catalog commits to that value.
* The channel is linear, and one mutable object announces it.
  `channel.yaml` at the root names the newest published version. `liken
  bundle` maintains it, and the release workflow uploads it last. This
  pointer is advisory by design. A cluster polls it to learn that a
  newer version exists, but a cluster adopts a release only through the
  digest-pinned catalog entry. A tampered pointer can misstate what
  exists, but it cannot change what a machine installs. Apart from that
  one pointer, the channel does not list itself: the objects are
  public-read, but the bucket refuses anonymous listing.

One task stays for the website: a release page, where a person reads
what changed, through a changelog that is written or derived, and which
links into the channel instead of hosting it. Signatures stay deferred
with the hardening tier, and they will land here.
