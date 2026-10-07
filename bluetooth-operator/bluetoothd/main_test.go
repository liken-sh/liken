package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
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
	if want := "[General]\nClassicBondedOnly=true\nUserspaceHID=false\n"; string(contents) != want {
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
	if want := "[General]\nClassicBondedOnly=false\nUserspaceHID=false\n"; string(contents) != want {
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

// dbus-daemon binds the socket a moment after it starts, and the wait
// returns at the first poll that finds it.
func TestWaitForSocketReturnsOnceTheSocketIsBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "system_bus_socket")
		go func() {
			time.Sleep(10*busPoll - busPoll/2)
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Errorf("listening: %v", err)
				return
			}
			t.Cleanup(func() { listener.Close() })
		}()
		start := time.Now()

		if err := waitForSocket(path); err != nil {
			t.Errorf("waitForSocket: %v", err)
		}
		if waited := time.Since(start); waited != 10*busPoll {
			t.Errorf("the wait took %s, want %s", waited, 10*busPoll)
		}
	})
}

// The wait gives up one poll after its timeout at the latest, for a
// socket that never appears, and for a leftover plain file at that
// path. dbus-daemon unlinks and recreates the socket at every start, so
// a plain file is a bus that is not listening.
func TestWaitForSocketGivesUp(t *testing.T) {
	cases := []struct {
		name  string
		decoy bool
	}{
		{name: "nothing binds"},
		{name: "a plain file", decoy: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "system_bus_socket")
				if c.decoy {
					if err := os.WriteFile(path, nil, 0o644); err != nil {
						t.Fatalf("writing the decoy: %v", err)
					}
				}
				start := time.Now()

				if err := waitForSocket(path); err == nil {
					t.Fatal("waitForSocket answered a bus that is not listening")
				}
				if waited := time.Since(start); waited <= busTimeout || waited > busTimeout+busPoll {
					t.Errorf("the wait gave up after %s, want more than %s and at most %s", waited, busTimeout, busTimeout+busPoll)
				}
			})
		})
	}
}

// writeMainConfFrom writes a settings file that holds value, runs
// writeMainConf on it, and answers the main.conf it wrote.
func writeMainConfFrom(t *testing.T, value string) (string, error) {
	t.Helper()
	directory := t.TempDir()
	settings := filepath.Join(directory, "privacy")
	if err := os.WriteFile(settings, []byte(value), 0o644); err != nil {
		t.Fatalf("writing the settings file: %v", err)
	}
	path := filepath.Join(directory, "main.conf")
	if err := writeMainConf(path, settings); err != nil {
		return "", err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	return string(contents), nil
}

// Each of the five values of BlueZ's Privacy key reaches main.conf as
// it is spelled, beside the AutoEnable that every start needs.
func TestWriteMainConfWritesEachPrivacyValue(t *testing.T) {
	for _, value := range []string{"off", "network", "device", "limited-network", "limited-device"} {
		t.Run(value, func(t *testing.T) {
			contents, err := writeMainConfFrom(t, value+"\n")
			if err != nil {
				t.Fatalf("writeMainConf: %v", err)
			}
			want := "[General]\nPrivacy=" + value + "\n\n[Policy]\nAutoEnable=true\n"
			if contents != want {
				t.Errorf("main.conf = %q, want %q", contents, want)
			}
		})
	}
}

// A pod with no settings volume has no file, and starts with privacy
// off, which is BlueZ's own default.
func TestWriteMainConfWritesOffWithNoSettingsFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "main.conf")

	if err := writeMainConf(path, filepath.Join(directory, "privacy")); err != nil {
		t.Fatalf("writeMainConf: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if want := "[General]\nPrivacy=off\n\n[Policy]\nAutoEnable=true\n"; string(contents) != want {
		t.Errorf("main.conf = %q, want %q", contents, want)
	}
}

// BlueZ reads a Privacy value it does not recognize as off, so a value
// outside the five stops the container before it reaches main.conf.
func TestWriteMainConfRefusesAnyOtherValue(t *testing.T) {
	for _, value := range []string{"", "on", "Device", "true", "limited"} {
		t.Run(value, func(t *testing.T) {
			if _, err := writeMainConfFrom(t, value); err == nil {
				t.Errorf("writeMainConf accepted %q", value)
			}
		})
	}
}
