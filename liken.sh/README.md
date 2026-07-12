# The liken.sh deployment

This directory is a complete, real deployment of liken: the cluster
that serves the project's own website and release channel. It is
deliberately the project's first production deployment — the cluster
that hosts liken's releases upgrades itself from those same releases,
so every rough edge in that story is felt here before anyone else
feels it.

Everything the deployment needs lives here, organized the way any
liken deployment would be:

* `terraform.tf` — the infrastructure: DNS, the machine, its disks,
  release storage, and the CI upload credential.
* `cluster.yaml` and `machines/` — the fleet, in liken's own
  vocabulary.
* `Makefile` — how the OS gets built for this machine.
* `grub.cfg` — the boot entry, in GRUB's language (below explains
  why GRUB is here at all).
* `identity/` — the cluster's minted certificate authorities and
  join token, created by `make identity` and never committed.

## The machine, and why it boots the way it does

The cluster is one Linode nanode: 1 GB of memory, the smallest
machine Linode sells. That is a deliberate constraint, not thrift —
liken claims to be a lightweight OS, and the claim is only honest if
the project's own production cluster runs comfortably inside 1 GB.

Linode shaped this deployment in a second way: **Akamai's hosts boot
guests BIOS-style only — there is no UEFI option at all.** liken
normally boots with no bootloader of its own: the firmware's boot
entries load the kernel's EFI stub directly, and upgrades actuate by
flipping firmware variables (BootNext to try a new slot once,
BootOrder to keep it). None of that machinery exists under BIOS. So
this machine's disk carries its own GRUB — first stage in the MBR,
the rest in a BIOS boot partition tucked into the GPT's alignment
gap, and its config and modules under `/boot/grub` on the active
slot — and Linode's "direct disk" boot setting simply executes what
the MBR carries. (Linode's own GRUB 2 loader is a dead end here: it
reads its config from the disk treated as one whole-disk filesystem,
the way Linode's own images are laid out, and never looks inside a
partition table.)

We considered moving to a cloud with UEFI guests and decided to stay.
BIOS-only machines are not a Linode quirk; they are a standing fact of
the hardware liken will meet — old servers, cheap virtual machines,
other clouds' legacy tiers. An OS that only knows how to upgrade
itself where UEFI firmware exists is narrower than liken means to be,
and this deployment forces the missing capability honestly: teaching
liken to actuate upgrades by rewriting GRUB's configuration instead of
firmware variables is [its own milestone](../plans/30-bios-upgrades.md).
Until that lands, updating this machine means shipping a fresh system
disk, which the next section makes cheap and safe.

## How the OS ships

The OS reaches this machine as a disk image. `make` here runs a real
liken install in a local QEMU guest — the installer boots and runs
exactly as it would on hardware — against a 3 GiB raw disk file, then
plants GRUB on the result. Terraform uploads that file as a Linode
custom image and stamps it onto the machine's system disk.

Shipping is an explicit act, never a side effect of a routine apply
(image builds aren't byte-reproducible, so Terraform is told not to
treat a rebuilt file as intent):

    make
    linode-cli linodes shutdown $(terraform output -raw node_id)
    terraform apply -replace=linode_image.system
    # restore the MBR boot code (below), then:
    linode-cli linodes boot $(terraform output -raw node_id) \
      --config_id $(terraform output -raw boot_config_id)

The extra step is Linode's one distortion of the image: deploys
preserve every partition faithfully but zero the MBR's boot code, so
GRUB's first stage has to be put back before the disk can boot. From
a rescue boot with ssh enabled (mind the device letter — Finnix
enumerates its own devices first, so check lsblk for the 3 GiB disk):

    dd if=image/disk.img bs=1 count=440 | \
      ssh root@liken.sh 'dd of=/dev/sdX bs=1 count=440 conv=notrunc,fsync'

One more Linode behavior to wait out: creating the disk from the
image leaves a failed disk_resize event behind ("couldn't set
credentials" — the API requires a login credential to inject, and a
raw image has no filesystem to inject it into). That failure can
grind on in the background for up to an hour and appears to zero the
boot code again when it dies, undoing a restore done too early.
Watch `linode-cli events list` until the disk_resize settles before
restoring the MBR.

A ship erases the system disk entirely, and the machine's storage is
split by lifetime to make that safe. The system disk (3 GiB: boot
slots, machine state, scratch) is disposable and rewritten by every
ship; the data disk (the rest of the nanode's storage: the cluster's
database, the container store, pod volumes) is created blank exactly
once, claimed by liken on the machine's first boot, and never shipped
to again. Reinstalling the OS costs a reboot, not the cluster.

The 3 GiB figure is Linode's constraint: uploaded images are capped at
6 GB uncompressed, which is what pushed everything durable onto a
second disk in the first place. The image is also built slightly
smaller than the disk Terraform gives it, because Linode's advertised
disk sizes don't land on exact byte counts — liken notices a disk
larger than its partition table claims and grows the table to the real
end on first boot.

## Reaching the cluster

The API server's certificate names the node's cluster-segment address
(the VLAN, where peers and future nodes live), so reaching it over the
public internet means telling kubectl which name to expect:

    kubectl --kubeconfig identity/kubeconfig \
      --server=https://50.116.63.57:6443 --tls-server-name=10.10.0.1 \
      get nodes
