// start-bluetoothd is the entrypoint of the bluetoothd container. It
// starts a D-Bus system bus, writes bluetoothd's configuration, powers
// every adapter off, and then replaces itself with bluetoothd.
//
// bluetoothd serves its API only on the D-Bus system bus, and the
// `liken` OS runs no D-Bus. So the image includes dbus-daemon, and this
// program starts a system bus for this bluetoothd alone. The operator's
// container and any bluetoothctl session connect to it as clients.
//
// The image has no shell, so the entrypoint is a Go program.
//
// The exec makes bluetoothd the container's main process. The TERM that
// the kubelet sends at pod shutdown then reaches bluetoothd, which
// powers its adapters off before it exits (see poweroff.go).
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

	"github.com/liken-sh/liken/bluetooth-operator/bluetoothd/mgmt"
)

const (
	// The bus socket is in a directory that the pod mounts from the
	// host into the bluetoothd and operator containers, so both reach
	// the same bus. A consumer such as PipeWire gets a mount of the same
	// directory through its claim's CDI spec (cdi.go in the operator).
	// Each process finds the socket through DBUS_SYSTEM_BUS_ADDRESS,
	// which libdbus and godbus both read.
	//
	// The mount is the directory and not the socket file, because
	// dbus-daemon deletes and creates the socket again at every start.
	// A mount of the file would keep the deleted socket, which has no
	// listener.
	busAddressVar     = "DBUS_SYSTEM_BUS_ADDRESS"
	defaultBusAddress = "unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket"
	unixAddressPrefix = "unix:path="

	dbusDaemonPath = "/usr/bin/dbus-daemon"
	bluetoothdPath = "/usr/libexec/bluetooth/bluetoothd"
	inputConfPath  = "/etc/bluetooth/input.conf"
	mainConfPath   = "/etc/bluetooth/main.conf"

	// privacyPath is the file that bondfetch writes from the Adapter's
	// spec.privacy before this container starts. The operator's
	// container mounts the same volume, so it can read the value that
	// this bluetoothd started with.
	privacyPath = "/var/run/bluetooth.liken.sh/settings/privacy"

	classicBondedOnlyVar = "BLUETOOTH_CLASSIC_BONDED_ONLY"

	busTimeout = 10 * time.Second

	// busPoll is the interval between checks for the socket. The socket
	// normally appears within milliseconds, so a poll is simpler than an
	// inotify watch.
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
	// bluetoothd inherits the environment, so it gets the default
	// address when the pod did not set one.
	if err := os.Setenv(busAddressVar, address); err != nil {
		return fmt.Errorf("setting %s: %w", busAddressVar, err)
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

	// --address replaces the listen address of dbus's system.conf and
	// keeps its policy rules, including the rule from BlueZ that lets
	// root own org.bluez. --nopidfile skips the pid file, which only an
	// init system reads.
	//
	// dbus-daemon forks, and the parent exits 0 whether or not the bus
	// started. So a failed start shows only as a socket that never
	// appears, and waitForSocket is the real check. The bus keeps
	// running as a child of this process after the exec.
	bus := exec.Command(dbusDaemonPath, "--system", "--nopidfile", "--address="+address)
	bus.Stdout, bus.Stderr = os.Stdout, os.Stderr
	if err := bus.Run(); err != nil {
		return fmt.Errorf("starting dbus-daemon: %w", err)
	}
	if err := waitForSocket(socket); err != nil {
		return err
	}

	// The adapters are powered off last, after the bus is up, so they
	// stay off for as short a time as possible.
	return becomeBluetoothd(mgmt.Open, os.Stdout, syscall.Exec)
}

// busSocket returns the socket path of a D-Bus address. It accepts only
// the unix:path= form, because run creates the socket's directory and
// waits for the socket file, and an abstract socket or a TCP address has
// neither.
func busSocket(address string) (string, error) {
	path, found := strings.CutPrefix(address, unixAddressPrefix)
	if !found || path == "" {
		return "", fmt.Errorf("%s must be %s<path>, not %q", busAddressVar, unixAddressPrefix, address)
	}
	return path, nil
}

// writeInputConf writes BlueZ's input.conf at container start, so a
// deployment can change ClassicBondedOnly, a security setting, without
// a new image.
//
// An empty value means true, which is BlueZ's default and the fix for
// CVE-2023-45866. With false, an attacker in radio range can open an
// HID channel without a bond and inject keystrokes. The operator pairs
// over D-Bus, which creates the bond before any input channel opens, so
// its pairing works with true. A controller that opens its HID channel
// before it bonds needs false: set false, pair, and set true again. The
// bonds stay on their volume, so each change costs one restart.
//
// A value other than true or false is an error, because glib, which
// BlueZ uses to read the file, reads an unknown value as false. A
// misspelled "yes" would turn the protection off with no warning.
//
// UserspaceHID=false makes bluetoothd give a classic HID connection to
// the kernel's hidp module after it sets the connection up. With
// BlueZ's default, bluetoothd copies each input report from L2CAP to
// /dev/uhid, so it wakes for every report. With hidp, the kernel reads
// the reports. HID over
// GATT, which Low Energy remotes use, is a different profile and still
// goes through uhid.
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

// privacyValues are the values that parse_privacy in BlueZ's
// src/main.c accepts for the Privacy key. The enum of the Adapter's
// spec.privacy in deploy/crds.yaml lists the same values.
var privacyValues = map[string]bool{
	"off":             true,
	"network":         true,
	"device":          true,
	"limited-network": true,
	"limited-device":  true,
}

// writeMainConf writes bluetoothd's main.conf, with the Privacy value
// from the settings file.
//
// bluetoothd sends Privacy to the kernel when it sets up each adapter,
// and the kernel accepts it only while the adapter is powered off. So a
// new value takes effect only in a new bluetoothd, and the operator
// deletes the pod to apply it.
//
// When the settings file does not exist, Privacy is off, BlueZ's
// default. A value that is not in privacyValues is an error, because
// parse_privacy reads an unknown value as off, and a typing error would
// turn privacy off with no warning.
//
// AutoEnable=true makes bluetoothd power each adapter on when it
// starts. becomeBluetoothd powers every adapter off first, so without
// AutoEnable the adapters would stay off until someone ran
// bluetoothctl.
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
		return fmt.Errorf("the privacy setting in %s must be off, network, device, limited-network, or limited-device, not %q", settings, privacy)
	}
	conf := fmt.Sprintf("[General]\nPrivacy=%s\n\n[Policy]\nAutoEnable=true\n", privacy)
	return os.WriteFile(path, []byte(conf), 0o644)
}

// waitForSocket polls until a socket exists at path, or returns an
// error after busTimeout. A regular file at the path does not count.
func waitForSocket(path string) error {
	deadline := time.Now().Add(busTimeout)
	for {
		info, err := os.Stat(path)
		if err == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("dbus-daemon created no socket at %s within %s", path, busTimeout)
		}
		time.Sleep(busPoll)
	}
}
