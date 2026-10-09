package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/machine"
)

// stick is the recurring unclaimed device in these tests: the lab's
// QEMU USB stick, waiting for the usb_storage driver.
var stick = machine.UnclaimedDevice{
	Modalias:   "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
	Bus:        "usb",
	Name:       "QEMU QEMU USB HARDDRIVE",
	Class:      "mass-storage",
	Candidates: []string{"usb_storage", "uas"},
	Message:    "declare usb_storage or uas in spec.modules",
}

// fixtureModuleTree points the soft-dependency reader at a tree of the
// test's making and restores the real tree when the test ends. Without
// it, the advice would read the host's own module tree, and a test's
// expectation would depend on the machine that runs it.
func fixtureModuleTree(t *testing.T, base string) {
	t.Helper()
	old := softdepBase
	softdepBase = base
	t.Cleanup(func() { softdepBase = old })
}

func TestTransitionsNarrateANewGap(t *testing.T) {
	fixtureModuleTree(t, t.TempDir())
	lines := hardwareTransitions(nil, []machine.UnclaimedDevice{stick}, nil)
	want := "liken: hardware: unclaimed usb mass-storage device QEMU QEMU USB HARDDRIVE: declare usb_storage or uas in spec.modules"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q, want [%q]", lines, want)
	}
}

// nic is an unclaimed Realtek NIC: the case that needs realtek loaded
// before r8169, a soft dependency modules.dep does not record.
var nic = machine.UnclaimedDevice{
	Modalias:   "pci:v000010ECd00008168sv00sd00bc02sc00i00",
	Bus:        "pci",
	Name:       "RTL8111/8168/8411 PCI Express Gigabit Ethernet Controller",
	Class:      "ethernet",
	Candidates: []string{"r8169"},
	Message:    "declare r8169 in spec.modules",
}

func TestTransitionsNameTheSoftdepChain(t *testing.T) {
	fixtureModuleTree(t, softdepTree(t, map[string][]byte{
		"kernel/drivers/net/ethernet/realtek/r8169.ko": modinfoBytes("softdep=pre: realtek"),
		"kernel/drivers/net/phy/realtek.ko":            modinfoBytes("license=GPL"),
	}))
	lines := hardwareTransitions(nil, []machine.UnclaimedDevice{nic}, nil)
	want := "liken: hardware: unclaimed pci ethernet device RTL8111/8168/8411 PCI Express Gigabit Ethernet Controller: declare realtek, then r8169 in spec.modules"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q, want [%q]", lines, want)
	}
}

func TestTransitionsNarrateAClaim(t *testing.T) {
	devices := []hardware.Device{{Bus: "usb", Modalias: stick.Modalias, Driver: "usb-storage"}}
	lines := hardwareTransitions([]machine.UnclaimedDevice{stick}, nil, devices)
	want := "liken: hardware: QEMU QEMU USB HARDDRIVE is now driven by usb-storage"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q, want [%q]", lines, want)
	}
}

func TestTransitionsNarrateARemoval(t *testing.T) {
	lines := hardwareTransitions([]machine.UnclaimedDevice{stick}, nil, nil)
	want := "liken: hardware: QEMU QEMU USB HARDDRIVE was removed"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q, want [%q]", lines, want)
	}
}

// noisyChannel sends signals continuously, faster than any quiet
// interval, until the test ends. This is the pattern of a node whose
// containers are starting and stopping constantly.
func noisyChannel(t *testing.T) chan struct{} {
	t.Helper()
	ch := make(chan struct{}, 1)
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		for {
			select {
			case <-t.Context().Done():
				return
			case ch <- struct{}{}:
			default:
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return ch
}

func TestSettleReturnsAtTheCeilingUnderConstantNoise(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		settle(t.Context(), noisyChannel(t), time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != 5*time.Second {
			t.Errorf("settle returned after %s, want the 5s ceiling", elapsed)
		}
	})
}

func TestSettleReturnsAtOnceWhenTheListenerStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ch := make(chan struct{})
		close(ch)
		started := time.Now()
		settle(t.Context(), ch, time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != 0 {
			t.Errorf("settle returned after %s, want at once for a closed channel", elapsed)
		}
	})
}

func TestSettleReturnsAtQuietWhenTheStreamStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ch := make(chan struct{}, 1)
		started := time.Now()
		settle(t.Context(), ch, time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != time.Second {
			t.Errorf("settle returned after %s, want the 1s quiet interval", elapsed)
		}
	})
}

func TestTransitionsAreQuietWhenNothingChanged(t *testing.T) {
	lines := hardwareTransitions([]machine.UnclaimedDevice{stick}, []machine.UnclaimedDevice{stick}, nil)
	if lines != nil {
		t.Errorf("lines = %q, want none", lines)
	}
}

