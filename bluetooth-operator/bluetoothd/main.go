// start-bluetoothd starts the pod's D-Bus system bus, waits for the
// bus socket, and then becomes bluetoothd.
//
// The bus belongs to this pod. bluetoothd's whole API is the D-Bus
// system bus, and a pod has no host bus to join, so this image includes
// dbus-daemon and starts a bus with one service and two clients on it:
// bluetoothd owns org.bluez, and the operator's container and any
// bluetoothctl session read it. This is also why liken's system image
// needs no D-Bus: the bus exists only for this daemon and ships in the
// image that holds the daemon.
//
// This program is Go rather than a shell script because the image has
// no shell. Three static binaries and their configuration are
// the whole of it, and a shell would be a fourth binary added only to
// run this program.
//
// The exec at the end makes bluetoothd this container's process, so
// the kubelet's TERM reaches bluetoothd directly and bluetoothd writes
// its bonds out before it goes.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// The bus is at a path of this operator's own, not at dbus's
	// packaged /run/dbus, and every process here finds it through
	// DBUS_SYSTEM_BUS_ADDRESS. bluetoothd and bluetoothctl read that
	// variable through libdbus, and the operator reads it through
	// godbus.
	//
	// The path is a contract. The pod mounts one emptyDir here and
	// gives both containers the same address, and a later capability
	// that has to reach this bluetoothd over its bus, such as a
	// PipeWire that handles Bluetooth audio, runs in this pod and
	// mounts the same directory.
	//
	// The directory is the mount unit, never the socket file.
	// dbus-daemon unlinks and recreates the socket at every start, so a
	// mount of the file alone would pin the inode the daemon deleted,
	// and whatever mounted it would hold a socket with no
	// listener.
	busAddressVar     = "DBUS_SYSTEM_BUS_ADDRESS"
	defaultBusAddress = "unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket"
	unixAddressPrefix = "unix:path="

	dbusDaemonPath = "/usr/bin/dbus-daemon"
	bluetoothdPath = "/usr/libexec/bluetooth/bluetoothd"
	inputConfPath  = "/etc/bluetooth/input.conf"
	mainConfPath   = "/etc/bluetooth/main.conf"

	// privacyPath is the file that bondfetch writes from the Adapter's
	// spec.privacy before this container starts. The directory is the
	// pod's settings volume, which the operator's container also
	// mounts, so the operator reads the value that this bluetoothd
	// started with.
	privacyPath = "/var/run/bluetooth.liken.sh/settings/privacy"

	classicBondedOnlyVar = "BLUETOOTH_CLASSIC_BONDED_ONLY"

	// busTimeout bounds the wait for the bus socket. dbus-daemon forks
	// to the background and the parent exits 0 whether the bus came up
	// or not, so a failed start shows up here as a socket that never
	// appears, and this is how long that takes to report.
	busTimeout = 10 * time.Second

	// busPoll is how often the wait looks for the socket. There is no
	// event to wait on: the socket appears in a directory this program
	// created, and an inotify watch for one file that arrives in
	// milliseconds costs more than it saves.
	busPoll = 100 * time.Millisecond
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "start-bluetoothd: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	address := os.Getenv(busAddressVar)
	if address == "" {
		address = defaultBusAddress
	}
	socket, err := busSocket(address)
	if err != nil {
		return err
	}
	// bluetoothd inherits the address, so the pod may state it once and
	// both containers agree.
	if err := os.Setenv(busAddressVar, address); err != nil {
		return err
	}

	if err := writeInputConf(inputConfPath, os.Getenv(classicBondedOnlyVar)); err != nil {
		return err
	}
	if err := writeMainConf(mainConfPath, privacyPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		return err
	}

	// --address overrides the listen address in dbus's own system.conf
	// and leaves every policy rule in it, including the one BlueZ
	// installs that lets root own org.bluez.
	//
	// --nopidfile drops the other thing system.conf asks for. The pid
	// file exists so an init system can signal the bus, and this
	// container has no init system: the bus runs only as long as the
	// container that starts it.
	//
	// Run waits for the forking parent, which exits as soon as it has
	// spawned the bus, so this returns in milliseconds and returns 0
	// whether the bus came up or not. The wait below is the real check.
	// The bus itself is left as a child of this process, which the exec
	// at the end turns into bluetoothd.
	bus := exec.Command(dbusDaemonPath, "--system", "--nopidfile", "--address="+address)
	bus.Stdout, bus.Stderr = os.Stdout, os.Stderr
	if err := bus.Run(); err != nil {
		return fmt.Errorf("starting dbus-daemon: %w", err)
	}
	if err := waitForSocket(socket); err != nil {
		return err
	}

	// -n keeps bluetoothd in the foreground, because it daemonizes by
	// default and a daemonized process would leave this container with
	// nothing running in it.
	return syscall.Exec(bluetoothdPath, []string{bluetoothdPath, "-n"}, os.Environ())
}

