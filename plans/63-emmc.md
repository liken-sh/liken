# eMMC storage

A machine whose only disk is an eMMC module installs and boots the
same way a SATA or NVMe machine does.

This is a stub. The design is not written yet. The facts below came
from a real machine, and the code references were confirmed in the
tree.

## The problem

liken cannot see, install to, or boot from an eMMC disk. The gap has
three parts:

* **The report is blind to the controller.** `pciClassWord`
  (`hardware/names.go`) maps PCI base class `08` to the word
  `system`. An SD/eMMC host controller is class `0805`, inside base
  class `08`. `reportableClass` (`init/report.go`) accepts only
  `storage`, `network`, and `mass-storage`, and `claimableClass`
  drops `system` too. So an undriven eMMC controller gets no driver
  recommendation and no unclaimed listing. It is invisible in the
  proposal and in the warnings.
* **The boot archive has no eMMC driver.** `image/boot-modules.conf`
  carries `ahci` and `nvme` as the only real disk controllers, and
  boot-disk drivers cannot come from `spec.modules`, because the
  install claims disks long before declared modules load. The system
  image already ships the modules (`sdhci`, `sdhci-pci`,
  `sdhci-acpi`, `mmc_block`); the boot archive does not.
* **The by-id tree gives an mmc disk no identity name.**
  `init/diskids.go` builds `ata-` and `nvme-` names only, so a
  manifest could name an eMMC only by kernel path.

## The field case

`stick-1` at 44 Stony Point: an Apollo Lake HDMI stick PC, meant to
join a testbed cluster. Its hardware report proposed `storage: {}`
and said no disk can carry a role, while a 64GB eMMC sat in the
machine with a working Windows install on it.

The EFI shell on the install stick confirmed the hardware:

* Three class `0805` controllers, shown as "Base System Peripherals -
  SD Host controller": `8086:5ACA` (the SD card slot), `8086:5ACC`
  (the eMMC), and `8086:5AD0` (SDIO).
* `map -r` listed the eMMC's GPT through `8086:5ACC`: an EFI
  partition, a 16MB MSR, a ~61GB Windows partition, and a ~1GB
  recovery partition. The firmware reads the disk fine, so boot
  entries and slot mounts have nothing new to solve at that layer.
* The eMMC's two hardware boot areas appeared as their own block
  devices, which the kernel will also expose (`mmcblk0boot0`,
  `mmcblk0boot1`, and possibly `rpmb`).

## What the design must decide

* The class mapping: `0805` reads as storage, which means the word
  comes from the base class and the subclass together, not the base
  class alone.
* Which drivers go into the boot archive, and what they cost in
  staged RAM: `sdhci-pci` covers this machine, `sdhci-acpi` covers
  boards that enumerate the controller through ACPI, and both need
  `mmc_block`.
* The identity name for an mmc disk, built from the module's sysfs
  `name` and `serial`, the way `ata-` and `nvme-` names are built.
* The disk walk must skip the boot areas and RPMB, which are block
  devices but not disks a role may land on.
* Whether the SD card slot and SDIO controllers should propose
  anything. A card in a slot leaves with the person, like the
  install stick.
