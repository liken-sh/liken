# The image domain: where the pieces become a bootable system.
#
# For now, "the system" is an initramfs: a cpio archive the kernel
# unpacks into an in-memory root filesystem at boot. There is no disk,
# no bootloader, and no installer anywhere in this — the entire OS is
# two files, the kernel and this archive, and QEMU is told to boot them
# directly.
#
# The archive contains two things: liken, our init, and the Machine
# manifest that tells it who this machine is. The kernel auto-mounts
# devtmpfs (CONFIG_DEVTMPFS_MOUNT=y in our vendored config) before it
# runs init, so we don't even need to ship the traditional /dev/console
# node — device nodes are the kernel's problem, and everything else
# liken needs it makes for itself at boot.
#
# The image is assembled by staging a root filesystem tree in dist/root
# and archiving it — the staging tree on disk is exactly the filesystem
# the machine boots with, inspectable with ls.
#
# cpio flags, one story each:
#   -o          copy-out: create an archive from a list of filenames
#               read on stdin (cpio never walks directories itself,
#               which is exactly what we want — the image's contents
#               are an explicit, reviewable list)
#   -H newc     the one header format the kernel's unpacker accepts
#   -R +0:+0    everything in the image belongs to root (uid 0, gid 0),
#               not to whoever happened to run this build
dist/liken.cpio: ../init/dist/liken machine.yaml
	rm -rf dist/root
	mkdir -p dist/root/etc/liken
	cp ../init/dist/liken dist/root/liken
	cp machine.yaml dist/root/etc/liken/machine.yaml
	cd dist/root && find . | cpio -o -H newc -R +0:+0 > ../liken.cpio

.PHONY: all clean

all: dist/liken.cpio

clean:
	rm -rf dist
