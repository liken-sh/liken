# Boot to a hello world

Milestone 01. Completed. The machine boots into a Go init that prints
to the serial console.

`make run` boots QEMU, and PID 1 prints to the serial console. There is
no shell and no prompt, because the design makes the console
output-only.
1. [x] `kernel/`: Vendor a pre-built vanilla kernel from Ubuntu's
   mainline builds. Fetch a pinned version, verify its checksums,
   extract the image and the modules, and run `depmod` at build time.
2. [x] `init/`: A minimal Go init. It mounts `/proc`, `/sys`, and
   `/dev`, prints a report of the hardware and the kernel state that it
   finds, and reaps zombie processes.
3. [x] `image/`: Assemble the initramfs, a cpio archive. The operating
   system is `vmlinuz` plus `liken.cpio`.
4. [x] `make run`: Boot it headless in QEMU. A smoke test can watch the
   serial output for a marker, and this is the start point for CI. Use
   explicit flags (`-display none -serial stdio -monitor none
   -no-reboot`) in place of the `-nographic` group, so that each flag
   can have its own comment.