func TestListenerStoppedTellsAClosedChannelFromAnOpenOne(t *testing.T) {
	closed := make(chan struct{})
	close(closed)
	pending := make(chan struct{}, 1)
	pending <- struct{}{}
	cases := []struct {
		name    string
		uevents <-chan struct{}
		want    bool
	}{
		{"a closed channel", closed, true},
		{"an open channel with a wake pending", pending, false},
		{"an open, quiet channel", make(chan struct{}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := listenerStopped(c.uevents); got != c.want {
				t.Errorf("listenerStopped = %v, want %v", got, c.want)
			}
		})
	}
}

// useListener stands listen in for the kernel's uevent socket for the
// rest of the test, and restores the real socket when the test ends.
func useListener(t *testing.T, listen func(context.Context) (<-chan struct{}, error)) {
	t.Helper()
	saved := listenForUevents
	t.Cleanup(func() { listenForUevents = saved })
	listenForUevents = listen
}

// closedListener is a uevent listener that has already stopped: its
// channel is closed.
func closedListener(context.Context) (<-chan struct{}, error) {
	stopped := make(chan struct{})
	close(stopped)
	return stopped, nil
}

// stoppedListener makes every component's uevent listener one that has
// already stopped.
func stoppedListener(t *testing.T) {
	t.Helper()
	useListener(t, closedListener)
}

// The hardware watch walks sysfs as soon as its listener opens, before
// it waits, so a disk that arrived while a stopped listener was being
// replaced still reaches the facts. Then it returns errUeventsStopped,
// and the machine plane starts it again.
func TestTheHardwareWatchWalksBeforeItWaitsAndEndsWhenItsListenerStops(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 8<<30, nil)
	saved := sysfsRoot
	t.Cleanup(func() { sysfsRoot = saved })
	sysfsRoot = t.TempDir()
	stoppedListener(t)
	tree := machine.FactsTree{Dir: t.TempDir()}

	err := watchHardware(&hardware.Catalog{}, tree, nil, nil)(t.Context())

	facts, _ := tree.Read()
	if !errors.Is(err, errUeventsStopped) || facts == nil || len(facts.Hardware.BlockDevices) != 1 {
		t.Errorf("the watch answered %v and published %+v; want errUeventsStopped after it published vda", err, facts)
	}
}

// Each of init's other uevent components ends with errUeventsStopped
// when its listener stops, so the machine plane starts it again with a
// new listener, instead of spinning on the closed channel.
func TestTheUeventComponentsEndWhenTheirListenerStops(t *testing.T) {
	cases := []struct {
		name      string
		component func(t *testing.T) func(context.Context) error
	}{
		{"the disk links", func(*testing.T) func(context.Context) error { return watchDiskLinks }},
		{"the serial lines", func(t *testing.T) func(context.Context) error {
			return watchSerio(newSerioRegistry(newFakeTTYs(t).open), machine.FactsTree{Dir: t.TempDir()})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeMachine(t)
			stoppedListener(t)
			if err := c.component(t)(t.Context()); !errors.Is(err, errUeventsStopped) {
				t.Errorf("the component answered %v, want errUeventsStopped", err)
			}
		})
	}
}

// errNoSocket stands in for a uevent socket that the kernel refused.
var errNoSocket = errors.New("no uevent socket")

// failedListener is a uevent listener whose socket did not open.
func failedListener(context.Context) (<-chan struct{}, error) {
	return nil, errNoSocket
}

// The hardware watch returns the listener's error, so the machine plane
// reports the fault and starts the watch again.
func TestTheHardwareWatchEndsWhenItsListenerCannotOpen(t *testing.T) {
	useListener(t, failedListener)

	err := watchHardware(&hardware.Catalog{}, machine.FactsTree{Dir: t.TempDir()}, nil, nil)(t.Context())

	if !errors.Is(err, errNoSocket) {
		t.Errorf("the watch answered %v, want the listener's error", err)
	}
}

// A uevent makes the hardware watch walk sysfs again once the burst
// settles, and republish the disk inventory when a disk changed. A
// virtual disk that grew keeps its name, so only a comparison of every
// field finds the change. The unclaimed NIC is the same in both walks,
// and the watch still publishes the disk. When the context ends, the
// watch returns nil.
func TestTheHardwareWatchRepublishesADiskThatChangedAfterAUevent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "vda", 8<<30, nil)
		root := fakeBus(t)
		addPCIDevice(t, root, "0000:00:1f.6", "0x020000", nicModalias)
		catalog, base := fixtureCatalog(t, map[string]string{nicModalias: "r8169"})
		fixtureModuleTree(t, base)
		wakes := make(chan struct{}, 1)
		useListener(t, func(context.Context) (<-chan struct{}, error) { return wakes, nil })
		tree := machine.FactsTree{Dir: t.TempDir()}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- watchHardware(catalog, tree, nil, nil)(ctx) }()
		synctest.Wait()

		writeSysfs(t, filepath.Join(sys, "vda"), "size", "33554432\n")
		wakes <- struct{}{}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		facts, _ := tree.Read()
		cancel()

		if err := <-done; err != nil {
			t.Errorf("the watch answered %v after its context ended, want nil", err)
		}
		if facts == nil || len(facts.Hardware.BlockDevices) != 1 || facts.Hardware.BlockDevices[0].SizeBytes != 16<<30 {
			t.Errorf("the watch published %+v, want vda at 16 GiB", facts)
		}
	})
}

