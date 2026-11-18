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
		t.Fatalf("busSocket returned an error for a unix:path= address: %v", err)
	}
	if want := "/var/run/bluetooth.liken.sh/dbus/system_bus_socket"; path != want {
		t.Errorf("busSocket returned %q, want %q", path, want)
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
				t.Errorf("busSocket(%q) returned no error, want one because the address is not unix:path=<path>", address)
			}
		})
	}
}

// An unset variable writes ClassicBondedOnly=true, the fix for
// CVE-2023-45866.
func TestWriteInputConfDefaultsToBondedOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.conf")
	if err := writeInputConf(path, ""); err != nil {
		t.Fatalf("writing input.conf: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the file back: %v", err)
	}
	if want := "[General]\nClassicBondedOnly=true\nUserspaceHID=false\n"; string(contents) != want {
		t.Errorf("input.conf is %q, want %q", contents, want)
	}
}

func TestWriteInputConfWritesFalseWhenAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.conf")
	if err := writeInputConf(path, "false"); err != nil {
		t.Fatalf("writing input.conf: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the file back: %v", err)
	}
	if want := "[General]\nClassicBondedOnly=false\nUserspaceHID=false\n"; string(contents) != want {
		t.Errorf("input.conf is %q, want %q", contents, want)
	}
}

// glib reads an unknown value as false, so writeInputConf refuses it and
// writes no file.
func TestWriteInputConfRejectsAnythingElse(t *testing.T) {
	for _, value := range []string{"yes", "no", "1", "0", "True", "FALSE"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.conf")
			if err := writeInputConf(path, value); err == nil {
				t.Errorf("writeInputConf(%q) returned no error, want one", value)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("writeInputConf(%q) created %s, want no file", value, path)
			}
		})
	}
}

func TestWaitForSocketReturnsOnceTheSocketIsBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "system_bus_socket")
		go func() {
			time.Sleep(10*busPoll - busPoll/2)
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Errorf("listening on the socket: %v", err)
				return
			}
			t.Cleanup(func() { listener.Close() })
		}()
		start := time.Now()

		if err := waitForSocket(path); err != nil {
			t.Errorf("waitForSocket returned an error after the socket appeared: %v", err)
		}
		if waited := time.Since(start); waited != 10*busPoll {
			t.Errorf("waitForSocket returned after %s, want %s", waited, 10*busPoll)
		}
	})
}

// The wait fails within one poll after busTimeout, both when no socket
// appears and when a regular file is at the path.
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
					t.Fatal("waitForSocket returned nil, want an error because nothing listens on the socket")
				}
				if waited := time.Since(start); waited <= busTimeout || waited > busTimeout+busPoll {
					t.Errorf("waitForSocket gave up after %s, want more than %s and at most %s", waited, busTimeout, busTimeout+busPoll)
				}
			})
		})
	}
}

// writeMainConfFrom writes a settings file containing value, runs
// writeMainConf on it, and returns the contents of the main.conf.
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
		t.Fatalf("reading the file back: %v", err)
	}
	return string(contents), nil
}

func TestWriteMainConfWritesEachPrivacyValue(t *testing.T) {
	for _, value := range []string{"off", "network", "device", "limited-network", "limited-device"} {
		t.Run(value, func(t *testing.T) {
			contents, err := writeMainConfFrom(t, value+"\n")
			if err != nil {
				t.Fatalf("writing main.conf: %v", err)
			}
			want := "[General]\nPrivacy=" + value + "\n\n[Policy]\nAutoEnable=true\n"
			if contents != want {
				t.Errorf("main.conf is %q, want %q", contents, want)
			}
		})
	}
}

func TestWriteMainConfWritesOffWithNoSettingsFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "main.conf")

	if err := writeMainConf(path, filepath.Join(directory, "privacy")); err != nil {
		t.Fatalf("writing main.conf: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the file back: %v", err)
	}
	if want := "[General]\nPrivacy=off\n\n[Policy]\nAutoEnable=true\n"; string(contents) != want {
		t.Errorf("main.conf is %q, want %q", contents, want)
	}
}

// BlueZ reads an unknown Privacy value as off, so writeMainConf refuses
// it.
func TestWriteMainConfRefusesAnyOtherValue(t *testing.T) {
	for _, value := range []string{"", "on", "Device", "true", "limited"} {
		t.Run(value, func(t *testing.T) {
			if _, err := writeMainConfFrom(t, value); err == nil {
				t.Errorf("writeMainConf accepted the privacy value %q, want an error", value)
			}
		})
	}
}
