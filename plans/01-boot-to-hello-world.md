# Boot to a hello world

Milestone 1 — Done

Boot to a hello world from an init I built: `make run` boots QEMU
and PID 1 prints to the serial console. There is no shell and no
prompt; the console is output-only by design.
1. [x] `kernel/`: vendor a pre-built vanilla kernel from Ubuntu's
   mainline builds: fetch a pinned version, verify checksums,
   extract the image and modules, and run `depmod` at build time.
2. [x] `init/`: a minimal Go init that mounts `/proc`, `/sys`, and
   `/dev`, prints a report of the hardware and kernel state it
   finds, and reaps zombie processes.
3. [x] `image/`: assemble the initramfs, which is a cpio archive. The
   whole OS is `vmlinuz` plus `liken.cpio`.
4. [x] `make run`: boot it headless in QEMU. A smoke test can watch
   the serial output for a marker, which is the starting point
   for CI. Use explicit flags (`-display none -serial stdio
   -monitor none -no-reboot`) rather than the `-nographic`
   bundle, so that each flag can carry its own explanatory
   comment.