// busSocket reads the socket path out of a D-Bus address. Only the
// unix:path= form appears here, because this program creates the
// directory the socket is in and then waits for the socket, and
// neither is a thing it can do for an abstract socket or a TCP
// address.
func busSocket(address string) (string, error) {
	path, found := strings.CutPrefix(address, unixAddressPrefix)
	if !found || path == "" {
		return "", fmt.Errorf("%s must be %s<path>, not %q", busAddressVar, unixAddressPrefix, address)
	}
	return path, nil
}

// writeInputConf writes BlueZ's ClassicBondedOnly setting at start
// rather than baking it into the image, because the choice between its
// two values is a security decision made at deploy time.
//
// true, the default, is BlueZ's own default and the fix for
// CVE-2023-45866: with it off, an attacker in radio range can open an
// HID channel without any bond and inject keystrokes into the machine.
// The operator's own pairing runs with it on, because the operator
// pairs over D-Bus and the bond registers before any input channel
// opens. false is the fallback for a controller that opens its HID
// channel first: turn it off for that pairing, pair, and turn it back
// on. The bonds persist on their volume, so the flip costs one restart
// and nothing else.
//
// Any other value is a failure to start. BlueZ reads this file with
// glib, and glib reads a value it does not recognize as false, so a
// misspelled "yes" reads as false and disables the protection with no
// error.
//
// UserspaceHID=false hands a classic HID connection to the kernel's
// hidp module once bluetoothd has set it up. With BlueZ's default,
// bluetoothd reads every input report from the L2CAP interrupt
// channel and writes it to /dev/uhid, which wakes it for each report;
// a DualSense sends about 400 a second. Over hidp the kernel owns
// both channels and bluetoothd never wakes for a report. HID over
// GATT, which Low Energy remotes use, is a different profile and
// keeps going through uhid whatever this setting says.
func writeInputConf(path, bondedOnly string) error {
	switch bondedOnly {
	case "":
		bondedOnly = "true"
	case "true", "false":
	default:
		return fmt.Errorf("%s must be true or false, not %q", classicBondedOnlyVar, bondedOnly)
	}
	contents := fmt.Sprintf("[General]\nClassicBondedOnly=%s\nUserspaceHID=false\n", bondedOnly)
	return os.WriteFile(path, []byte(contents), 0o644)
}

// privacyValues are the values of BlueZ's Privacy key, spelled as
// parse_privacy in BlueZ's src/main.c reads them. The Adapter's
// spec.privacy states the same five in its schema.
var privacyValues = map[string]bool{
	"off":             true,
	"network":         true,
	"device":          true,
	"limited-network": true,
	"limited-device":  true,
}

// writeMainConf writes bluetoothd's main.conf at start, because the
// Privacy key in it comes from the Adapter, and a person sets it for
// one radio.
//
// bluetoothd reads Privacy once, and sends it to the kernel when each
// adapter starts. The kernel accepts the privacy command only while
// the radio is powered off. So the value in this file is the value
// for the life of this bluetoothd, and the operator deletes its pod to
// apply a new one.
//
// A missing settings file means privacy off, which is BlueZ's own
// default. A pod with no settings volume then starts as BlueZ would
// start with no Privacy key. Any value outside the five is a failure
// to start. parse_privacy reads a value it does not recognize as off,
// so a typing error would turn privacy off with no error.
//
// AutoEnable powers the adapter on when bluetoothd starts. bluetoothd
// leaves an adapter down otherwise, so without this a machine that
// reboots comes back with its radio off and its bonds unreachable, and
// somebody has to run bluetoothctl to turn it on. No deployment has a
// reason to turn it off, so it has no setting.
func writeMainConf(path, settings string) error {
	privacy := "off"
	contents, err := os.ReadFile(settings)
	switch {
	case err == nil:
		privacy = strings.TrimSpace(string(contents))
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("reading the privacy setting: %w", err)
	}
	if !privacyValues[privacy] {
		return fmt.Errorf("%s must be off, network, device, limited-network, or limited-device, not %q", settings, privacy)
	}
	conf := fmt.Sprintf("[General]\nPrivacy=%s\n\n[Policy]\nAutoEnable=true\n", privacy)
	return os.WriteFile(path, []byte(conf), 0o644)
}

// waitForSocket waits for dbus-daemon to bind the bus socket. It
// checks that the path is a socket and not merely a name, because a
// stale file at that path would otherwise read as a bus that is
// listening.
func waitForSocket(path string) error {
	deadline := time.Now().Add(busTimeout)
	for {
		info, err := os.Stat(path)
		if err == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("dbus-daemon did not create %s within %s", path, busTimeout)
		}
		time.Sleep(busPoll)
	}
}
