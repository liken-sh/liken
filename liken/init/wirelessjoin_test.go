package main

// joinWireless is the order of a radio's bring-up: the configuration,
// then the supervised supplicant, then the wait for the association.
// These tests run it end to end against a stand-in supplicant on a
// real control socket, with the process start scripted, and check the
// verdict each failure leaves on the radio.

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// wirelessJoinedLine is the event the supplicant sends when the
// four-way handshake completes.
const wirelessJoinedLine = "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed [id=0]"

// wirelessSupplicantAt answers ATTACH at one socket and then sends
// each event to the client that attached, the order a real supplicant
// uses when it joins right after a client attaches.
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

// wirelessStopsSupplicantsAtEnd runs the shutdown's stop when the test
// ends, so no supervision loop outlives the test, and then reopens the
// list the stop latched.
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

// A radio whose passphrase is on the image must get a configuration
// that only root can read, a supplicant the shutdown can stop, and the
// Connected verdict the supplicant reports.
func TestJoinWirelessJoinsThroughASupervisedSupplicant(t *testing.T) {
	stateRoot := passphraseFiles(t)
	writePassphrase(t, imagePassphraseDir, "homenet", "correcthorse\n")
	run := aimWirelessRunDir(t)
	starts := scriptStarts(t, 0)
	wirelessStopsSupplicantsAtEnd(t)
	wirelessSupplicantAt(t, filepath.Join(controlSocketDir("wlan0"), "wlan0"), wirelessJoinedLine)

	r := joinWireless(declaredRadio("wlan0", "homenet"), stateRoot)
	t.Cleanup(r.control.close)

	if r.state != machine.WirelessConnected {
		t.Errorf("state %q, message %q", r.state, r.message)
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
		t.Errorf("config mode %v:\n%s", info.Mode().Perm(), readFile(t, config))
	}
}

// A radio with no passphrase can never join, however long it waits, so
// it must carry the WrongKey verdict that the park decision acts on,
// and no supplicant may start for it.
func TestJoinWirelessWithNoPassphraseIsAWrongKeyAndStartsNothing(t *testing.T) {
	stateRoot := passphraseFiles(t)
	aimWirelessRunDir(t)
	starts := scriptStarts(t, 0)

	r := joinWireless(declaredRadio("wlan0", "homenet"), stateRoot)

	if r.state != machine.WirelessWrongKey || !r.deterministic() {
		t.Errorf("state %q", r.state)
	}
	if !strings.Contains(r.message, `no passphrase for "homenet"`) {
		t.Errorf("message %q", r.message)
	}
	if got := starts.count(); got != 0 {
		t.Errorf("%d supplicants started for a radio that cannot join", got)
	}
}

// A failure on the machine's side, before or at the supplicant's
// start, says nothing about the passphrase. It must carry NoCarrier,
// which never parks a boot, and the message must name the cause.
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
			run := aimWirelessRunDir(t)
			starts := scriptStarts(t, tc.failures)
			tc.prepare(t, run)

			r := joinWireless(declaredRadio("wlan0", "homenet"), stateRoot)

			if r.state != machine.WirelessNoCarrier || r.deterministic() {
				t.Errorf("state %q", r.state)
			}
			if !strings.Contains(r.message, tc.want) {
				t.Errorf("message %q, want %q", r.message, tc.want)
			}
			if got := starts.count(); got != tc.failures {
				t.Errorf("%d starts, want %d", got, tc.failures)
			}
			if got := len(trackedSupplicants()); got != 0 {
				t.Errorf("the shutdown would stop %d supplicants that never ran", got)
			}
		})
	}
}
