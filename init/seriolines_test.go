package main

// The fake machine the serio tests run against: a sysfs with the tty
// class, the USB bus, and /sys/module, all built in tempdirs, and a
// fake opener in place of the ttys. The fixture writes the links and
// files the kernel writes for a CDC ACM adapter, so a test describes
// an adapter and the fixture owns the layout.

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// fakeSerioMachine points sysfs, /dev, and /sys/module at tempdirs,
// and returns the sysfs root. Because the roots are package variables,
// tests in this package must not run in parallel.
func fakeSerioMachine(t *testing.T) string {
	t.Helper()
	sys, dev, modules := t.TempDir(), t.TempDir(), t.TempDir()
	oldSys, oldDev, oldModules := sysfsRoot, devRoot, sysModuleDir
	sysfsRoot, devRoot, sysModuleDir = sys, dev, modules
	t.Cleanup(func() { sysfsRoot, devRoot, sysModuleDir = oldSys, oldDev, oldModules })
	// A virtual terminal is a tty with no device link. Every machine
	// has one, and the walk must pass over it.
	mkdirs(t, filepath.Join(sys, "devices", "virtual", "tty", "tty0"))
	mkdirs(t, filepath.Join(sys, "class", "tty"))
	symlink(t, filepath.Join(sys, "devices", "virtual", "tty", "tty0"), filepath.Join(sys, "class", "tty", "tty0"))
	return sys
}

func mkdirs(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	mkdirs(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// loadSerioModules marks modules as resident in the fake /sys/module.
func loadSerioModules(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		mkdirs(t, filepath.Join(sysModuleDir, name))
	}
}

// adapter is one fake USB device: its port path on the bus, its
// identity, and the tty cdc_acm gave it. An empty tty is an adapter
// whose line driver is not loaded. plug also builds the serio port
// with its driver bound, the state an attach leads to, unless portless
// asks for a tty that the kernel has not attached anything under yet.
type adapter struct {
	port, serial, tty string
	portless          bool
}

func (a adapter) deviceDir() string {
	return filepath.Join(sysfsRoot, "devices", "usb1", a.port)
}

func (a adapter) ttyDir() string {
	return filepath.Join(a.deviceDir(), a.port+":1.0", "tty", a.tty)
}

// plug builds the adapter's sysfs entries: the USB device with its
// identity, its communications interface, and the tty under it, with
// the class and bus links that point at them.
func (a adapter) plug(t *testing.T) {
	t.Helper()
	device := a.deviceDir()
	mkdirs(t, device)
	writeSysfs(t, device, "idVendor", "2548\n")
	writeSysfs(t, device, "idProduct", "1002\n")
	if a.serial != "" {
		writeSysfs(t, device, "serial", a.serial+"\n")
	}
	iface := filepath.Join(device, a.port+":1.0")
	mkdirs(t, iface)
	symlink(t, device, filepath.Join(sysfsRoot, "bus", "usb", "devices", a.port))
	if a.tty == "" {
		return
	}
	dir := a.ttyDir()
	mkdirs(t, dir)
	writeSysfs(t, dir, "dev", "166:0\n")
	writeSysfs(t, dir, "uevent", "DEVNAME="+a.tty+"\n")
	symlink(t, iface, filepath.Join(dir, "device"))
	symlink(t, dir, filepath.Join(sysfsRoot, "class", "tty", a.tty))
	if !a.portless {
		a.registerPort(t)
	}
}

// unplug removes everything plug built, the way the kernel removes a
// device's entries when it leaves.
func (a adapter) unplug(t *testing.T) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(sysfsRoot, "class", "tty", a.tty),
		filepath.Join(sysfsRoot, "bus", "usb", "devices", a.port),
		a.deviceDir(),
	} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
}

// registerPort builds what the kernel registers under the tty once the
// attach holds: the serio port with its driver bound, the CEC adapter
// under it, and the remote-control input device with its event node.
func (a adapter) registerPort(t *testing.T) {
	t.Helper()
	a.registerUnboundPort(t)
	port := filepath.Join(a.ttyDir(), "serio0")
	symlink(t, filepath.Join(sysfsRoot, "bus", "serio", "drivers", "pulse8-cec"), filepath.Join(port, "driver"))
}

