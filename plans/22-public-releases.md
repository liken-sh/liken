# Public releases

Milestone 22 — In progress: the toolkit, the generic/deployment
split, and the two-layer release channels exist; install media and
scaffolding remain

The goal, designed from the user's experience inward: download a
release, run a command, produce a USB stick, boot your cluster —
human or agent, no repo, no builds. The pieces, as they now exist:

- **The toolkit** is one static Go binary, `liken` (cli/), shipped
  with public releases. It mints or adopts a cluster identity,
  computes an admin kubeconfig, packs a deployment layer, and
  publishes, serves, and drills a release channel. The CLI is a thin
  dispatch table; each capability lives as a Go package in the domain
  that owns it (identity/, image/, releases/).

- **Composition replaces rebuilding.** The image build produces a
  generic archive: the OS, nobody's identity, a digest that never
  changes with the deployment. A deployment is a small second cpio
  (`liken layer`: manifests, identity, declared kernel modules), and
  concatenating the two is the whole assembly — the kernel unpacks
  concatenated archives in order into one filesystem, the same
  mechanism the install image uses to carry its payload.

- **Releases come in two layers.** liken's own public releases
  (releases/) carry vmlinuz, the generic liken.cpio, and the toolkit,
  named by digest in a release.yaml whose digests are stable and
  publishable. A deployment's fleet upgrades from its own channel —
  the same OS composed with its layer, because the digest chain
  rooted in the Cluster's catalog must cover the exact bytes its
  machines boot. The repo's lab keeps its channel in
  dev-cluster/releases/, built from the public build by the same
  steps any deployment would follow.

Decisions on the record: install media is one stick per machine (the
media bakes liken.machine=<name> into the kernel command line);
public releases are self-contained (vmlinuz ships in the bundle, no
fetch-on-demand); signatures stay deferred with the rest of the
hardening tier — integrity is the digest chain, rooted in the
Cluster's catalog for fleets and in the published release.yaml for
first contact.

What remains:

- **Install media.** `liken media`: a bootable per-machine image
  (kernel + composed initramfs + command line) written from a public
  release plus a deployment directory, the piece that turns "download
  and run a command" into a stick a machine boots.
- **Scaffolding.** `liken new`: a deployment directory started from
  answers (names, disks, NICs), plus the getting-started document
  that walks the whole path.
- **Publishing the public bundle somewhere public** — the website
  milestones (25, 26) own where it lands.
