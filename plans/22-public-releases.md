# Public releases

Milestone 22 — In progress: the toolkit, the generic/deployment
split, the release channel, and install-media assembly exist;
per-machine media and scaffolding remain

The goal, designed from the user's experience inward: download a
release, run a command, produce a USB stick, boot your cluster —
human or agent, no repo, no builds. The pieces, as they now exist:

- **The toolkit** is one static Go binary, `liken` (cli/), shipped
  with public releases. It mints or adopts a cluster identity,
  computes an admin kubeconfig, packs a deployment layer, assembles
  install media, and bundles, serves, and drills the release channel.
  The CLI is a thin dispatch table; each capability lives as a Go
  package in the domain that owns it (identity/, image/, releases/).

- **Composition replaces rebuilding.** The image build produces a
  generic archive: the OS, nobody's identity, a digest that never
  changes with the deployment. A deployment is a small second cpio
  (`liken layer`: manifests, identity, declared kernel modules), and
  concatenating the two is the whole assembly — the kernel unpacks
  concatenated archives in order into one filesystem, the same
  mechanism the install image uses to carry its payload.

- **One channel, and it is public.** A release (releases/) carries
  vmlinuz, the generic liken.cpio, and the toolkit, named by digest
  in a release.yaml whose digests are stable and publishable. Every
  fleet upgrades from that channel directly: a deployment pins the
  document's digest in its Cluster's catalog, and each machine
  supplies the one thing a release can't — its own deployment layer,
  carried between its boot slots. Nothing is composed or hosted per
  deployment (milestone 28 owns that design); the lab serves
  releases/dist as its stand-in for the website's channel.

Decisions on the record: install media is one stick per machine (the
media bakes liken.machine=<name> into the kernel command line);
public releases are self-contained (vmlinuz ships in the bundle, no
fetch-on-demand); signatures stay deferred with the rest of the
hardening tier — integrity is the digest chain, rooted in the
Cluster's catalog for fleets and in the published release.yaml for
first contact.

What remains:

- **Per-machine media.** `liken media` assembles an install image
  from a release and a layer; what it doesn't yet do is bake one
  machine's `liken.machine=<name>` command line into a stick, the
  piece that makes the image bootable hardware media rather than the
  lab's -kernel payload.
- **Scaffolding.** `liken new`: a deployment directory started from
  answers (names, disks, NICs), plus the getting-started document
  that walks the whole path.
- **Publishing the public bundle somewhere public** — the website
  milestones (25, 26) own where it lands.