// registerUnboundPort builds the port and its devices with no driver
// link on the port, the state a driver whose probe failed leaves.
func (a adapter) registerUnboundPort(t *testing.T) {
	t.Helper()
	port := filepath.Join(a.ttyDir(), "serio0")
	node := func(rel, subsystem, devname string) {
		dir := filepath.Join(port, rel)
		mkdirs(t, dir)
		symlink(t, filepath.Join(sysfsRoot, "class", subsystem), filepath.Join(dir, "subsystem"))
		writeSysfs(t, dir, "dev", "1:1\n")
		writeSysfs(t, dir, "uevent", "DEVNAME="+devname+"\n")
	}
	mkdirs(t, port)
	node("cec/cec0", "cec", "cec0")
	node("rc/rc0/input83/event13", "input", "input/event13")
}

// fakeTTYs is the opener the registry uses in these tests. Each open
// takes the next line from lines, or a line that holds its read until
// the test ends, and records the path it opened.
type fakeTTYs struct {
	mu    sync.Mutex
	lines []*fakeLine
	opens []string
	gate  chan struct{}
	t     *testing.T
}

func newFakeTTYs(t *testing.T, lines ...*fakeLine) *fakeTTYs {
	return &fakeTTYs{lines: lines, t: t}
}

func (f *fakeTTYs) open(path string) (serialLine, error) {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens = append(f.opens, path)
	if len(f.lines) > 0 {
		line := f.lines[0]
		f.lines = f.lines[1:]
		return line, nil
	}
	line := &fakeLine{release: make(chan struct{})}
	f.t.Cleanup(func() { close(line.release) })
	return line, nil
}

func (f *fakeTTYs) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.opens)
}

// holdingLine is a line whose read blocks until the test releases it,
// which is how the kernel ends the port on an unplug.
func holdingLine() *fakeLine {
	return &fakeLine{release: make(chan struct{})}
}

var pulse8Entry = machine.SerioAttachment{
	Protocol: "pulse8-cec",
	USB:      machine.SerioUSB{Vendor: "2548", Product: "1002"},
}

// The walk finds each adapter's line by the identity above its tty,
// and passes over a tty that belongs to no hardware.
func TestDiscoverSerialLinesReadsTheIdentityAboveEachTTY(t *testing.T) {
	fakeSerioMachine(t)
	one := adapter{port: "1-4", serial: "A1", tty: "ttyACM0"}
	one.plug(t)
	adapter{port: "1-5", tty: "ttyACM1"}.plug(t)

	got := discoverSerialLines()

	two := adapter{port: "1-5", tty: "ttyACM1"}
	want := []serialLineInfo{
		{tty: "ttyACM0", dir: one.ttyDir(), usbPath: one.deviceDir(), vendor: "2548", product: "1002", serial: "A1"},
		{tty: "ttyACM1", dir: two.ttyDir(), usbPath: two.deviceDir(), vendor: "2548", product: "1002"},
	}
	identities := map[uint64]bool{}
	for i := range got {
		identities[got[i].identity] = true
		got[i].identity = 0
	}
	if !slices.Equal(got, want) {
		t.Errorf("lines = %+v", got)
	}
	if len(identities) != 2 || identities[0] {
		t.Errorf("each tty carries its own identity: %v", identities)
	}
}

// A tty that registers again under the same name gets a new directory,
// and the walk reads it as new hardware.
func TestATTYThatRegistersAgainHasANewIdentity(t *testing.T) {
	fakeSerioMachine(t)
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	before := discoverSerialLines()
	pulse.unplug(t)
	pulse.plug(t)
	after := discoverSerialLines()

	if before[0].identity == after[0].identity {
		t.Errorf("identity %d stayed", after[0].identity)
	}
}

func TestUSBDevicePresentReadsTheBusNotTheLines(t *testing.T) {
	fakeSerioMachine(t)
	adapter{port: "1-4"}.plug(t)
	other := pulse8Entry
	other.USB.Product = "1001"

	if !usbDevicePresent(pulse8Entry) || usbDevicePresent(other) {
		t.Error("the bus lists the adapter with no line, and nothing else")
	}
}
