package main

// Tests for the report boot's own steps: the wait for the probe, the
// look at each network port, the gathering of the whole report, and the
// messages a person reads at the end. The steps that act on the
// machine (the loop mount, raising a link, the stick's mount, and the
// reboot) need root, so each test stands in for the one it reaches.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// The report boot runs only when the command line carries
// liken.report, because it is the one menu entry that never touches a
// disk.
func TestReportingReadsTheCommandLine(t *testing.T) {
	cases := []struct {
		name    string
		cmdline string
		want    bool
	}{
		{"the report entry", "console=ttyS0 liken.report\n", true},
		{"an ordinary boot", "console=ttyS0 liken.machine=node-3\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeCmdline(t, c.cmdline)
			if got := reporting(); got != c.want {
				t.Errorf("reporting() = %v, want %v", got, c.want)
			}
		})
	}
}

// quietListener is a uevent listener whose channel stays open and never
// signals: a machine whose probe has already finished.
func quietListener(context.Context) (<-chan struct{}, error) {
	return make(chan struct{}), nil
}

// The report waits for the bus probe to go quiet before it walks sysfs.
// With a live listener it waits for one quiet second. When it cannot
// hear the probe, because the socket did not open or the listener
// stopped, it pauses for quiescePause, so a probe still has time to
// finish.
func TestQuiesceHardwareWaitsForTheProbe(t *testing.T) {
	cases := []struct {
		name   string
		listen func(context.Context) (<-chan struct{}, error)
		want   time.Duration
	}{
		{"a quiet listener", quietListener, time.Second},
		{"no uevent socket", failedListener, quiescePause},
		{"a listener that stops", closedListener, quiescePause},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				useListener(t, c.listen)
				started := time.Now()
				quiesceHardware()
				if elapsed := time.Since(started); elapsed != c.want {
					t.Errorf("the report waited %s, want %s", elapsed, c.want)
				}
			})
		})
	}
}

// fakeNetlink stands in for the kernel's list of interfaces. It answers
// the list with links, or with listErr, and records the name of each
// link the report raises. A raise of a name in refuse fails.
type fakeNetlink struct {
	links   []netlink.Link
	listErr error
	refuse  string
	raised  []string
}

// useLinks hands the report the fake's interfaces in place of the
// machine's own for the rest of the test.
func useLinks(t *testing.T, f *fakeNetlink) {
	t.Helper()
	savedList, savedRaise := listLinks, raiseLink
	t.Cleanup(func() { listLinks, raiseLink = savedList, savedRaise })
	listLinks = func() ([]netlink.Link, error) { return f.links, f.listErr }
	raiseLink = func(link netlink.Link) error {
		f.raised = append(f.raised, link.Attrs().Name)
		if link.Attrs().Name == f.refuse {
			return errors.New("operation not permitted")
		}
		return nil
	}
}

// port builds a hardware network interface with a MAC address.
func port(t *testing.T, name, mac string) netlink.Link {
	t.Helper()
	addr, err := net.ParseMAC(mac)
	if err != nil {
		t.Fatal(err)
	}
	return &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: name, HardwareAddr: addr}}
}

// loopback is the kernel's loopback interface. It carries an all-zero
// MAC address, so only its flag tells it apart from hardware.
var loopback = &netlink.Device{LinkAttrs: netlink.LinkAttrs{
	Name: "lo", Flags: net.FlagLoopback, HardwareAddr: make(net.HardwareAddr, 6),
}}

// tunnel is a virtual interface with no MAC address.
var tunnel = &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: "wg0"}}

// operstate writes the kernel's link state for one interface into the
// fake sysfs tree.
func operstate(t *testing.T, root, name, state string) {
	t.Helper()
	dir := filepath.Join(root, "class", "net", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, dir, "operstate", state+"\n")
}

