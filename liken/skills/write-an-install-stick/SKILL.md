---
name: write-an-install-stick
description: "Write a liken install image to a USB stick and prove that the bytes on the stick match the image, before a machine boots from it. Use when building install media for a new machine or a reinstall, or when a machine refuses to boot from a stick that was just written."
---

This skill is the guide at https://liken.sh/docs/guides/write-an-install-stick/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Write an install stick

[`liken stick`](https://liken.sh/docs/reference/cli/#liken-stick) writes a bootable
disk image to a file. This guide puts that file on a USB stick and
checks the copy, so that a machine that refuses to boot is a machine
problem and never a stick problem. At the end, the stick contains the
image byte for byte, and its partitions are visible to the
workstation.

You need:

* The image, `install.img`, from step 4 of the
  [install](https://liken.sh/docs/guides/install/#4-build-the-install-stick).
* A USB stick at least as large as the image.
* `sudo` on the workstation.

## 1. Find the stick

Connect the stick and list the block devices:

    lsblk -o NAME,SIZE,MODEL,LABEL,MOUNTPOINT

The stick is the device whose size and model match it. Every command
below overwrites that device, so read the name twice. If the
workstation mounted a filesystem from the stick, unmount it first:

    sudo umount /dev/YOUR-STICK*

## 2. Write the image

    sudo dd if=install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress
    sync

`oflag=direct` sends every block to the device as it goes, so the
rate that `dd` reports is the device's rate. Without it, `dd` fills
the page cache and reports a rate several times faster than the
stick can write, and the command returns before the bytes reach the
stick. `sync` waits for anything that is still in flight.

## 3. Read the stick back

The check reads exactly the image's length from the stick and
compares the two hashes. Drop the page cache first, or the read comes
from memory and proves nothing:

    sha256sum install.img
    sudo sh -c 'echo 3 > /proc/sys/vm/drop_caches'
    sudo head -c "$(stat -c %s install.img)" /dev/YOUR-STICK | sha256sum

The two hashes must be equal. Read the stick with `head`, as above,
and never with `dd iflag=direct`: direct reads fail on a length that
is not a multiple of the device's block size, and the failed read
hashes an empty stream. That hash,
`e3b0c442...`, matches nothing and looks like a result.

A hash that differs means the write did not land. Write the stick
again. A stick that fails twice is worn out, and a different stick
is the fix.

## 4. Reread the partition table

    sudo partprobe /dev/YOUR-STICK
    lsblk /dev/YOUR-STICK

The kernel still uses the partition table that the stick had before the
write until something asks it to read the table again. The listing
now shows the image's partitions. A workstation that automounts
removable media may mount the stick's EFI partition here; unmount it
before you remove the stick.

## Firmware without an EFI shell

Some firmware ships no EFI shell, and a machine with no shell gives
you no way to look at its disks from the firmware when an install
fails. The stick's EFI partition has room for one. On a workstation
with the shell package installed, mount the partition and copy the
shell to both paths the firmware searches:

    sudo mount /dev/YOUR-STICK1 /mnt
    sudo cp /usr/share/efi-shell-x64/shellx64.efi /mnt/shellx64.efi
    sudo mkdir -p /mnt/EFI
    sudo cp /usr/share/efi-shell-x64/shellx64.efi /mnt/EFI/SHELLX64.EFI
    sudo umount /mnt

Every write of the stick erases the shell, so repeat this after every
write. The package name and the path above are Ubuntu's; another
distribution names them differently.

## Which menu entry to select

The stick's menu has two entries for each machine.
`install as <name>` claims blank disks only and refuses a disk it
does not recognize. A machine that has another operating system
on the disk the manifest declares needs `wipe and reinstall as
<name>` for its first install, and so does a machine that `liken`
installed before. The [install](https://liken.sh/docs/guides/install/#5-boot-each-machine-from-the-stick)
describes both entries and what each one erases.
