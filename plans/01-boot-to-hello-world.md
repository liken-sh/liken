# Boot to a hello world

Milestone 1 — Done

Boot to a hello world from an init I built. `make run` boots QEMU,
and PID 1 prints to the serial console. There is no shell and no
prompt. The design makes the console output-only.
1. [x] `kernel/`: Vendor a pre-built vanilla kernel from Ubuntu's
   mainline builds. Fetch a pinned version, verify its checksums,
   extract the image and modules, and run `depmod` at build time.
2. [x] `init/`: A minimal Go init. It mounts `/proc`, `/sys`, and
   `/dev`, prints a report of the hardware and kernel state it
   finds, and reaps zombie processes.
3. [x] `image/`: Assemble the initramfs, a cpio archive. The whole
   OS is `vmlinuz` plus `liken.cpio`.
4. [x] `make run`: Boot it headless in QEMU. A smoke test can watch
   the serial output for a marker; this is the starting point
   for CI. Use explicit flags (`-display none -serial stdio
   -monitor none -no-reboot`) instead of the `-nographic`
   bundle. This way, each flag can carry its own explanatory
   comment.