// The report raises every hardware port and waits three seconds for
// copper autonegotiation before it reads the carrier, because a carrier
// read before the link trains reports every port dark. Loopback and a
// virtual interface are not hardware, so the report leaves them alone.
// A port that refuses to come up is still reported, and a port whose
// state the kernel does not publish reads as unknown.
func TestObserveInterfacesRaisesAndReadsEveryHardwarePort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := fakeBus(t)
		operstate(t, root, "eth0", "up")
		links := &fakeNetlink{
			links:  []netlink.Link{loopback, port(t, "eth0", "52:54:00:12:34:56"), tunnel, port(t, "eth1", "52:54:00:12:34:57")},
			refuse: "eth1",
		}
		useLinks(t, links)

		started := time.Now()
		got := observeInterfaces()
		elapsed := time.Since(started)

		want := []reportInterface{
			{Name: "eth0", MAC: "52:54:00:12:34:56", Link: "up"},
			{Name: "eth1", MAC: "52:54:00:12:34:57", Link: "unknown"},
		}
		if !slices.Equal(got, want) {
			t.Errorf("the report observed %+v, want %+v", got, want)
		}
		if !slices.Equal(links.raised, []string{"eth0", "eth1"}) {
			t.Errorf("the report raised %q, want eth0 and eth1", links.raised)
		}
		if elapsed != 3*time.Second {
			t.Errorf("the report waited %s for the links to train, want 3s", elapsed)
		}
	})
}

// The report waits for links to train only when it raised one. A
// machine with no hardware port, or a list the kernel refused, reports
// no interface and costs the boot no wait.
func TestObserveInterfacesWaitsOnlyForARaisedPort(t *testing.T) {
	cases := []struct {
		name  string
		links *fakeNetlink
	}{
		{"no hardware port", &fakeNetlink{links: []netlink.Link{loopback, tunnel}}},
		{"a refused list", &fakeNetlink{listErr: errors.New("netlink: permission denied")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fakeBus(t)
				useLinks(t, c.links)
				started := time.Now()
				got := observeInterfaces()
				if elapsed := time.Since(started); got != nil || elapsed != 0 {
					t.Errorf("the report observed %+v after %s, want nothing at once", got, elapsed)
				}
			})
		})
	}
}

// useImageMount stands mount in for the loop mount of the payload's
// system image, and points the report's mount point at a temporary
// directory, for the rest of the test. It returns that directory.
func useImageMount(t *testing.T, mount func(image, target string) error) string {
	t.Helper()
	savedMount, savedTarget := mountReportImage, reportImageMount
	t.Cleanup(func() { mountReportImage, reportImageMount = savedMount, savedTarget })
	mountReportImage = mount
	reportImageMount = t.TempDir()
	return reportImageMount
}

// reportMachine builds the machine every gather test reports on: UEFI
// firmware, one 500 GiB disk, the installation stick, one cabled
// port, and a display controller that a workload could claim. The
// probe is already quiet. It returns the fake sysfs root and /dev.
func reportMachine(t *testing.T) (root, dev string) {
	t.Helper()
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "model", "REAL DISK\n")
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", stickInstallPartition, 1<<30)
	root = fakeBus(t)
	addPCIDevice(t, root, "0000:00:02.0", "0x030000", displayModalias)
	operstate(t, root, "eth0", "up")
	useLinks(t, &fakeNetlink{links: []netlink.Link{loopback, port(t, "eth0", "52:54:00:12:34:56")}})
	useListener(t, quietListener)
	savedEFI := efiSysDir
	t.Cleanup(func() { efiSysDir = savedEFI })
	efiSysDir = t.TempDir()
	return root, dev
}

// reportSummary renders the facts of a report that the gather tests
// check, one field per word, so a test compares one string.
func reportSummary(r hardwareReport) string {
	var disks, interfaces, claimable []string
	for _, d := range r.Disks {
		disks = append(disks, d.Name)
	}
	for _, i := range r.Interfaces {
		interfaces = append(interfaces, i.Name+":"+i.Link)
	}
	for _, c := range r.Claimable {
		claimable = append(claimable, c.Class)
	}
	return fmt.Sprintf("uefi=%v stick=%s disks=%s interfaces=%s claimable=%s",
		r.UEFI, filepath.Base(r.StickPath), strings.Join(disks, ","),
		strings.Join(interfaces, ","), strings.Join(claimable, ","))
}

