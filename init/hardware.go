package main

// Hardware observation: the boot-time walk and the live watch that
// keep the unclaimed-device report correct.
//
// This approach comes from milestone 11: drivers are declared
// (spec.modules) and never auto-loaded, so a surprise device is an
// inert, reported fact. The kernel does everything else. A resident
// driver binds hot-plugged hardware without any userspace help. This
// leaves exactly one job here: notice undriven devices and report
// them, to the console and to the facts file, where the operator
// lifts them into the Machine's status. One watcher produces both
// outputs, and the same watcher will one day feed ResourceSlices too.

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/machine"
)

// The observation's inputs are variables, so tests can point them
// into fabricated trees. pciIDsPath is where the image stages
// hwdata's database. When this file is absent, devices show numeric
// names instead.
var (
	sysfsRoot  = "/sys"
	pciIDsPath = "/usr/share/hwdata/pci.ids"
)

// loadHardwareCatalog loads the lookup tables once per boot. A nil
// catalog (an image without the full alias table) disables the
// report rather than the boot. The machine still runs; it just
// cannot name the devices that it is not driving.
func loadHardwareCatalog() *hardware.Catalog {
	moduleDir := filepath.Join("/lib/modules", kernelRelease())
	catalog, err := hardware.LoadCatalog(moduleDir, pciIDsPath)
	if err != nil {
		fmt.Printf("liken: hardware: no unclaimed-device reporting: %v\n", err)
		return nil
	}
	return catalog
}

// discoverUnclaimed is the boot-time walk, nil-safe for images with
// no catalog.
func discoverUnclaimed(catalog *hardware.Catalog) []machine.UnclaimedDevice {
	if catalog == nil {
		return nil
	}
	return catalog.Discover(sysfsRoot)
}

// watchHardware is the machine-plane component that keeps the report
// current. It waits on the kernel's uevent socket, and when the
// hardware changes, it re-walks sysfs, reports the difference to the
// console, and republishes the facts. The uevent only signals that
// something changed; the walk re-reads the whole truth, so a missed
// or coalesced event costs nothing.
func watchHardware(catalog *hardware.Catalog, facts *factsFile, last []machine.UnclaimedDevice) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		uevents, err := hardware.ListenForUevents(ctx)
		if err != nil {
			return err
		}
		// The disk inventory refreshes on the same uevent signal,
		// because it has the same failure mode that the watch exists
		// to prevent: a boot-time snapshot goes stale the moment
		// hardware moves. This inventory can even race the boot. A
		// disk behind a just-loaded driver (a USB stick binding at
		// boot) can finish its SCSI probe after the facts were first
		// published, and the probe's own uevents bring the inventory
		// current moments later. The baseline comes from the published
		// facts rather than a fresh walk, so a disk that appeared
		// between the boot's walk and this one still reads as a change
		// worth publishing.
		var lastDisks []machine.BlockDevice
		facts.mutate(func(s *machine.MachineStatus) {
			lastDisks = slices.Clone(s.Hardware.BlockDevices)
		})
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-uevents:
			}
			// One plugged-in device produces a burst of uevents (the lab
			// measured eleven for one USB stick). This code waits for
			// the burst to finish rather than walking once per event.
			settle(ctx, uevents, time.Second, 5*time.Second)

			devices := hardware.DiscoverDevices(sysfsRoot, catalog.PCI)
			unclaimed := catalog.Unclaimed(devices)
			disks := discoverBlockDevices()
			for _, line := range hardwareTransitions(last, unclaimed, devices) {
				fmt.Println(line)
			}
			if !slices.EqualFunc(last, unclaimed, unclaimedEqual) || !slices.Equal(lastDisks, disks) {
				facts.publish(func(s *machine.MachineStatus) {
					s.Hardware.Unclaimed = unclaimed
					s.Hardware.BlockDevices = disks
				})
			}
			last, lastDisks = unclaimed, disks
		}
	}
}

// settle drains further uevent signals until quiet lasts a full
// interval, so a burst of arrivals becomes one walk, but only up to
// a ceiling. Waiting for true silence does not work on this machine.
// A node running Kubernetes emits uevents continuously while
// containers start and stop (every veth pair and overlay device
// announces itself). A settle that insists on quiet can block for
// minutes, which is exactly the staleness that the watch exists to
// prevent. The lab observed this blocking a hot-plugged disk's report
// for minutes because of an unrelated crash-looping pod. Walks are
// cheap and idempotent, so when the stream will not go quiet, walking
// anyway is the correct move. Anything that changes during the walk
// sends another uevent signal.
func settle(ctx context.Context, uevents <-chan struct{}, quiet, ceiling time.Duration) {
	deadline := time.NewTimer(ceiling)
	defer deadline.Stop()
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-uevents:
			timer.Reset(quiet)
		case <-timer.C:
			return
		case <-deadline.C:
			return
		}
	}
}

// hardwareTransitions describes what changed between two walks, in
// the same style as the rest of the boot's console report. An entry
// that appeared is a new gap. An entry that left either got its
// driver (this reports which driver) or was unplugged.
func hardwareTransitions(before, after []machine.UnclaimedDevice, devices []hardware.Device) []string {
	var lines []string
	for _, u := range after {
		if !slices.ContainsFunc(before, func(b machine.UnclaimedDevice) bool { return b.Modalias == u.Modalias }) {
			lines = append(lines, fmt.Sprintf("liken: hardware: unclaimed %s: %s", describeUnclaimed(u), u.Message))
		}
	}
	for _, u := range before {
		if slices.ContainsFunc(after, func(a machine.UnclaimedDevice) bool { return a.Modalias == u.Modalias }) {
			continue
		}
		driver := ""
		for _, d := range devices {
			if d.Modalias == u.Modalias && d.Driver != "" {
				driver = d.Driver
			}
		}
		if driver != "" {
			lines = append(lines, fmt.Sprintf("liken: hardware: %s is now driven by %s", nameOrModalias(u), driver))
		} else {
			lines = append(lines, fmt.Sprintf("liken: hardware: %s was removed", nameOrModalias(u)))
		}
	}
	return lines
}

// describeUnclaimed renders one entry for a console line: bus and
// class when known, then the best name available.
func describeUnclaimed(u machine.UnclaimedDevice) string {
	description := u.Bus
	if u.Class != "" {
		description += " " + u.Class
	}
	return description + " device " + nameOrModalias(u)
}

func nameOrModalias(u machine.UnclaimedDevice) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Modalias
}

func unclaimedEqual(a, b machine.UnclaimedDevice) bool {
	return a.Modalias == b.Modalias && a.Bus == b.Bus && a.Name == b.Name &&
		a.Class == b.Class && a.Message == b.Message &&
		slices.Equal(a.Candidates, b.Candidates)
}
