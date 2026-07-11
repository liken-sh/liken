# Public releases

Milestone 22 — Done

The goal, designed from the user's experience inward: download a
release, run a command, produce a USB stick, boot your cluster —
human or agent, no repo, no builds. GETTING-STARTED.md at the repo
root walks that path end to end; these are the pieces underneath it:

- **The toolkit** is one static Go binary, `liken` (cli/), shipped
  with releases. It scaffolds a deployment from answers, mints or
  adopts a cluster identity, computes an admin kubeconfig, packs a
  deployment layer, builds install media, and bundles and serves the
  release channel. The CLI is a thin dispatch table; each capability
  lives as a Go package in the domain that owns it (scaffold/,
  identity/, image/, releases/, disks/).

- **Composition replaces rebuilding.** The image build produces a
  generic archive: the OS, nobody's identity, a digest that never
  changes with the deployment. A deployment is a small second cpio
  (`liken layer`: manifests, identity, declared kernel modules), and
  concatenating the two is the whole assembly — the kernel unpacks
  concatenated archives in order into one filesystem.

- **One channel, and it is public.** A release (releases/) carries
  vmlinuz, the generic liken.cpio, the toolkit, and systemd-boot,
  named by digest in a release.yaml whose digests are stable and
  publishable. Every fleet upgrades from that channel directly: a
  deployment pins the document's digest in its Cluster's catalog, and
  each machine supplies the one thing a release can't — its own
  deployment layer, carried between its boot slots. Nothing is
  composed or hosted per deployment (milestone 28 owns that design);
  the lab serves releases/dist as its stand-in for the website's
  channel.

- **One stick per deployment, with a menu.** `liken stick` turns a
  downloaded release and a deployment layer into a GPT disk image:
  systemd-boot at the removable-media path firmware runs, and a menu
  with one entry per machine, each differing only by
  liken.machine=<name>. The operator boots any machine from the same
  stick and picks the name they are standing at; the machine
  installs itself and powers off. This *replaced* the earlier
  one-stick-per-machine decision — a menu the deployment's own
  manifests populate beats reflashing media per machine, and the
  entries' plain-text files cost nothing. systemd-boot was chosen
  over GRUB (smaller by an order of magnitude, no config language)
  and over menu code in init (PID 1 stays non-interactive); the
  systemd-boot domain's fetch.sh carries the full reasoning.

- **Scaffolding.** `liken new` interviews for the dozen facts a
  deployment is (machines, leaders, addresses, interfaces, disks,
  time, features) and writes cluster.yaml and machine manifests that
  carry the dev cluster's teaching comments, generalized. Everything
  generated is parsed back through the machines' own strict parsers
  before it is written.

Decisions on the record: releases are self-contained (vmlinuz ships
in the bundle, no fetch-on-demand); signatures stay deferred with
the rest of the hardening tier — integrity is the digest chain,
rooted in the Cluster's catalog for fleets and in the published
release.yaml for first contact; the stick's payload duplicates the
OS artifacts that also sit beside it as boot files (~160MB),
because the installer reads only its own initramfs — teaching it to
read the stick's filesystem would save the space and is deliberately
not taken on.

What the lab showed: the whole hardware path under OVMF —
`make install-stick` refreshes a node's firmware varstore to blank,
firmware falls through to the stick, the menu renders on the serial
console, a picked entry installs the machine, and the disk boot
joins the cluster Ready with the stick's console= carried into its
permanent boot entries. The drill also caught systemd-boot's
newest-first entry ordering (fixed with sort-keys, so the menu reads
node-1 first) and two domain Makefiles whose dependency lists had
fallen behind the packages their binaries compile.

Publishing releases somewhere public is the website milestones'
(25, 26) half of the story.
