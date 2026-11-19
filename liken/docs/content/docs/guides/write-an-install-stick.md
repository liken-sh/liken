---
title: Write an install stick
weight: 15
description: "Write a liken install image to a USB stick and prove that the bytes on the stick match the image, before a machine boots from it. Use when building install media for a new machine or a reinstall, or when a machine refuses to boot from a stick that was just written."
---

# Write an install stick

[`liken stick`](/docs/reference/cli/#liken-stick) writes a bootable
disk image to a file. This guide writes that file to a USB stick and
checks the copy, so that when a machine refuses to boot, you know that
the stick is not the cause. At the end, the stick holds the image byte
for byte, and the workstation lists its partitions.

You need:

* The image, `install.img`, from step 4 of the
  [install](/docs/guides/install/#4-build-the-install-stick).
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

`oflag=direct` writes each block to the device immediately, so the
rate that `dd` reports is the rate of the stick. Without it, `dd`
writes into the page cache, reports a rate faster than the stick can
write, and returns before the bytes reach the stick. `sync` waits
until all pending writes finish.

## 3. Read the stick back

The check reads exactly the image's length from the stick and
compares the two hashes. Drop the page cache first, or the read comes
from memory and proves nothing:

    sha256sum install.img
    sudo sh -c 'echo 3 > /proc/sys/vm/drop_caches'
    sudo head -c "$(stat -c %s install.img)" /dev/YOUR-STICK | sha256sum

The two hashes must be equal. Read the stick with `head`, as above,
and not with `dd iflag=direct`. A direct read fails when the length
is not a multiple of the device's block size, and `sha256sum` then
prints the hash of no data, `e3b0c442...`, which looks like a real
result.

When the hashes differ, the write failed, so write the stick again.
When a stick fails twice, it is worn out, and you need a different
stick.

## 4. Reread the partition table

    sudo partprobe /dev/YOUR-STICK
    lsblk /dev/YOUR-STICK

The kernel keeps the partition table that the stick had before the
write until a program tells it to read the table again. `partprobe`
does this, so the listing now shows the image's partitions. A
workstation that mounts removable media automatically can mount the
stick's EFI partition now. Unmount it before you remove the stick.

## Firmware without an EFI shell

Some firmware has no EFI shell, so when an install fails, the
firmware cannot show you the machine's disks. You can add a shell to
the stick's EFI partition. On a workstation with the shell package
installed, mount the partition and copy the shell to both paths that
the firmware searches:

    sudo mount /dev/YOUR-STICK1 /mnt
    sudo cp /usr/share/efi-shell-x64/shellx64.efi /mnt/shellx64.efi
    sudo mkdir -p /mnt/EFI
    sudo cp /usr/share/efi-shell-x64/shellx64.efi /mnt/EFI/SHELLX64.EFI
    sudo umount /mnt

Each write of the image erases the shell, so copy it again after each
write. The package name and the path above are Ubuntu's. Another
distribution can use different ones.

## Which menu entry to select

The stick's menu has two entries for each machine.
`install as <name>` claims blank disks only and refuses a disk it
does not recognize. A machine that has another operating system
on the disk the manifest declares needs `wipe and reinstall as
<name>` for its first install, and so does a machine that `liken`
installed before. The [install](/docs/guides/install/#5-boot-each-machine-from-the-stick)
describes both entries and what each one erases.