// failedMount is a loop mount that the kernel refused.
func failedMount(image, target string) error {
	return fmt.Errorf("attaching %s: operation not permitted", image)
}

// payloadMount is a loop mount that succeeds onto a module tree the
// test wrote at the mount point: an alias table that names a driver
// for the display controller.
func payloadMount(image, target string) error {
	tree := filepath.Join(target, "lib", "modules", kernelRelease())
	if err := os.MkdirAll(tree, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(tree, "modules.alias"), []byte("alias "+displayModalias+" bochs\n"), 0o644)
}

// The report describes the disks and ports a machine has, with the
// stick left out of the disks, whether or not the payload's system
// image mounts. The module tree in that image is what names the drivers
// for undriven hardware, so only a report whose image mounted lists the
// display controller as hardware a workload could claim. A machine
// whose payload does not mount still gets a report of what the boot
// path's drivers bound.
func TestGatherHardwareReportDescribesTheMachine(t *testing.T) {
	cases := []struct {
		name  string
		mount func(image, target string) error
		want  string
	}{
		{
			"a payload that will not mount",
			failedMount,
			"uefi=true stick=sdb disks=sda interfaces=eth0:up claimable=",
		},
		{
			"a payload that mounts",
			payloadMount,
			"uefi=true stick=sdb disks=sda interfaces=eth0:up claimable=display",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				reportMachine(t)
				useImageMount(t, c.mount)

				report, stick := gatherHardwareReport()

				if got := reportSummary(report); got != c.want {
					t.Errorf("the report read %q, want %q", got, c.want)
				}
				if stick.Disk != "sdb" {
					t.Errorf("the report resolved the stick %+v, want sdb", stick)
				}
			})
		})
	}
}

// fakeStickMount stands in for the stick's FAT volume. The mount point
// is a temporary directory, a mount answers mountErr, and an unmount
// with no flags answers busyErr, the way a volume that something still
// holds refuses a plain unmount. It records the flags of each unmount.
type fakeStickMount struct {
	dir      string
	mountErr error
	busyErr  error
	unmounts []int
}

// useStickMount hands the report the fake volume in place of the stick's
// own for the rest of the test.
func useStickMount(t *testing.T, f *fakeStickMount) {
	t.Helper()
	savedMount, savedUnmount, savedTarget := mountStick, unmountStick, reportStickMount
	t.Cleanup(func() { mountStick, unmountStick, reportStickMount = savedMount, savedUnmount, savedTarget })
	f.dir = t.TempDir()
	reportStickMount = f.dir
	mountStick = func(device, target string) error { return f.mountErr }
	unmountStick = func(target string, flags int) error {
		f.unmounts = append(f.unmounts, flags)
		if flags == 0 {
			return f.busyErr
		}
		return nil
	}
}

// readProposal reads the proposal the report left on the fake volume,
// or the empty string when it left none.
func readProposal(f *fakeStickMount) string {
	raw, _ := os.ReadFile(filepath.Join(f.dir, hardwareReportName))
	return string(raw)
}

