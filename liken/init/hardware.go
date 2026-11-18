package main

// Init walks the devices at boot, and then watches uevents, to report
// each device that has no driver.
//
// Init loads only the drivers that spec.modules declares. A device
// whose driver is not declared stays unbound, and init reports it
// on the console and in the facts tree, where the operator copies it
// into the Machine's status. The kernel binds a device to a driver
// that is already loaded, including a device that is plugged in later,
// so init does not need to bind anything.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/machine"
)

// sysfsRoot and pciIDsPath are variables so that tests can point them
// at a fake tree. Without the hwdata PCI ID database at pciIDsPath, a
// device shows its numeric IDs and no name.
var (
	sysfsRoot  = "/sys"
	pciIDsPath = "/usr/share/hwdata/pci.ids"
)

// loadHardwareCatalog loads the module alias table and the PCI ID
// database once per boot. When the load fails, it returns nil. The
// machine still boots, but it does not report unclaimed devices.
func loadHardwareCatalog() *hardware.Catalog {
	moduleDir := filepath.Join("/lib/modules", kernelRelease())
	catalog, err := hardware.LoadCatalog(moduleDir, pciIDsPath)
	if err != nil {
		fmt.Printf("liken: hardware: unclaimed devices will not be reported: loading the hardware catalog: %v\n", err)
		return nil
	}
	return catalog
}

// discoverUnclaimed walks sysfs once and returns the devices that no
// driver is bound to. It returns nil when catalog is nil. A serial-line
// adapter counts as unclaimed until a spec.serio entry declares it
// (hardware/unclaimedserio.go).
func discoverUnclaimed(catalog *hardware.Catalog) []machine.UnclaimedDevice {
	if catalog == nil {
		return nil
	}
	return catalog.Discover(sysfsRoot, serioAttachments.declaredEntries())
}

// watchHardware returns the machine-plane component that keeps the
// unclaimed-device list and the disk list current. After each uevent
// it walks sysfs again, prints each change to the console, and writes
// both lists to the facts tree when either one changed. The walk reads
// the whole state, so a missed or merged uevent loses nothing. After
// the boot, this watch is the only writer of hardware/blockDevices/
// and hardware/unclaimed/.
//
// last and lastDisks are the lists that the boot published. The first
// walk compares with them, so it publishes a disk whose SCSI probe
// finished after the boot read the disks. catalog must not be nil.
func watchHardware(catalog *hardware.Catalog, tree machine.FactsTree, last []machine.UnclaimedDevice, lastDisks []machine.BlockDevice) func(ctx context.Context) error {
	walk := func() {
		devices := hardware.DiscoverDevices(sysfsRoot, catalog.PCI)
		unclaimed := catalog.Unclaimed(devices, serioAttachments.declaredEntries())
		disks := discoverBlockDevices()
		for _, line := range hardwareTransitions(last, unclaimed, devices) {
			fmt.Println(line)
		}
		if !slices.EqualFunc(last, unclaimed, unclaimedEqual) || !slices.EqualFunc(lastDisks, disks, blockDeviceEqual) {
			tree.WriteUnclaimed(unclaimed)
			tree.WriteBlockDevices(disks)
		}
		last, lastDisks = unclaimed, disks
	}
	return func(ctx context.Context) error {
		// Open the listener before the first walk, so a device that
		// changes during the walk still sends a uevent that the loop
		// receives.
		uevents, err := listenForUevents(ctx)
		if err != nil {
			return err
		}
		for {
			walk()
			select {
			case <-ctx.Done():
				return nil
			case _, ok := <-uevents:
				if !ok {
					return errUeventsStopped
				}
			}
			// Plugging in one device sends a burst of uevents. Wait for
			// the burst to settle, so that one device causes one walk.
			hardware.Settle(ctx, uevents, time.Second, 5*time.Second)
		}
	}
}

// listenForUevents is a variable so that a test can supply a listener
// that it stops or wakes.
var listenForUevents = hardware.ListenForUevents

// errUeventsStopped is what a component returns when its uevent
// listener stops. The machine plane starts the component again, and
// the new run walks once before it waits, so it reports a change made
// while no listener was open.
var errUeventsStopped = errors.New("the uevent listener stopped")

// hardwareTransitions returns one console line for each device that
// entered or left the unclaimed list between two walks. A device that
// left either got a driver, which the line names, or was unplugged.
func hardwareTransitions(before, after []machine.UnclaimedDevice, devices []hardware.Device) []string {
	base := moduleBase()
	var lines []string
	for _, u := range after {
		if !slices.ContainsFunc(before, func(b machine.UnclaimedDevice) bool { return b.Modalias == u.Modalias }) {
			lines = append(lines, fmt.Sprintf("liken: hardware: unclaimed %s: %s", describeUnclaimed(u), unclaimedAdvice(base, u)))
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
		// An unclaimed serial-line adapter is already bound to its
		// driver and has no candidate drivers. So when such an entry
		// leaves the list, a spec.serio entry declared it.
		if driver != "" && len(u.Candidates) == 0 {
			lines = append(lines, fmt.Sprintf("liken: hardware: %s is now declared in spec.serio", nameOrModalias(u)))
		} else if driver != "" {
			lines = append(lines, fmt.Sprintf("liken: hardware: %s is now driven by %s", nameOrModalias(u), driver))
		} else {
			lines = append(lines, fmt.Sprintf("liken: hardware: %s was removed", nameOrModalias(u)))
		}
	}
	return lines
}

func describeUnclaimed(u machine.UnclaimedDevice) string {
	description := u.Bus
	if u.Class != "" {
		description += " " + u.Class
	}
	return description + " device " + nameOrModalias(u)
}

// softdepBase is the module tree that unclaimedAdvice reads soft
// dependencies from. When it is empty, the tree of the running kernel
// is used. Tests set it to a fixture tree.
var softdepBase = ""

func moduleBase() string {
	if softdepBase != "" {
		return softdepBase
	}
	return filepath.Join("/lib/modules", kernelRelease())
}

// unclaimedAdvice returns the console advice for one unclaimed device.
// A candidate driver can have a soft dependency, a module that must
// load before it, such as realtek before r8169. modules.dep does not
// record soft dependencies, so a person who declares only r8169 gets a
// driver that does not work. So the advice lists each candidate with
// its soft dependencies in load order: "declare realtek, then r8169 in
// spec.modules". When no candidate has a soft dependency, including a
// device with no candidates, it returns the catalog's message.
func unclaimedAdvice(base string, u machine.UnclaimedDevice) string {
	expanded := false
	choices := make([]string, 0, len(u.Candidates))
	for _, candidate := range u.Candidates {
		chain := softdepChain(base, candidate)
		if len(chain) == 1 {
			choices = append(choices, candidate)
			continue
		}
		expanded = true
		choices = append(choices, strings.Join(chain[:len(chain)-1], ", ")+", then "+chain[len(chain)-1])
	}
	if !expanded {
		return u.Message
	}
	return "declare " + strings.Join(choices, " or ") + " in spec.modules"
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

// blockDeviceEqual compares every field of two disks. BlockDevice has
// a slice field, so == does not compile for it.
func blockDeviceEqual(a, b machine.BlockDevice) bool {
	return a.Name == b.Name && a.SizeBytes == b.SizeBytes &&
		a.Model == b.Model && a.Serial == b.Serial &&
		slices.Equal(a.StableNames, b.StableNames)
}
