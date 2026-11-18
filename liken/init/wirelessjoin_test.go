package main

// These tests run joinWireless against a fake wpa_supplicant on a real
// control socket, with the process start scripted.

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// wirelessJoinedLine is the event wpa_supplicant sends when it has
// joined the network.
const wirelessJoinedLine = "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]"

// wirelessSupplicantAt listens on socket, replies OK to ATTACH, and
// then sends each event to the client that attached.
func wirelessSupplicantAt(t *testing.T, socket string, events ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, wpaMessageMax)
		for {
			n, from, err := conn.ReadFromUnix(buf)
			if err != nil {
				return
			}
			if string(buf[:n]) != "ATTACH" {
				continue
			}
			_, _ = conn.WriteToUnix([]byte("OK\n"), from)
			for _, event := range events {
				_, _ = conn.WriteToUnix([]byte(event), from)
			}
		}
	}()
}

// wirelessStopsSupplicantsAtEnd stops the supervised supplicants when
// the test ends, and then resets the state that stopSupplicants
// leaves, so the next test can start supplicants.
func wirelessStopsSupplicantsAtEnd(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		stopSupplicants()
		supplicantsMu.Lock()
		supplicantsStopping = false
		supplicants = nil
		supplicantsMu.Unlock()
	})
}

// A radio with a passphrase gets a configuration that only root can
// read, a supplicant that the shutdown stops, and the Connected state.
func TestJoinWirelessJoinsThroughASupervisedSupplicant(t *testing.T) {
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "homenet", "correcthorse\n")
	run := useWirelessRunDir(t)
	starts := scriptStarts(t, 0)
	wirelessStopsSupplicantsAtEnd(t)
	wirelessSupplicantAt(t, filepath.Join(controlSocketDir("wlan0"), "wlan0"), wirelessJoinedLine)

	r := joinWireless(declaredRadio("wlan0", "homenet"), stateRoot)
	t.Cleanup(r.control.close)

	if r.state != machine.WirelessConnected {
		t.Errorf("the state is %q with message %q, want %q", r.state, r.message, machine.WirelessConnected)
	}
	if got := starts.count(); got != 1 {
		t.Errorf("%d supplicants started, want 1", got)
	}
	if got := len(trackedSupplicants()); got != 1 {
		t.Errorf("the shutdown would stop %d supplicants, want 1", got)
	}
	config := filepath.Join(run, "wlan0", "wpa_supplicant.conf")
	info, err := os.Stat(config)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || !strings.Contains(readFile(t, config), "sae_password=") {
		t.Errorf("the config has mode %v and reads:\n%s\nwant mode 0600 and a sae_password line", info.Mode().Perm(), readFile(t, config))
	}
}

// A radio with no passphrase cannot join, so it gets WrongKey, a state
// that can make the boot wait, and no supplicant starts.
func TestJoinWirelessWithNoPassphraseIsAWrongKeyAndStartsNothing(t *testing.T) {
	stateRoot := passphraseFiles(t)
	useWirelessRunDir(t)
	starts := scriptStarts(t, 0)

	r := joinWireless(declaredRadio("wlan0", "homenet"), stateRoot)

	if r.state != machine.WirelessWrongKey || !r.deterministic() {
		t.Errorf("the state is %q, want %q and a deterministic failure", r.state, machine.WirelessWrongKey)
	}
	if !strings.Contains(r.message, `no passphrase for "homenet"`) {
		t.Errorf("the message is %q, want it to contain no passphrase for \"homenet\"", r.message)
	}
	if got := starts.count(); got != 0 {
		t.Errorf("%d supplicants started for a radio that cannot join, want none", got)
	}
}

// A failure to start the supplicant says nothing about the passphrase,
// so the radio gets NoCarrier, which never makes the boot wait.
func TestJoinWirelessThatCannotStartTheSupplicantIsNoCarrier(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prepare  func(t *testing.T, run string)
		failures int
		want     string
	}{
		{
			name: "the run directory is a file",
			prepare: func(t *testing.T, run string) {
				file := filepath.Join(run, "file")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				wirelessRunDir = file
			},
			want: "preparing",
		},
		{
			name: "the configuration path is a directory",
			prepare: func(t *testing.T, run string) {
				if err := os.MkdirAll(filepath.Join(run, "wlan0", "wpa_supplicant.conf"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "writing",
		},
		{
			name:     "the supplicant does not start",
			prepare:  func(*testing.T, string) {},
			failures: 1,
			want:     "starting the supplicant on wlan0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateRoot := passphraseFiles(t)
			writePassphrase(t, imagePassphraseDir, "homenet", "correcthorse\n")
			run := useWirelessRunDir(t)
			starts := scriptStarts(t, tc.failures)
			tc.prepare(t, run)

			r := joinWireless(declaredRadio("wlan0", "homenet"), stateRoot)

			if r.state != machine.WirelessNoCarrier || r.deterministic() {
				t.Errorf("the state is %q, want %q and a retryable failure", r.state, machine.WirelessNoCarrier)
			}
			if !strings.Contains(r.message, tc.want) {
				t.Errorf("the message is %q, want it to contain %q", r.message, tc.want)
			}
			if got := starts.count(); got != tc.failures {
				t.Errorf("%d starts, want %d", got, tc.failures)
			}
			if got := len(trackedSupplicants()); got != 0 {
				t.Errorf("%d supplicants that never ran are tracked for the shutdown, want none", got)
			}
		})
	}
}
