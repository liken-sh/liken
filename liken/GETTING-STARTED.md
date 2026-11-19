# Getting started

These steps take you from a downloaded `liken` release and a set of
blank machines to `kubectl get nodes`. You do not need this repository
or a build. The release has every file you need, including the
`liken` toolkit that runs these steps. The manual has the full
procedure: <https://liken.sh/docs/guides/install/>.

## 1. Download a release and check it

A release has these files and a document, `release.yaml`, that lists them:

    vmlinuz               the Linux kernel
    liken.sqfs            the operating system: a read-only filesystem
                          image a machine mounts as its root
    boot.cpio             the small initramfs: init and the early
                          boot's kernel modules
    microcode.cpio        CPU microcode, loaded before everything else
    liken                 the toolkit
    systemd-bootx64.efi   the install stick's boot menu, for UEFI
    grub-boot.img         the BIOS boot loader's first stage
    grub-core.img         the BIOS boot loader's second stage
    LICENSES.md           third-party license notices
    sbom.cdx.json         the release's SBOM, in CycloneDX
    release.yaml          each of the above, by sha256 digest, and
                          which kernel, k3s, and other components are
                          inside

Download all of them into one directory. The toolkit checks each file
against `release.yaml`, so the stick build fails when a file is missing
or differs.

A release's version is the release date and a serial number, for
example 2026.07.11-001. The `components` section of `release.yaml`
lists the versions of the kernel, k3s, and the other components.

To check the files yourself, compare the digest of `release.yaml` with
the one on the release's page, and then compare each file with its
`sha256:` line in `release.yaml`:

    sha256sum vmlinuz liken.sqfs boot.cpio microcode.cpio liken \
        systemd-bootx64.efi grub-boot.img grub-core.img LICENSES.md \
        sbom.cdx.json

Then make the toolkit executable:

    chmod +x liken

`liken fetch` downloads and checks a whole release with one command.
The manual gives the steps:
<https://liken.sh/docs/guides/install/>.

## 2. Describe your cluster

    ./liken new mycluster

This command asks for your machines' names, which of them are
leaders, their addresses, and their disks. Then it writes
`mycluster/`, with a `cluster.yaml` file and one manifest for each
machine. A comment on each field explains what it does. Keep
`mycluster/` in version control, because it is the declaration of
your cluster.

## 3. Mint the cluster's identity

    ./liken mint mycluster/identity

The identity is the certificate authorities and the join token that
make your machines one cluster. The files include private keys, and
the `.gitignore` file that `liken new` wrote keeps them out of version
control. If you already run a k3s cluster, do not run `liken mint`. Use
`liken adopt` instead, to join machines to that cluster. To list and
read about each command, run `./liken` with no arguments.

## 4. Pack your layer and build the stick

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick . mycluster/deployment.cpio mycluster/install.img

The layer is a small archive of your manifests and your identity.
`liken stick` combines the release with your layer into one bootable
disk image. Here `.` is the directory with `release.yaml` and the
files it lists.

For a machine with no screen, name its serial port:

    ./liken stick -console ttyS0 . mycluster/deployment.cpio mycluster/install.img

The boot menu and all messages then go to that port, and the
installed machines keep the setting.

Write the image to a USB stick. This command overwrites the device, so
check the device name first.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

## 5. Boot each machine from the stick

You must be at each machine while it installs, to select the boot
entry and to press Enter at the end, so give it a keyboard and a
screen, or a serial console. After the installation, it needs
neither.

Connect the stick and boot the machine. You may need the firmware's
boot-device menu to boot from the stick. The stick's menu has two
entries for each machine, then an entry for the hardware report:

    install as big
    wipe and reinstall as big
    install as little
    wipe and reinstall as little
    liken hardware report

The menu has no timeout, so nothing boots until you select an entry.

Run the hardware report before you install a machine: select `liken
hardware report`. This boot changes no disk. It writes a proposed
manifest to the stick as `hardware-report.yaml`, with the machine's
`spec.modules`, `spec.network`, and `spec.storage`. Copy those sections
into the machine's manifest in `mycluster/`, then build the stick again
with step 4. The manual gives the steps:
<https://liken.sh/docs/guides/install/#first-run-the-hardware-report>.

To install, select `install as <name>` for the machine in front of you.
The machine partitions its blank disks, copies the operating system
onto them, registers itself with its firmware, and then waits for you:

    liken: installed to slot A. Remove the stick, then press Enter to power off. The next power-on boots from the disk.

Remove the stick and press Enter. The machine powers off, and when
you power it on again, it boots from its own disk. Use the same stick
for each machine, and start with the first leader.

`install as <name>` uses only blank disks. To replace an earlier
`liken` installation, select `wipe and reinstall as <name>`, which
erases the disks that the machine's manifest declares and then
installs the machine.

Each machine connects to the others at the addresses you declared.
The leaders form the control plane, and the followers join it.

## 6. Talk to your cluster

    ./liken kubeconfig mycluster

This command writes an administrator kubeconfig to
`mycluster/identity/kubeconfig`, with the `endpoint:` of your
`cluster.yaml` as its server. If your workstation reaches the cluster
at a different address, pass `-server`:

    ./liken kubeconfig -server https://203.0.113.5:6443 mycluster

Then run:

    kubectl --kubeconfig mycluster/identity/kubeconfig get nodes

Each machine shows as Ready. The cluster is an ordinary Kubernetes
cluster, and `liken` adds these resources:

    kubectl get clusters      the fleet's configuration, as one document
    kubectl get machines      the configuration and state of each machine

To change the configuration, edit those resources. To upgrade to a
new `liken` release, add it to `spec.releases.catalog` on the Cluster
and point `spec.version` at it. The upgrade needs no new install
stick and no work at each machine. Each machine downloads, verifies,
and trial-boots the new version itself, one machine at a time.

To apply these edits from a git repository, declare the `flux`
feature. The cluster then syncs your manifests and your workloads from
the repository. The manual gives the steps:
<https://liken.sh/docs/guides/gitops/>.
