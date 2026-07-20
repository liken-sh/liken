package main

// These tests exercise the time responder the strongest way
// available: over a real UDP socket on loopback, with the same
// vendored client that the machines themselves use. If beevik/ntp
// accepts the response, a follower will accept it too.

import (
	"net"
	"testing"
	"time"

	"github.com/beevik/ntp"
)

// startResponder serves time from the given clock on an ephemeral
// loopback port, and it returns the address to query.
func startResponder(t *testing.T, clk *clock) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go answerTimeQueries(t.Context(), pc, clk)
	return pc.LocalAddr().String()
}

// syncedClock is a clock that measured its source a moment ago.
func syncedClock() *clock {
	clk := newClock([]string{"time.example.com"})
	clk.record(&timeSync{
		source:  "time.example.com",
		stratum: 3,
		offset:  time.Millisecond,
		at:      time.Now(),
	})
	return clk
}

func TestResponderSatisfiesTheRealClient(t *testing.T) {
	addr := startResponder(t, syncedClock())
	resp, err := ntp.Query(addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Validate(); err != nil {
		t.Fatalf("the client rejected our answer: %v", err)
	}
	if resp.Stratum != 4 {
		t.Errorf("a stratum-3 source makes this server stratum 4, got %d", resp.Stratum)
	}
	if offset := resp.ClockOffset.Abs(); offset > 250*time.Millisecond {
		t.Errorf("loopback offset should be near zero, got %v", resp.ClockOffset)
	}
}

func TestResponderAdvertisesFreeRunning(t *testing.T) {
	addr := startResponder(t, newClock(nil))
	resp, err := ntp.Query(addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Validate(); err != nil {
		t.Fatalf("a free-running server still serves: %v", err)
	}
	if resp.Stratum != stratumFreeRunning {
		t.Errorf("free-running serves the local-clock convention, got %d", resp.Stratum)
	}
}

func TestResponderRefusesToServeBeforeItsFirstSync(t *testing.T) {
	addr := startResponder(t, newClock([]string{"time.example.com"}))
	resp, err := ntp.Query(addr)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Validate() == nil {
		t.Fatal("an unsynchronized server's answer must not validate")
	}
	if resp.KissCode != "INIT" {
		t.Errorf("expected the INIT kiss code, got %q", resp.KissCode)
	}
}

func TestRespondToIgnoresGarbage(t *testing.T) {
	clk := syncedClock()
	if respondTo([]byte{1, 2, 3}, clk, time.Now()) != nil {
		t.Error("a short packet deserves silence, not a reply")
	}
	server := make([]byte, 48)
	server[0] = 4 // mode 4: another server, not a client request
	if respondTo(server, clk, time.Now()) != nil {
		t.Error("only client (mode 3) requests get answers")
	}
}

func TestNTPTimestampEncoding(t *testing.T) {
	// The NTP epoch is 1900-01-01. The Unix epoch is 1970-01-01. Unix
	// time zero corresponds to NTP second 2,208,988,800, which is the
	// standard check for this conversion.
	epoch := time.Unix(0, 0)
	if got := ntpTimestamp(epoch); got != 2_208_988_800<<32 {
		t.Errorf("got %d", got)
	}
	// Half a second equals half of 2^32 in the fraction field.
	half := time.Unix(0, 500_000_000)
	want := uint64(2_208_988_800)<<32 | 1<<31
	if got := ntpTimestamp(half); got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}
