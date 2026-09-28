---
title: The liken command
weight: 30
toc: true
---

# The `liken` command

`liken` is the toolkit that you use to make and to operate a
deployment. It runs on your workstation, and every release includes
it. You do not need the repository or a build to make a cluster. To
print the full usage, run `liken` with no arguments.
[Install a cluster](/docs/guides/install/) runs the common commands
in order.

Three terms occur on this page:

* An **identity directory** holds the certificates and the join token
  that all the machines in one cluster share. Some of the files are
  private keys. Keep the directory out of version control.
* A **deployment layer** is a small archive that holds the parts of
  the operating system that are yours and not `liken`'s: your
  manifests and your identity. A machine boots the generic image and
  your layer together.
* A **release channel** is a directory that any web server can share.
  [The release channel](/docs/reference/release-channel/) describes
  its layout.

## `liken new`

    liken new <directory>

Starts a deployment. The command asks a few questions and writes a
directory of manifests: `cluster.yaml` and one file for each machine.
The comments in the files describe every field. The other commands
use this directory.

## `liken mint`

    liken mint <identity-dir>

Makes a new cluster identity: the certificate authorities and the
join token that all the machines in one cluster share.

## `liken adopt`

    liken adopt <harvest-dir> <identity-dir>

Takes identity files that you copied from the server of an existing
cluster, and arranges them as an identity directory. You can adopt
the identity of any k3s cluster. [Adopt an existing k3s
cluster](/docs/guides/adopt/) gives the steps.

## `liken kubeconfig`

    liken kubeconfig [-server URL] <deployment-dir>

Writes an administrator kubeconfig to
`<deployment-dir>/identity/kubeconfig`: the credential that `kubectl`
uses to administer the cluster. The server address comes from the
`endpoint:` in the deployment's `cluster.yaml`. Pass `-server` when
your machine reaches the cluster at a different address.

## `liken approve-reboot`

    liken approve-reboot [-server URL] <deployment-dir> <machine>

