package main

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// fakeSerioMachine points sysfs, /dev, and /sys/module at temporary
// directories, and returns the sysfs root. The roots are package
// variables, so tests in this package must not run in parallel.
func fakeSerioMachine(t *testing.T) string {
	t.Helper()
	sys, dev, modules := t.TempDir(), t.TempDir(), t.TempDir()
	oldSys, oldDev, oldModules := sysfsRoot, devRoot, sysModuleDir
	sysfsRoot, devRoot, sysModuleDir = sys, dev, modules
	t.Cleanup(func() { sysfsRoot, devRoot, sysModuleDir = oldSys, oldDev, oldModules })
	// A virtual terminal is a tty with no device link, and every
	// machine has one.
	mkdirs(t, filepath.Join(sys, "devices", "virtual", "tty", "tty0"))
	mkdirs(t, filepath.Join(sys, "class", "tty"))
	symlink(t, filepath.Join(sys, "devices", "virtual", "tty", "tty0"), filepath.Join(sys, "class", "tty", "tty0"))
	return sys
}

func mkdirs(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	mkdirs(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}
}

// loadSerioModules marks modules as resident in the fake /sys/module.
func loadSerioModules(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		mkdirs(t, filepath.Join(sysModuleDir, name))
	}
}

// adapter is one fake CDC ACM adapter: its port path on the USB bus,
// its serial number, and the tty that cdc_acm created for it. An empty
// tty means that cdc_acm is not loaded. Unless portless is set, plug
// also builds the serio port with its driver bound, as after a
// successful attach.
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

// unplug removes everything plug built.
//
// It moves the tty's directory to another temporary directory instead
// of deleting it, so its inode stays in use until the test ends.
// discoverSerialLines uses the inode to tell a tty that registers again
// apart from the old one. kernfs allocates inode numbers cyclically, so
// a new registration does not get the old number. A filesystem such as
// ext4 can give a freed inode to the next new directory, which would
// make the replugged tty look like the old one.
func (a adapter) unplug(t *testing.T) {
	t.Helper()
	if a.tty != "" {
		if _, err := os.Stat(a.ttyDir()); err == nil {
			if err := os.Rename(a.ttyDir(), filepath.Join(t.TempDir(), a.tty)); err != nil {
				t.Fatalf("moving the tty directory aside: %v", err)
			}
		}
	}
	for _, path := range []string{
		filepath.Join(sysfsRoot, "class", "tty", a.tty),
		filepath.Join(sysfsRoot, "bus", "usb", "devices", a.port),
		a.deviceDir(),
	} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatalf("removing the adapter path: %v", err)
		}
	}
}

// registerPort builds what the kernel registers under the tty after a
// successful attach: the serio port with its driver bound, the CEC
// adapter under it, and the remote-control input device with its
// event node.
func (a adapter) registerPort(t *testing.T) {
	t.Helper()
	a.registerUnboundPort(t)
	port := filepath.Join(a.ttyDir(), "serio0")
	symlink(t, filepath.Join(sysfsRoot, "bus", "serio", "drivers", "pulse8-cec"), filepath.Join(port, "driver"))
}

// registerUnboundPort builds the port and its devices with no driver
// link on the port, as when the driver's probe failed.
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

// fakeTTYs is the opener that serioRegistry uses in these tests. Each
// open records the path and returns the next line from lines, or a line
// whose read blocks until the test ends.
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

// holdingLine is a line whose read blocks until the test closes
// release, as serport's read blocks until an unplug closes the port.
func holdingLine() *fakeLine {
	return &fakeLine{release: make(chan struct{})}
}

var pulse8Entry = machine.SerioAttachment{
	Protocol: "pulse8-cec",
	USB:      machine.SerioUSB{Vendor: "2548", Product: "1002"},
}

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
		t.Errorf("the serial lines are %+v, want %+v", got, want)
	}
	if len(identities) != 2 || identities[0] {
		t.Errorf("the identities are %v, want two different nonzero values", identities)
	}
}

func TestATTYThatRegistersAgainHasANewIdentity(t *testing.T) {
	fakeSerioMachine(t)
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	before := discoverSerialLines()
	pulse.unplug(t)
	pulse.plug(t)
	after := discoverSerialLines()

	if before[0].identity == after[0].identity {
		t.Errorf("the tty has identity %d before and after registering again, want a different one", after[0].identity)
	}
}

func TestUSBDevicePresentReadsTheBusNotTheLines(t *testing.T) {
	fakeSerioMachine(t)
	adapter{port: "1-4"}.plug(t)
	other := pulse8Entry
	other.USB.Product = "1001"

	if !usbDevicePresent(pulse8Entry) {
		t.Error("usbDevicePresent is false for the plugged adapter, want true")
	}
	if usbDevicePresent(other) {
		t.Error("usbDevicePresent is true for a product that is not plugged in, want false")
	}
}
