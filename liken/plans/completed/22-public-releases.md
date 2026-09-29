# Public releases

Milestone 22. Completed. A published release and one command make a
USB stick that installs a cluster, with no repository and no build
step.

The goal is this path: download a release, run a command, make a USB
stick, and boot the cluster. A human or an agent can do it.
GETTING-STARTED.md, at the repository root, gives the path from end to
end. These pieces are under it:

- **The toolkit** is one static Go binary, `liken` (cli/), shipped with
  releases. It scaffolds a deployment from answers, mints or adopts a
  cluster identity, computes an admin kubeconfig, packs a deployment
  layer, builds install media, and bundles and serves the release
  channel. The CLI is a thin dispatch table. Each capability is a Go
  package in the domain that owns it (scaffold/, identity/, image/,
  releases/, disks/).

- **Composition replaces rebuilding.** The image build produces a
  generic archive: the OS, with nobody's identity in it, at a digest
  that does not change with the deployment. A deployment is a small
  second cpio (`liken layer`: manifests, identity, declared kernel
  modules). The assembly is a concatenation of the two archives,
  because the kernel unpacks concatenated archives in order into one
  filesystem.

- **One channel, and it is public.** A release (releases/) holds
  vmlinuz, the generic liken.cpio, the toolkit, and systemd-boot, named
  by digest in a release.yaml whose digests stay stable and
  publishable. Every fleet upgrades from that channel directly. A
  deployment pins the document's digest in its Cluster's catalog, and
  each machine supplies the one thing a release cannot: its own
  deployment layer, carried between its boot slots. Nothing is composed
  or hosted for each deployment separately; milestone 28 covers that
  design. The lab serves releases/dist in place of the website's
  channel.

- **One stick for each deployment, with a menu.** `liken stick` turns a
  downloaded release and a deployment layer into a GPT disk image.
  systemd-boot is at the removable-media path that the firmware runs,
  with a menu that has one entry for each machine. The entries differ
  only by liken.machine=<name>. The operator can boot any machine from
  the same stick and pick the name of the machine in front of them. The
  machine installs itself and powers off. This design replaced an
  earlier decision to use one stick for each machine. A menu that the
  deployment's own manifests populate is better than reflashed media
  for each machine, and the entries' plain-text files cost almost
  nothing. systemd-boot was chosen over GRUB, because it is smaller by
  an order of magnitude and has no configuration language, and over
  menu code inside init, because PID 1 stays non-interactive. The
  systemd-boot domain's fetch.sh gives the full reasoning.

- **Scaffolding.** `liken new` asks the dozen questions that describe a
  deployment (machines, leaders, addresses, interfaces, disks, time,
  features) and writes cluster.yaml and the machine manifests. These
  include the dev cluster's teaching comments, written in general terms.
  The machines' own strict parsers parse everything generated before it
  is written.

Some decisions are on the record. Releases are self-contained: vmlinuz
ships in the bundle, and nothing is fetched on demand. Signatures stay
deferred with the rest of the hardening tier. Integrity today comes
from the digest chain, rooted in the Cluster's catalog for fleets and
in the published release.yaml for first contact. The stick's payload
duplicates the OS artifacts that are also beside it as boot files,
about 160MB, because the installer reads only its own initramfs. An
installer that reads the stick's filesystem instead would save that
space, and this design deliberately does not take that step.

The lab proved the whole hardware path under OVMF. `make
install-stick` refreshes a node's firmware varstore to blank, the
firmware falls through to the stick, the menu renders on the serial
console, a picked entry installs the machine, and the disk boot joins
the cluster in the Ready state. The stick's console= setting is carried
into the permanent boot entries. The drill also found systemd-boot's
newest-first entry order, fixed with sort-keys so that the menu reads
node-1 first, and two domain Makefiles whose dependency lists had
fallen behind the packages their binaries compile.

The website milestones (25, 26) cover publishing releases in public.
