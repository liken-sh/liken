package main

// The loop mount, raising a link, the stick's mount, and the reboot
// need root, so these tests replace each of them with a fake.

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

// quietListener returns a uevent channel that stays open and never
// sends, like a machine whose probe has finished.
func quietListener(context.Context) (<-chan struct{}, error) {
	return make(chan struct{}), nil
}

// With a working listener, quiesceHardware waits for one second with no
// uevent. When the socket does not open or the listener stops, it
// sleeps for quiescePause.
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

// fakeNetlink replaces listLinks and raiseLink. listLinks returns links
// and listErr. raiseLink records each name in raised, and fails for the
// name in refuse.
type fakeNetlink struct {
	links   []netlink.Link
	listErr error
	refuse  string
	raised  []string
}

// useLinks installs f until the test ends.
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
		t.Fatalf("parsing the MAC address %q: %v", mac, err)
	}
	return &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: name, HardwareAddr: addr}}
}

// loopback has an all-zero MAC address, so only its flag tells it apart
// from hardware.
var loopback = &netlink.Device{LinkAttrs: netlink.LinkAttrs{
	Name: "lo", Flags: net.FlagLoopback, HardwareAddr: make(net.HardwareAddr, 6),
}}

// tunnel is a virtual interface with no MAC address.
var tunnel = &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: "wg0"}}

// operstate writes an interface's operstate into the fake sysfs tree.
func operstate(t *testing.T, root, name, state string) {
	t.Helper()
	dir := filepath.Join(root, "class", "net", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the sysfs directory of %s: %v", name, err)
	}
	writeSysfs(t, dir, "operstate", state+"\n")
}

// observeInterfaces raises every port with a MAC address, skips
// loopback and wg0, and waits three seconds for autonegotiation before
// it reads operstate. eth1 fails to come up and has no operstate, and
// it is still reported, as unknown.
func TestObserveInterfacesRaisesAndReadsEveryHardwarePort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := fakeSysfs(t)
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

// With no port to raise, observeInterfaces returns nothing and does not
// wait.
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
				fakeSysfs(t)
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

// useImageMount replaces the loop mount of the payload's system image
// with mount, and points reportImageMount at a temporary directory,
// until the test ends. It returns that directory.
func useImageMount(t *testing.T, mount func(image, target string) error) string {
	t.Helper()
	savedMount, savedTarget := mountReportImage, reportImageMount
	t.Cleanup(func() { mountReportImage, reportImageMount = savedMount, savedTarget })
	mountReportImage = mount
	reportImageMount = t.TempDir()
	return reportImageMount
}

// reportMachine builds a fake machine with UEFI firmware, a 500 GiB
// disk, the install stick, a port with a carrier, and a display
// controller with no driver. It returns the fake sysfs root and /dev.
func reportMachine(t *testing.T) (root, dev string) {
	t.Helper()
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "model", "REAL DISK\n")
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", stickInstallPartition, 1<<30)
	root = fakeSysfs(t)
	addPCIDevice(t, root, "0000:00:02.0", "0x030000", displayModalias)
	operstate(t, root, "eth0", "up")
	useLinks(t, &fakeNetlink{links: []netlink.Link{loopback, port(t, "eth0", "52:54:00:12:34:56")}})
	useListener(t, quietListener)
	savedEFI := efiSysDir
	t.Cleanup(func() { efiSysDir = savedEFI })
	efiSysDir = t.TempDir()
	return root, dev
}

// reportSummary formats the report fields that the gather tests check
// as one line, so a test compares one string.
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

// failedMount is a loop mount that fails.
func failedMount(image, target string) error {
	return fmt.Errorf("attaching %s: operation not permitted", image)
}

// payloadMount is a loop mount that succeeds. It writes a
// modules.alias at the mount point that maps the display controller to
// the bochs driver.
func payloadMount(image, target string) error {
	tree := filepath.Join(target, "lib", "modules", kernelRelease())
	if err := os.MkdirAll(tree, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(tree, "modules.alias"), []byte("alias "+displayModalias+" bochs\n"), 0o644)
}

// The report lists the disks, without the stick, and the ports, whether
// or not the payload's system image mounts. Only the image's
// modules.alias maps the display controller to a driver, so the
// controller is claimable only when the image mounts.
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

// fakeStickMount replaces the stick's FAT volume with a temporary
// directory. A mount returns mountErr, and an unmount with no flags
// returns busyErr, as a plain unmount of a busy volume fails. unmounts
// records the flags of each unmount.
type fakeStickMount struct {
	dir      string
	mountErr error
	busyErr  error
	unmounts []int
}

// useStickMount installs f until the test ends.
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

// readProposal returns the proposal on the fake volume, or "" when
// there is none.
func readProposal(f *fakeStickMount) string {
	raw, _ := os.ReadFile(filepath.Join(f.dir, hardwareReportName))
	return string(raw)
}

// errorText returns err's message, or "" for a nil error.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// writeReportToStick writes the proposal and unmounts the volume, with
// a lazy detach when the volume is busy. With no stick, or with more
// than one disk that has the stick's partition name, it fails before it
// mounts anything.
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
			"no disk has a partition named liken:install, so the report found no install stick", "", nil},
		{"two sticks", installStick{Candidates: []string{"sdb", "sdc"}}, &fakeStickMount{},
			"more than one disk has a partition named liken:install (sdb, sdc), so the report cannot tell which disk is the stick", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			useStickMount(t, c.mount)

			err := writeReportToStick(c.stick, "kind: Machine\n")

			if got := errorText(err); got != c.wantErr {
				t.Errorf("the write returned %q, want %q", got, c.wantErr)
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

// useEnding replaces endReport until the test ends, and returns a
// count of its calls.
func useEnding(t *testing.T) *int {
	t.Helper()
	saved := endReport
	t.Cleanup(func() { endReport = saved })
	endings := 0
	endReport = func() { endings++ }
	return &endings
}

// When the stick does not mount, the report boot still ends, because
// the proposal is printed on the console.
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

func TestReportPromptTellsThePersonWhereTheReportIs(t *testing.T) {
	cases := []struct {
		name     string
		writeErr error
		want     string
	}{
		{"a written report", nil,
			"liken: wrote this report to the stick as hardware-report.yaml. Press Enter to reboot."},
		{"a failed write", errors.New("mounting the stick: permission denied"),
			"liken: writing hardware-report.yaml to the stick failed, so the text above is the only copy. Press Enter to reboot."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reportPrompt(c.writeErr); got != c.want {
				t.Errorf("reportPrompt = %q, want %q", got, c.want)
			}
		})
	}
}