// The hardware watch republishes the disk inventory when a disk
// changed in any field, including a disk that kept its name.
func TestBlockDeviceEqualComparesEveryField(t *testing.T) {
	disk := machine.BlockDevice{Name: "vda", SizeBytes: 8 << 30, Model: "QEMU HARDDISK", Serial: "lab", StableNames: []string{"virtio-lab"}}
	cases := []struct {
		name  string
		other machine.BlockDevice
		want  bool
	}{
		{"the same disk", disk, true},
		{"a disk that grew", machine.BlockDevice{Name: "vda", SizeBytes: 16 << 30, Model: "QEMU HARDDISK", Serial: "lab", StableNames: []string{"virtio-lab"}}, false},
		{"a disk with no stable name", machine.BlockDevice{Name: "vda", SizeBytes: 8 << 30, Model: "QEMU HARDDISK", Serial: "lab"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := blockDeviceEqual(disk, c.other); got != c.want {
				t.Errorf("blockDeviceEqual = %v, want %v", got, c.want)
			}
		})
	}
}

// The hardware watch republishes the unclaimed list when an entry
// changed in any field, including its candidate drivers.
func TestUnclaimedEqualComparesEveryField(t *testing.T) {
	withOneCandidate := stick
	withOneCandidate.Candidates = []string{"usb_storage"}
	cases := []struct {
		name  string
		other machine.UnclaimedDevice
		want  bool
	}{
		{"the same device", stick, true},
		{"a device with other candidates", withOneCandidate, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unclaimedEqual(stick, c.other); got != c.want {
				t.Errorf("unclaimedEqual = %v, want %v", got, c.want)
			}
		})
	}
}

// settle returns when its context ends, even while the stream is still
// noisy, so a component that is shutting down does not wait out the
// ceiling.
func TestSettleReturnsWhenTheContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		started := time.Now()
		settle(ctx, noisyChannel(t), time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != 2*time.Second {
			t.Errorf("settle returned after %s, want 2s, when the context ended", elapsed)
		}
	})
}

// An entry that leaves the unclaimed list is narrated by what made it
// leave. A serial-line adapter already had its driver, so it leaves
// when a spec.serio entry declares it. A device the catalog could not
// name is narrated by its modalias.
func TestTransitionsNarrateAnEntryThatLeft(t *testing.T) {
	adapter := machine.UnclaimedDevice{Modalias: "usb:v2548p1002", Bus: "usb", Name: "Pulse-Eight CEC adapter"}
	unnamed := machine.UnclaimedDevice{Modalias: "pci:v00001234d00005678", Bus: "pci", Candidates: []string{"mystery"}}
	cases := []struct {
		name    string
		before  machine.UnclaimedDevice
		devices []hardware.Device
		want    string
	}{
		{"a declared serial-line adapter", adapter, []hardware.Device{{Bus: "usb", Modalias: adapter.Modalias, Driver: "cdc_acm"}}, "liken: hardware: Pulse-Eight CEC adapter is now declared in spec.serio"},
		{"an unnamed device that was removed", unnamed, nil, "liken: hardware: pci:v00001234d00005678 was removed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines := hardwareTransitions([]machine.UnclaimedDevice{c.before}, nil, c.devices)
			if len(lines) != 1 || lines[0] != c.want {
				t.Errorf("lines = %q, want [%q]", lines, c.want)
			}
		})
	}
}

// modaliases lists the fingerprint of each unclaimed entry, in order.
func modaliases(unclaimed []machine.UnclaimedDevice) []string {
	var fingerprints []string
	for _, u := range unclaimed {
		fingerprints = append(fingerprints, u.Modalias)
	}
	return fingerprints
}

// The boot-time walk reports nothing on an image with no catalog, and
// otherwise reports each device that no driver claims.
func TestDiscoverUnclaimedNeedsACatalog(t *testing.T) {
	root := fakeBus(t)
	addPCIDevice(t, root, "0000:00:1f.6", "0x020000", nicModalias)
	catalog, _ := fixtureCatalog(t, map[string]string{nicModalias: "r8169"})
	cases := []struct {
		name    string
		catalog *hardware.Catalog
		want    []string
	}{
		{"no catalog", nil, nil},
		{"a catalog", catalog, []string{nicModalias}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := modaliases(discoverUnclaimed(c.catalog)); !slices.Equal(got, c.want) {
				t.Errorf("discoverUnclaimed found %q, want %q", got, c.want)
			}
		})
	}
}
