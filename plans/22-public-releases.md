# Public releases

Milestone 22 — Done

This milestone was designed from the user's experience inward. The
goal: download a release, run a command, produce a USB stick, and boot
your cluster. This works for a human or an agent, with no repo and no
build step. GETTING-STARTED.md, at the repo root, walks that path end
to end. The pieces below sit underneath it:

- **The toolkit** is one static Go binary, `liken` (cli/), shipped
  with releases. It scaffolds a deployment from answers, mints or
  adopts a cluster identity, computes an admin kubeconfig, packs a
  deployment layer, builds install media, and bundles and serves the
  release channel. The CLI is a thin dispatch table. Each capability
  lives as a Go package in the domain that owns it (scaffold/,
  identity/, image/, releases/, disks/).

- **Composition replaces rebuilding.** The image build produces a
  generic archive: the OS, with nobody's identity in it, at a digest
  that never changes with the deployment. A deployment is a small
  second cpio (`liken layer`: manifests, identity, declared kernel
  modules). Concatenating the two archives is the whole assembly,
  because the kernel unpacks concatenated archives in order into one
  filesystem.

- **One channel, and it is public.** A release (releases/) carries
  vmlinuz, the generic liken.cpio, the toolkit, and systemd-boot,
  named by digest in a release.yaml whose digests stay stable and
  publishable. Every fleet upgrades from that channel directly: a
  deployment pins the document's digest in its Cluster's catalog, and
  each machine supplies the one thing a release cannot, its own
  deployment layer, carried between its boot slots. Nothing is
  composed or hosted for each deployment separately (milestone 28
  covers that design). The lab serves releases/dist in place of the
  website's channel.

- **One stick for each deployment, with a menu.** `liken stick` turns
  a downloaded release and a deployment layer into a GPT disk image:
  systemd-boot sits at the removable-media path that firmware runs,
  with a menu that has one entry for each machine, and the entries
  differ only by liken.machine=<name>. The operator can boot any
  machine from the same stick and pick the name for the machine they
  are standing at. The machine installs itself and powers off. This
  design replaced an earlier decision to use one stick for each
  machine: a menu that the deployment's own manifests populate beats
  reflashing media for each machine, and the entries' plain-text files
  cost nothing. systemd-boot was chosen over GRUB (it is smaller by
  an order of magnitude, with no configuration language) and over menu
  code inside init (PID 1 stays non-interactive). The systemd-boot
  domain's fetch.sh carries the full reasoning.

- **Scaffolding.** `liken new` asks the dozen questions that describe
  a deployment (machines, leaders, addresses, interfaces, disks, time,
  features) and writes cluster.yaml and machine manifests. These carry
  the dev cluster's teaching comments, written in general terms.
  Everything generated is parsed back through the machines' own strict
  parsers before it is written.

A few decisions are on the record. Releases are self-contained:
vmlinuz ships in the bundle, with no fetch-on-demand. Signatures stay
deferred with the rest of the hardening tier; integrity today comes
from the digest chain, rooted in the Cluster's catalog for fleets and
in the published release.yaml for first contact. The stick's payload
duplicates the OS artifacts that also sit beside it as boot files
(about 160MB), because the installer reads only its own initramfs.
Teaching it to read the stick's filesystem instead would save that
space, and this design deliberately does not take that step.

The lab proved the whole hardware path under OVMF. `make
install-stick` refreshes a node's firmware varstore to blank, the
firmware falls through to the stick, the menu renders on the serial
console, a picked entry installs the machine, and the disk boot joins
the cluster in the Ready state, with the stick's console= setting
carried into its permanent boot entries. The drill also caught
systemd-boot's newest-first entry ordering (fixed with sort-keys, so
the menu now reads node-1 first) and found two domain Makefiles whose
dependency lists had fallen behind the packages their binaries
compile.

Publishing releases somewhere public is the other half of the story,
covered by the website milestones (25, 26).
