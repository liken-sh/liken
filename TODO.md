# The rough path

1. [ ] Boot to a shell I built: QEMU, a kernel, a hand-rolled initramfs, a
       tiny init that mounts `/proc`, `/sys`, and `/dev` and gets to a prompt.
2. [ ] Init starts k3s and nothing else — and discover every host dependency
       (cgroups, kernel modules, time, entropy) the hard way.
3. [ ] Bake in Flux bootstrap, so the machine converges to its repo from
       first boot.
4. [ ] The mastery tier: A/B image upgrades, UKIs, dm-verity, secure boot,
       TPM-sealed secrets.
