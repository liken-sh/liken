# The image domain: where the pieces become a bootable system.
#
# For now, "the system" is an initramfs: a cpio archive the kernel
# unpacks into an in-memory root filesystem at boot. There is no disk,
# no bootloader, and no installer anywhere in this — the entire OS is
# two files, the kernel and this archive, and QEMU is told to boot them
# directly.
#
# The archive currently contains exactly one file: liken, our init.
# The kernel auto-mounts devtmpfs (CONFIG_DEVTMPFS_MOUNT=y in our
# vendored config) before it runs init, so we don't even need to ship
# the traditional /dev/console node — device nodes are the kernel's
# problem, and everything else liken needs it makes for itself at boot.

# cpio flags, one story each:
#   -o          copy-out: create an archive from a list of filenames
#               read on stdin (cpio never walks directories itself,
#               which is exactly what we want — the image's contents
#               are an explicit, reviewable list)
#   -H newc     the one header format the kernel's unpacker accepts
#   -D          resolve those filenames from the init domain's output
#   -R +0:+0    everything in the image belongs to root (uid 0, gid 0),
#               not to whoever happened to run this build
dist/liken.cpio: ../init/dist/liken
	mkdir -p dist
	echo liken | cpio -o -H newc -D ../init/dist -R +0:+0 > dist/liken.cpio

.PHONY: all clean

all: dist/liken.cpio

clean:
	rm -rf dist