// errorText is an error's message, or the empty string for no error.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// The report writes the proposal to the root of the stick's volume and
// unmounts the volume, so the filesystem is clean when the person pulls
// the stick. A busy volume gets a lazy detach, so a later boot does not
// find a stale mount. The report writes only to a stick it resolved to
// exactly one disk: with no stick, or with two disks that each carry the
// stick's partition name, it refuses before it mounts anything. A volume
// that does not mount gets no file and no unmount. Each error tells the
// person why the file is not on the stick.
func TestWriteReportToStickLeavesTheProposalOnACleanVolume(t *testing.T) {
	stick := installStick{Disk: "sdb", Partition: "/dev/sdb1"}
	cases := []struct {
		name         string
		stick        installStick
		mount        *fakeStickMount
		wantErr      string
		wantFile     string
		wantUnmounts []int
	}{
		{"a stick that mounts", stick, &fakeStickMount{}, "", "kind: Machine\n", []int{0}},
		{"a busy stick", stick, &fakeStickMount{busyErr: unix.EBUSY}, "", "kind: Machine\n", []int{0, unix.MNT_DETACH}},
		{"a stick that does not mount", stick, &fakeStickMount{mountErr: unix.EINVAL},
			"mounting the stick /dev/sdb1: invalid argument", "", nil},
		{"no stick", installStick{}, &fakeStickMount{},
			"no installation stick found (no partition named liken:install)", "", nil},
		{"two sticks", installStick{Candidates: []string{"sdb", "sdc"}}, &fakeStickMount{},
			"more than one disk carries liken:install (sdb, sdc); refusing to guess which is the stick", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			useStickMount(t, c.mount)

			err := writeReportToStick(c.stick, "kind: Machine\n")

			if got := errorText(err); got != c.wantErr {
				t.Errorf("the write answered %q, want %q", got, c.wantErr)
			}
			if got := readProposal(c.mount); got != c.wantFile {
				t.Errorf("the stick holds %q, want %q", got, c.wantFile)
			}
			if !slices.Equal(c.mount.unmounts, c.wantUnmounts) {
				t.Errorf("the report unmounted with flags %v, want %v", c.mount.unmounts, c.wantUnmounts)
			}
		})
	}
}

// useEnding stands in for the reboot that ends the report boot, for the
// rest of the test, and returns how many times the boot ended.
func useEnding(t *testing.T) *int {
	t.Helper()
	saved := endReport
	t.Cleanup(func() { endReport = saved })
	endings := 0
	endReport = func() { endings++ }
	return &endings
}

// The report boot gathers the machine, writes the proposal to the stick,
// and ends with a reboot. The proposal names the machine's disk and its
// cabled port. A stick that does not mount gets no file, and the boot
// still ends, because the console holds the only copy and the person
// reads it there.
func TestRunHardwareReportWritesTheProposalAndEnds(t *testing.T) {
	cases := []struct {
		name     string
		mount    *fakeStickMount
		wantFile bool
	}{
		{"a stick that mounts", &fakeStickMount{}, true},
		{"a stick that does not mount", &fakeStickMount{mountErr: unix.EINVAL}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				reportMachine(t)
				useImageMount(t, failedMount)
				useStickMount(t, c.mount)
				fakeCmdline(t, "console=ttyS0 liken.report\n")
				endings := useEnding(t)

				runHardwareReport()

				proposal := readProposal(c.mount)
				if got := strings.Contains(proposal, "sda") && strings.Contains(proposal, "eth0"); got != c.wantFile {
					t.Errorf("the stick holds a proposal that names sda and eth0: %v, want %v:\n%s", got, c.wantFile, proposal)
				}
				if *endings != 1 {
					t.Errorf("the report boot ended %d times, want once", *endings)
				}
			})
		})
	}
}

// The held console's last line tells the person whether the proposal
// reached the stick. After a failed write it says the console holds the
// only copy, so the person copies it before pulling the stick.
func TestReportPromptTellsThePersonWhereTheReportIs(t *testing.T) {
	cases := []struct {
		name     string
		writeErr error
		want     string
	}{
		{"a written report", nil,
			"liken: this report was written to the stick as hardware-report.yaml; press Enter to reboot."},
		{"a failed write", errors.New("mounting the stick: permission denied"),
			"liken: writing hardware-report.yaml to the stick FAILED; the text above is the only copy; press Enter to reboot."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reportPrompt(c.writeErr); got != c.want {
				t.Errorf("reportPrompt = %q, want %q", got, c.want)
			}
		})
	}
}