Reports what a machine waits for, and grants it one disruption. A
machine whose
[`rebootPolicy`](/docs/reference/machine/#spec--rebootpolicy) is
`Manual` stages each change and waits. This command reads the
machine's `status.pending`, prints each waiting change, and writes
the `liken.sh/approve-disruption` annotation with the staged
change's hash. The machine then takes the same path an `Auto`
machine takes: it waits for the cluster's turn, drains, and applies
the change with the smallest disruption it needs. For a credentials
change that disruption is a k3s restart, not a reboot.

The grant is one-shot. Once the change applies, its hash is no
longer pending, and the next change hashes differently, so a stale
annotation approves nothing. Running the command twice writes the
same annotation. When two changes are pending, the command approves
the reboot-class one, because a reboot applies every staged change.
When nothing is pending, it reports that and writes nothing.

## `liken request-reboot`

    liken request-reboot [-server URL] <deployment-dir> <machine>

Asks a machine to reboot when no change asks it to. Every other
reboot that `liken` performs applies a staged document, so a machine
that agrees with every document it was given has no way to reboot,
and it has no shell for a person to use. Two cases need one anyway: a
kernel driver that bound the wrong device, which releases it only at
boot, and a machine you are experimenting on. The command writes the
`liken.sh/request-reboot` annotation, valued with the identity of the
boot that is running now.

The request skips none of the cluster's coordination. The machine
waits for the cluster to grant it a reboot turn under
[`spec.disruption`](/docs/reference/cluster/#specdisruption), cordons
its node, and drains its workloads, the same as a machine applying a
staged change. The two policies control only the approval:

* `rebootPolicy: Auto` needs nothing more. The machine takes its
  turn, drains, and reboots.
* `rebootPolicy: Manual`, the default, reports `RebootPending` and
  waits, the same as it does for a staged change.
  [`liken approve-reboot`](#liken-approve-reboot) releases it,
  through the same annotation.

Nothing is staged, so the machine comes back on the documents it
already runs. The reboot promotes no system slot and proves no
release.

The request is one-shot, and nothing has to clear it. It names the
boot it was written for, so the boot that comes back is a boot the
annotation does not name. The `RebootRequestHonored` condition then
reads `True` again. Run the command a second time and it names the
new boot.

The annotation is the whole interface, so `kubectl` alone can write
it. The machine reports the value to use in the same
`RebootRequestHonored` condition:

    kubectl describe machine <name>
    kubectl annotate machine <name> liken.sh/request-reboot=<identity>

## `liken kubectl`

    liken kubectl [-server URL] <deployment-dir> [args...]

Runs the `kubectl` from your `PATH` against the deployment's
cluster. The command writes the admin kubeconfig (see
[`liken kubeconfig`](#liken-kubeconfig)), sets `KUBECONFIG`, and
hands the terminal to `kubectl`. Everything after the deployment
directory goes to `kubectl` unchanged.

## `liken stern`

    liken stern [-server URL] <deployment-dir> [args...]

Runs the `stern` from your `PATH` against the deployment's cluster,
the same way `liken kubectl` runs `kubectl`. `stern` tails the logs
of many pods at once.

## `liken flux`

    liken flux [-server URL] <deployment-dir> [args...]

Runs the `flux` from your `PATH` against the deployment's cluster,
the same way `liken kubectl` runs `kubectl`. `liken` plants the Flux
engine when the cluster declares the `flux` feature, so this is the
command that inspects it.

## `liken layer`

    liken layer <manifests-dir> <identity-dir> <output.cpio>

Packs your cluster's part of the operating system into one small
archive: your cluster manifest, your machine manifests, and your
identity.

## `liken fetch`

    liken fetch [-digest sha256:<hex>] <source-url> <version|latest> <channel-dir>

Downloads a published release from a channel into a local channel
directory, and verifies every artifact against the release document.
To take the version that the channel names as the newest, give
`latest`. `-digest` pins the release document to a known digest,
which completes the trust chain.

## `liken media`

    liken media <release-dir> <deployment.cpio> <output.cpio>

Builds a bootable install image from a downloaded release and your
deployment layer. Machines install themselves from it. Use this form
for direct-kernel boots, for example QEMU or PXE.

## `liken stick`

    liken stick [-console ttyS0] <release-dir> <deployment.cpio> <output.img>

Builds the disk image for the USB install stick: one stick for the
full deployment. Boot the stick, select an entry, and obey the
console.

The menu holds two entries for each machine in the deployment, in the
order of the machine names, and one entry for the stick itself:

    install as big
    wipe and reinstall as big
    install as little
    wipe and reinstall as little
    liken hardware report

`install as <name>` uses blank disks only. `wipe and reinstall as
<name>` erases every disk that the manifest of that machine declares,
then installs. The report entry is last in the list because it
applies to no machine. It describes the hardware of the machine in
front of it, and it changes no disk. The menu has no time limit,
because every entry writes to a disk or asks for a person. A machine that
stays at the menu does nothing until a person selects an entry.

`-console` is repeatable. It adds a `console=` argument that the
machines keep permanently. Use it to install a machine that has no
screen: the menu and all the messages also go to that port.

## `liken bundle`

    liken bundle [-slot-size 1Gi] <vmlinuz> <liken.sqfs> <boot.cpio> <microcode.cpio> <liken-cli> <systemd-boot.efi> <grub-boot.img> <grub-core.img> <licenses.md> <channel-dir> <version> [component=version ...]

Lays out a release: it copies the artifacts into the channel and
writes the `release.yaml` that names each one by its digest. The
project's own release workflow runs this command. A deployment does
not need it.

## `liken serve`

    liken serve <channel-dir> [address]

Shares a release channel over plain HTTP, and records each request in
a log. The address defaults to `:8017`.

## `liken index`

    liken index -source <url> <output-dir> < keys

Renders the index of a channel: a front page that lists every release,
a page for each release, a page for the source mirror, and the
`versions.yaml` document. Give the channel's object keys on standard
input, one key per line.
The command reads each release document from the channel at `-source`,
and writes the pages into the output directory. The contents of that
directory belong at the root of the channel, because the pages link
from the root.

The project's own release workflow runs this command. A deployment
does not need it.

The pages hold no information of their own. Each one is a view of a
document that the channel already serves, and no machine reads a page.
To repair a page, run the command again over the same channel.

## The operator plugins

An operator gives a cluster a capability, and a short command uses it:
capture a sink, pair a controller, enrich a library.
`kubectl liken <domain> <verb>` reaches each operator's command.

`kubectl` dispatches by longest prefix. For
`kubectl liken audio capture room`, `kubectl` finds
`kubectl-liken-audio` on `PATH` and runs it with `capture room`. So
each operator ships one binary named `kubectl-liken-<domain>`, and the
two-layer command needs no code of its own.

`kubectl-liken` is the `liken` toolkit under a second name. It owns
the `plugins` group, and it walks the same prefix, so three commands
reach the same binary:

* `kubectl liken audio capture room`. `kubectl` walks the prefix.
* `liken audio capture room`. The toolkit walks the prefix itself.
* `kubectl-liken-audio capture room`. An agent runs the binary
  directly.

You install the base binary the normal way, from a release download or
a package, because it is the thing that reaches the cluster. You
install the per-operator CLIs from the cluster, with `plugins sync`.

### The `plugins` group

    liken plugins sync   [-server URL] <deployment-dir>
    liken plugins list   [-server URL] <deployment-dir>
    liken plugins remove <domain>

`plugins sync` installs one CLI per operator. It lists the workloads
that carry the `cli.liken.sh/plugin` label across the cluster's
Deployments, DaemonSets, and StatefulSets, reads each one's operator
image and its version tag, and pulls the matching `-cli` image for this
workstation's architecture into `~/.liken/plugins/bin`. The CLI comes
from the same version as the running operator, so the two never drift.
The command is idempotent: a second run installs the same versions over
the same files.

`plugins list` reports each installed CLI's version and the operator
version it faces, and marks a CLI whose version has drifted from its
operator. It also names an operator that runs in the cluster with no
local CLI, so a cold start (`kubectl liken audio` with no plugin, which
`kubectl` reports as "not found") has a place to look.

`plugins remove <domain>` deletes one installed CLI.

Five domains ship a CLI today: `audio`, `bluetooth`, `display`,
`library`, and `media`. Each one's own site documents its verbs. The
first cut:

* `audio capture <sink>` streams a sink's sound to stdout (wav, and
  `--format flac|opus`). `--source` captures a Source instead.
* `display capture <output>` streams an output's framebuffer to stdout
  (mp4, or png with `--format`).
* `media capture <player>` streams what a Player shows to stdout (mp4
  or mkv).
* `bluetooth pair` opens the pairing window so you pick a device to
  approve. `bluetooth unpair <device>` removes one.
* `library reenrich <library>` requests an enrichment pass. `--only
  <fact>` limits it to one fact.

Each capture verb is a thin client over its operator's stream API. It
authenticates with the credential you already hold: the API verifies
your kubeconfig client certificate against the cluster's client
authority, or it accepts a bearer token. A capture needs no in-cluster
routing, so it works from a workstation over one port-forward.

### Install the plugins

Install the base binary from a release or a package. It is `liken`,
and it answers to `kubectl-liken` as well. Then pull the CLIs the
cluster's operators ship:

    liken plugins sync <deployment-dir>
    installed kubectl-liken-audio from ghcr.io/liken-sh/audio-operator-cli:2026.09.03-007
    installed kubectl-liken-media from ghcr.io/liken-sh/media-operator-cli:2026.09.03-007
    export PATH="/home/you/.liken/plugins/bin:$PATH"

Add `~/.liken/plugins/bin` to `PATH`. `plugins sync` prints the line
to add when the directory is not on `PATH` yet. `kubectl` and the base
binary both find the CLIs there.

Now the command reaches the operator. One capture, a sink's sound to a
file:

    kubectl liken audio capture living-room --format flac > living-room.flac

### Tab completion

Completion is a step apart from `plugins sync`, because the shell reads
it from its own files, not from the plugin directory.

Turn on `kubectl`'s completion first, because the plugin completion
builds on it:

    source <(kubectl completion bash)

Install the base binary's completion, which serves `liken` and
`kubectl-liken`:

    liken completion bash > ~/.local/share/bash-completion/completions/liken

Install each operator CLI's completion, one file per domain:

    kubectl-liken-audio completion bash \
      > ~/.local/share/bash-completion/completions/kubectl-liken-audio

A `kubectl_complete-liken-<domain>` shim on `PATH` enables
`kubectl liken <domain> <TAB>`. Completion of an object name (a sink, a
display, a Player, a peripheral, a library) reads the cluster
read-only, and it honors `--context` and `--namespace`.

### Maintain the plugins

An operator upgrade moves the operator to a new version, and the CLI
that faces it must move with it. Re-run `plugins sync` after every
operator upgrade:

    liken plugins sync <deployment-dir>

`plugins list` shows the state, and marks a CLI whose version has
drifted from its operator:

    liken plugins list <deployment-dir>

## `liken version`

    liken version

Prints the version of the toolkit.
