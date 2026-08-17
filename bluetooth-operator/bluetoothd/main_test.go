package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBusSocketReadsTheUnixPath(t *testing.T) {
	path, err := busSocket("unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket")
	if err != nil {
		t.Fatalf("busSocket: %v", err)
	}
	if want := "/var/run/bluetooth.liken.sh/dbus/system_bus_socket"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestBusSocketRejectsAddressesItCannotServe(t *testing.T) {
	for _, address := range []string{
		"",
		"unix:path=",
		"unix:abstract=/tmp/bus",
		"tcp:host=localhost,port=1234",
	} {
		t.Run(address, func(t *testing.T) {
			if _, err := busSocket(address); err == nil {
				t.Errorf("busSocket(%q) accepted an address it cannot wait on", address)
			}
		})
	}
}

// The default is the whole point of this file: an unset variable must
// leave CVE-2023-45866 closed.
func TestWriteInputConfDefaultsToBondedOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.conf")
	if err := writeInputConf(path, ""); err != nil {
		t.Fatalf("writeInputConf: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if want := "[General]\nClassicBondedOnly=true\n"; string(contents) != want {
		t.Errorf("input.conf = %q, want %q", contents, want)
	}
}

func TestWriteInputConfWritesFalseWhenAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.conf")
	if err := writeInputConf(path, "false"); err != nil {
		t.Fatalf("writeInputConf: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if want := "[General]\nClassicBondedOnly=false\n"; string(contents) != want {
		t.Errorf("input.conf = %q, want %q", contents, want)
	}
}

// glib reads any unrecognized value as false, so a value this program
// does not recognize must stop the container instead of reaching the
// file.
func TestWriteInputConfRejectsAnythingElse(t *testing.T) {
	for _, value := range []string{"yes", "no", "1", "0", "True", "FALSE"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.conf")
			if err := writeInputConf(path, value); err == nil {
				t.Errorf("writeInputConf accepted %q", value)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("writeInputConf wrote %s for the rejected value %q", path, value)
			}
		})
	}
}

func TestWaitForSocketReturnsOnceTheSocketIsBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system_bus_socket")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	if err := waitForSocket(path, time.Second, time.Millisecond); err != nil {
		t.Errorf("waitForSocket: %v", err)
	}
}

func TestWaitForSocketGivesUpWhenNothingBinds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system_bus_socket")
	if err := waitForSocket(path, 20*time.Millisecond, time.Millisecond); err == nil {
		t.Error("waitForSocket returned success for a socket that never appeared")
	}
}

// dbus-daemon unlinks and recreates the socket at every start, so a
// leftover plain file at that path is a bus that is not listening.
func TestWaitForSocketIgnoresAPlainFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system_bus_socket")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("writing the decoy: %v", err)
	}
	if err := waitForSocket(path, 20*time.Millisecond, time.Millisecond); err == nil {
		t.Error("waitForSocket accepted a plain file as the bus socket")
	}
}
