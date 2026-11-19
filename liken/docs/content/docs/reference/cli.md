---
title: The liken command
weight: 30
toc: true
---

# The `liken` command

`liken` is the command that you run on your workstation to create and
operate a deployment. Every release includes it, so you do not need
the repository or a build to make a cluster. Run `liken` with no
arguments to print the full usage.
[Install a cluster](/docs/guides/install/) runs the common commands
in order.

This page uses these terms:

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

Reads the identity files that you copied from a server of an existing
cluster, and writes them as an identity directory. You can adopt
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

Prints the changes that a machine waits to apply, and approves them.
A machine whose
[`rebootPolicy`](/docs/reference/machine/#spec--rebootpolicy) is
`Manual` stages each change and waits for approval. The command reads
the machine's `status.pending`, prints each change, and sets
[`spec.approvedHash`](/docs/reference/disruption/#spec--approvedhash)
to `spec.hash` on the [`Disruption`](/docs/reference/disruption/) that
covers the change. It changes nothing else, and it prints the name of
the `Disruption` that it approved. The machine then continues as an
`Auto` machine does: it waits for its turn in the cluster, drains, and
applies the change. A change that does not need a reboot, such as new
credentials, needs only a k3s restart.

For staged changes, `machine-operator` creates one `Disruption`,
`<machine>.machine-operator`, that covers all of them, so one approval
approves them all. A reboot that you asked for with
[`liken request-reboot`](#liken-request-reboot) has its own
`Disruption`, `<machine>.reboot-<boot>`. When `status.pending` lists a
change that a reboot applies, the command approves that one, because
the reboot applies every staged change. Otherwise it approves the
first entry.

An approval applies only to the changes that you saw. When
`machine-operator` stages another change, it sets a new `spec.hash`,
so the old `spec.approvedHash` no longer matches and the machine waits
for you again. After the changes are applied, `machine-operator`
deletes the `Disruption`. You can run the command again safely,
because it writes the same value.

When nothing is pending, the command says so and writes nothing. When
no `Disruption` covers the change yet, the command fails and tells you
to run it again, because `machine-operator` creates the `Disruption`
on its next pass.

## `liken request-reboot`

    liken request-reboot [-server URL] <deployment-dir> <machine>

Reboots a machine that has no staged change. A machine reboots on its
own only to apply a change, and it has no shell where you could run
`reboot`. Use this command when a kernel driver bound the wrong device
and releases it only at boot, or when you experiment on a machine.

The command creates a [`Disruption`](/docs/reference/disruption/) named
`<machine>.reboot-<boot>`, where `<boot>` is the first 12 characters of
the ID of the current boot. It sets `spec.requester` to your Kubernetes
user name, such as `kubernetes-admin`, or to `liken-cli` when the API
server does not report one. On a cluster whose release has no
`Disruption` kind, the command fails and tells you to use the `liken`
command of that release.

The machine reboots the same way as for a staged change. It waits
until the
[`MachineDisruptionBudget`s](/docs/reference/machinedisruptionbudget/)
give it a reboot turn, cordons its node, and drains its workloads. The
reboot policy decides only whether you must approve:

* `rebootPolicy: Auto` needs no approval. The machine waits for its
  turn, drains, and reboots.
* `rebootPolicy: Manual`, the default, reports `RebootPending` and
  waits until you run
  [`liken approve-reboot`](#liken-approve-reboot).

When no change is staged, the machine boots the same release with the
same settings. When a change is staged, the reboot applies it too, and
the command lists each staged change in its output.

You do not need to delete the request. It names the boot that was
running when you created it, so after the reboot it no longer applies.
The `RebootRequestHonored` condition is `True` again,
`machine-operator` sets the `Disruption`'s `Complete` condition to
`True`, and `cluster-operator` deletes the `Disruption` one day later.
When you run the command again, it creates a request for the new
boot.

You can also make the request with `kubectl` alone. Create a
`Disruption` with no `spec.hash`. `machine-operator` writes the ID of
the running boot into `spec.hash` and sets the `Machine` as the
owner:

    kubectl create -f - <<EOF
    apiVersion: liken.sh/v1alpha1
    kind: Disruption
    metadata:
      name: <name>.reboot-now
    spec:
      machine: <name>
      requester: kubernetes-admin
      kind: Reboot
      reason: the driver bound the wrong device
    EOF

The `RebootRequestHonored` condition shows the ID of the running boot,
so you can also copy it into `spec.hash` yourself:

    kubectl describe machine <name>

## `liken kubectl`

    liken kubectl [-server URL] <deployment-dir> [args...]

Runs the `kubectl` from your `PATH` against the deployment's
cluster. The command writes the admin kubeconfig (see
[`liken kubeconfig`](#liken-kubeconfig)), sets `KUBECONFIG`, and
starts `kubectl` on your terminal. Everything after the deployment
directory goes to `kubectl` unchanged.

## `liken stern`

    liken stern [-server URL] <deployment-dir> [args...]

Runs the `stern` from your `PATH` against the deployment's cluster,
the same way `liken kubectl` runs `kubectl`. `stern` tails the logs
of many pods at once.

## `liken flux`

    liken flux [-server URL] <deployment-dir> [args...]

Runs the `flux` from your `PATH` against the deployment's cluster,
the same way `liken kubectl` runs `kubectl`. When the cluster declares
the `flux` feature, `liken` installs Flux, and this command inspects
it.

## `liken layer`

    liken layer <manifests-dir> <identity-dir> <output.cpio>

Packs the deployment layer into one archive: your cluster manifest,
your machine manifests, and your identity.

## `liken fetch`

    liken fetch [-digest sha256:<hex>] <source-url> <version|latest> <channel-dir>

Downloads a published release from a channel into a local channel
directory, and verifies every artifact against the release document.
Give `latest` to download the newest release on the channel. With
`-digest`, the command fails unless the release document has that
digest. Use the digest of the release's entry in your `Cluster`'s
catalog, so that the download matches the release that the cluster
names, and not only what the channel serves.

## `liken verify release`

    liken verify release [-source URL] [-digest sha256:<hex>] <version>

Checks that a published release is the one that `liken`'s CI signed.
The command checks the Sigstore bundle of `release.yaml`, then
downloads each artifact that `release.yaml` names and checks its size,
its sha256, and its own bundle. Each signature must come from
`component-liken.yaml` at a release tag. The command prints one line
for each file that passes, and it stops at the first file that fails.
At the end it prints the number of components in the release's SBOM,
by the value of their `liken:source` property.

`-source` defaults to `https://releases.liken.sh`. `-digest` works as
it does for `liken fetch`. The command includes Sigstore's trusted
root, so the check fetches no key from the network. [Verify a release or an
image](/docs/guides/verify/) gives the steps and the meaning of each
failure.

## `liken verify image`

    liken verify image <reference>

Checks that an image is one that `liken`'s CI published. The command
resolves the reference to the digest of the image's index, and checks
the cosign signature of that digest: it must come from a component's
workflow at `main` or at a release tag. Then it reads the SPDX SBOM
and the SLSA provenance that the index has for each platform, and
prints the signer and the number of packages in each SBOM.

## `liken sbom`

    liken sbom [-json] [-source URL] <version|reference>

Prints the name, version, and package URL of each package in the SBOM
of a release or of an image. An argument of the form `2026.11.01-001`
names a release on the channel that `-source` names, and any other
argument names an image. The command checks the signature first, the
same way as `liken verify`: for a release, the bundles of
`release.yaml` and `sbom.cdx.json` and the SBOM's digest. `-json`
prints the document itself, CycloneDX for a release and SPDX for an
image, for a vulnerability scanner to read.

## `liken media`

    liken media <release-dir> <deployment.cpio> <output.cpio>

Builds a bootable install image from a downloaded release and your
deployment layer. Machines install themselves from it. Use this form
for direct-kernel boots, for example QEMU or PXE.

## `liken stick`

    liken stick [-console ttyS0] <release-dir> <deployment.cpio> <output.img>

Builds the disk image for the USB install stick. One stick installs
every machine in the deployment. Boot a machine from the stick, select
an entry, and follow the instructions on the console.

The menu has two entries for each machine in the deployment, sorted by
machine name, and a last entry for the machine in front of you. For
machines named `big` and `little`, the menu is:

    install as big
    wipe and reinstall as big
    install as little
    wipe and reinstall as little
    liken hardware report

`install as <name>` installs only on blank disks. `wipe and reinstall as
<name>` erases every disk that the machine's manifest declares, then
installs. `liken hardware report` belongs to no machine. It describes
the hardware of the machine that booted the stick, and it writes to no
disk. The menu has no timeout, so a machine waits at the menu until a
person selects an entry.

`-console` can be given more than once. It adds a `console=` argument
to the kernel command line of each installed machine, and the machines
keep it after the install. Use it for a machine that has no screen. The
menu and all the messages then also appear on that port.

## `liken bundle`

    liken bundle [-slot-size 1Gi] <vmlinuz> <liken.sqfs> <boot.cpio> <microcode.cpio> <liken-cli> <systemd-boot.efi> <grub-boot.img> <grub-core.img> <licenses.md> <sbom.cdx.json> <channel-dir> <version> [component=version ...]

Builds a release in a channel directory. It copies the artifacts into
the channel and writes `release.yaml`, which lists each artifact with
its digest. `-slot-size`, `1Gi` by default, is the size of a boot
slot, and the command fails when the artifacts and their headroom do
not fit in it. The project's release workflow runs this command. A
deployment does not need it.

## `liken serve`

    liken serve [-firmware dir] <channel-dir> [address]

Serves a release channel over plain HTTP under `/releases/` and logs
each request. The address defaults to `:8017`. `-firmware` also serves
a directory of fwupd catalogs and cabinets under `/firmware/`.

## `liken index`

    liken index -source <url> <output-dir> < keys

Writes the index of a channel: a front page that lists every release,
a page for each release, a page for the source mirror, and the
`versions.yaml` document. Give the channel's object keys on standard
input, one key per line. The command reads each release document from
the channel at `-source` and writes the pages into the output
directory. Upload that directory's contents to the root of the
channel, because the pages link to each other from the root.

The project's release workflow runs this command. A deployment does
not need it.

Each page is generated from documents that the channel already serves,
and no machine reads a page. To regenerate a page, run the command
again over the same channel.

## The operator plugins

Some operators ship a command of their own, for tasks such as
capturing a sink's sound, pairing a controller, or enriching a
library. You run these commands as `kubectl liken <domain> <verb>`.

`kubectl` runs a plugin by finding the longest matching executable
name on `PATH`. For `kubectl liken audio capture room`, `kubectl` finds
`kubectl-liken-audio` and runs it with the arguments `capture room`.
So each operator ships one binary named `kubectl-liken-<domain>`.

The `liken` command also runs under the name `kubectl-liken`. It runs
the `plugins` group itself, and it finds an operator's binary for any
other domain the same way `kubectl` does. So each of these commands
runs the same binary:

* `kubectl liken audio capture room`, where `kubectl` finds the binary.
* `liken audio capture room`, where the toolkit finds the binary.
* `kubectl-liken-audio capture room`, which runs the binary directly,
  for example from an agent.

You install `liken` from a release download or a package, because you
need it before you can reach the cluster. Then `plugins sync` installs
the operators' commands from the cluster.

### The `plugins` group

    liken plugins sync   [-server URL] <deployment-dir>
    liken plugins list   [-server URL] <deployment-dir>
    liken plugins remove <domain>

`plugins sync` finds the Deployments, DaemonSets, and StatefulSets in
the cluster that have the `cli.liken.sh/plugin` label, reads the image
and version tag of each operator, and installs the CLI from the
matching `-cli` image into `~/.liken/plugins/bin`. So each CLI has the
same version as its running operator. The `-cli` images are built for
`linux/amd64` only, so on a workstation with another architecture
`plugins sync` fails and installs nothing. You can run the command
again safely.

`plugins list` shows the version of each installed CLI and of its
operator, and marks a CLI whose version differs. It also lists each
operator in the cluster that has no installed CLI. Look here when
`kubectl liken audio` reports "not found".

`plugins remove <domain>` deletes one installed CLI.

These domains ship a CLI: `audio`, `bluetooth`, `display`, `library`,
`media`, and `observatory`. Each operator's manual documents its
verbs. Some examples:

* `audio capture <sink>` writes a sink's sound to standard output as
  WAV, or as FLAC or Opus with `--format flac|opus`. `--source`
  captures a Source instead.
* `display capture <output>` writes an output's framebuffer to standard
  output as MP4, or as PNG with `--format`.
* `media capture <player>` writes what a Player shows to standard
  output as MP4 or MKV.
* `bluetooth pair` opens the pairing window so that you can approve a
  device. `bluetooth unpair <device>` removes one.
* `library reenrich <library>` requests an enrichment pass. `--only
  <fact>` limits it to one fact.
* `library retry <library>` requests another enrichment pass only for
  the titles where a provider found nothing or failed. `--only <fact>`
  limits it to one fact.
* `observatory set-up <kind> <name>` confirms that you set up a
  telescope or a device for the night, until dawn or until the time in
  `--until`. `observatory take-down <kind> <name>` withdraws the
  confirmation.

A capture command reads from its operator's streaming API with the
credential in your kubeconfig: a client certificate that the cluster's
client certificate authority signed, or a bearer token. It needs no
route into the cluster, and it works from a workstation through one
port-forward.

### Install the plugins

Install `liken` from a release or a package. Then install the CLIs
that the cluster's operators ship:

    liken plugins sync <deployment-dir>
    installed kubectl-liken-audio from ghcr.io/liken-sh/audio-operator-cli:2026.09.03-007
    installed kubectl-liken-media from ghcr.io/liken-sh/media-operator-cli:2026.09.03-007
    export PATH="/home/you/.liken/plugins/bin:$PATH"

Add `~/.liken/plugins/bin` to `PATH`. When the directory is not on
`PATH`, `plugins sync` prints the line to add. Then `kubectl` and
`liken` both find the CLIs, and you can run an operator's commands.
This one saves a sink's sound to a file:

    kubectl liken audio capture living-room --format flac > living-room.flac

### Tab completion

`plugins sync` does not install completion, because the shell reads
completion scripts from its own directories and not from the plugin
directory.

Turn on `kubectl`'s completion first, because plugin completion
depends on it:

    source <(kubectl completion bash)

Install the completion of `liken`, which covers both `liken` and
`kubectl-liken`:

    liken completion bash > ~/.local/share/bash-completion/completions/liken

Install each operator CLI's completion, one file per domain:

    kubectl-liken-audio completion bash \
      > ~/.local/share/bash-completion/completions/kubectl-liken-audio

A `kubectl_complete-liken-<domain>` executable on `PATH` enables
`kubectl liken <domain> <TAB>`. To complete the name of an object,
such as a sink, a display, a Player, a peripheral, or a library, the
CLI reads the cluster with the `--context` and `--namespace` that you
gave, and changes nothing.

### Maintain the plugins

A CLI must have the same version as its operator. Run `plugins sync`
again after every operator upgrade:

    liken plugins sync <deployment-dir>

`plugins list` marks a CLI whose version differs from its operator's:

    liken plugins list <deployment-dir>

## `liken version`

    liken version

Prints the version of the toolkit.
