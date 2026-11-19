# The FAT writer does not check the geometry it reads

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`NewFATWriter` in `disks/fat32_write.go` reads the geometry of the
volume from its boot sector and computes the number of clusters as
`(totalSectors - w.dataStart) / w.sectorsPerCluster`. All three values
are `uint32`. When the boot sector gives fewer total sectors than the
start of the data region, the subtraction wraps to a value near 2^32,
and `make([]uint32, clusters+2)` then asks for up to 16 GiB of memory.
When the boot sector gives 0 sectors for each cluster, the division
panics.

## The evidence

`NewFATWriter` checks the boot signature, the `FAT32   ` type string,
and the count of FATs, and nothing else. The only caller in the
component is `image/stick.go`, which calls it directly after
`disks.FormatFAT32` on the same section. So the bad values can come
only from a defect in `FormatFAT32` or from a later caller that opens
a volume that `liken` did not format.

## What would confirm it

A test that gives `NewFATWriter` a boot sector with the correct
signature and type string, and with total sectors smaller than the
reserved sectors and the FATs, or with 0 sectors for each cluster.

## What a fix has to decide

Whether `NewFATWriter` refuses a geometry that it cannot use, with an
error that gives the values, or whether its doc comment states that it
accepts only a volume that `FormatFAT32` wrote.
