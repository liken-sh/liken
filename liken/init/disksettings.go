package main

// init applies spec.disks and liken's default for the clusterState disk
// at boot, after storage is mounted and before k3s starts, so etcd's
// first write already goes to a disk with the smaller request size
// (machine/disksettings.go explains the default). machine-operator
// applies the same settings again on every pass, and reports each entry
// that it cannot apply in the DiskSettingsApplied condition. init only
// prints them, because no API server runs yet.

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/liken/machine"
)

// applyRequestSizes writes max_sectors_kb on each disk that needs it.
// Later boots find clusterState by its partition name, so the default
// goes from the partition that storage mounted to the disk that holds
// it.
func applyRequestSizes(entries []machine.DiskSettings, storage machine.StorageStatus) {
	disks := discoverBlockDevices()
	clusterStateDisk := ""
	if storage.ClusterState.Backing == machine.BackingPartition {
		clusterStateDisk = machine.DiskOfPartition(sysBlock, storage.ClusterState.Device, disks)
	}
	targets, problems := machine.PlanRequestSizes(entries, disks, clusterStateDisk)
	for _, p := range problems {
		fmt.Fprintf(os.Stderr, "liken: disks: %s\n", p.Message)
	}
	for _, target := range targets {
		// No program has written a request size since the kernel found
		// the disk, so a disk that keeps the kernel's value needs no
		// write at boot.
		if target.KiB == 0 {
			continue
		}
		current, _, err := machine.ReadRequestLimits(sysBlock, target.Disk)
		if err == nil && current == target.KiB {
			continue
		}
		if err := machine.WriteMaxRequestSize(sysBlock, target.Disk, target.KiB); err != nil {
			fmt.Fprintf(os.Stderr, "liken: disks: %s: %v\n", target.Source, err)
			continue
		}
		fmt.Printf("liken: disks: %s takes requests of at most %s, from %s\n", target.Disk, machine.RequestSizeText(target.KiB), target.Source)
	}
}
