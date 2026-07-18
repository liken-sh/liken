package main

// Hardware observation: the boot-time walk and the live watch that
// keep the unclaimed-device report true.
//
// The posture comes from milestone 11: drivers are declared
// (spec.modules), never auto-loaded, so a surprise device is an
// inert, reported fact. The kernel does everything else — a
// resident driver binds hot-plugged hardware without any userspace
// help — which leaves exactly one job here: notice undriven
// devices and report them, to the console and to the facts file,
// where the operator lifts them into the Machine's status. One
// watcher, two outputs, and the same watcher will one day feed
// ResourceSlices too.

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/machine"
)

// The observation's inputs, variables so tests can point them into
// fabricated trees. pciIDsPath is where the image stages hwdata's
// database; its absence just means numeric names.
var (
	sysfsRoot  = "/sys"
	pciIDsPath = "/usr/share/hwdata/pci.ids"
)

// loadHardwareCatalog loads the judgment tables once per boot. A nil
// catalog (an image without the full alias table) disables the
// report rather than the boot: the machine still runs, it just
// can't name what it isn't driving.
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

// watchHardware is the machine-plane component that keeps the
// report live: sleep on the kernel's uevent socket, and when the
// hardware changes, re-walk sysfs, narrate the difference to the
// console, and republish the facts. The uevent is only a doorbell —
// the walk re-reads the whole truth — so a missed or coalesced
// event costs nothing.
func watchHardware(catalog *hardware.Catalog, facts *factsFile, last []machine.UnclaimedDevice) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		uevents, err := hardware.ListenForUevents(ctx)
		if err != nil {
			return err
		}
		// The disk inventory refreshes on the same doorbell, because
		// it has the same failure mode the watch exists to prevent: a
		// boot-time snapshot goes stale the moment hardware moves. It
		// even races the boot — a disk behind a just-loaded driver
		// (a USB stick binding at boot) can finish its SCSI probe
		// after the facts were first published, and the probe's own
		// uevents are what bring the inventory current moments later.
		// The baseline comes from the published facts rather than a
		// fresh walk, so a disk that appeared between the boot's walk
		// and this one still reads as a change worth publishing.
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
			// One plugged-in device is a cascade of uevents (the lab
			// measured eleven for one USB stick); wait for the burst
			// to finish rather than walking once per event.
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

// settle drains further doorbell rings until quiet lasts a full
// interval, so a burst of arrivals becomes one walk — but only up
// to a ceiling. Waiting for true silence is a trap on this machine:
// a node running Kubernetes emits uevents continuously while
// containers churn (every veth pair and overlay device announces
// itself), and a settle that insists on quiet can be held captive
// for minutes, which is exactly the staleness the watch exists to
// prevent — the lab caught it holding a hot-plugged disk's report
// hostage to an unrelated crash-looping pod. Walks are cheap and
// idempotent, so when the stream won't quiet, walking anyway is
// the correct move; anything that changes mid-walk rings the
// doorbell again.
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

// hardwareTransitions narrates what changed between two walks, in
// the same voice as the rest of the boot's console report. An entry
// that appeared is a new gap; an entry that left either got its
// driver (the happy line — say who) or was unplugged.
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
// class when known, then the best name there is.
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
