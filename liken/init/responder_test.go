package main

// These tests query the responder over a UDP socket on loopback with
// beevik/ntp, the client that time.go uses, so a reply that the tests
// accept is one that a follower accepts.

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
		t.Fatalf("listening on loopback: %v", err)
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
		t.Fatalf("querying the responder: %v", err)
	}
	if err := resp.Validate(); err != nil {
		t.Fatalf("the client rejected our answer: %v", err)
	}
	if resp.Stratum != 4 {
		t.Errorf("the stratum is %d, want 4 from a stratum-3 source", resp.Stratum)
	}
	if offset := resp.ClockOffset.Abs(); offset > 250*time.Millisecond {
		t.Errorf("the clock offset is %v, want at most 250ms on loopback", resp.ClockOffset)
	}
}

func TestResponderAdvertisesFreeRunning(t *testing.T) {
	addr := startResponder(t, newClock(nil))
	resp, err := ntp.Query(addr)
	if err != nil {
		t.Fatalf("querying the responder: %v", err)
	}
	if err := resp.Validate(); err != nil {
		t.Fatalf("a free-running server still serves: %v", err)
	}
	if resp.Stratum != stratumFreeRunning {
		t.Errorf("the stratum is %d, want %d for a free-running server", resp.Stratum, stratumFreeRunning)
	}
}

func TestResponderRefusesToServeBeforeItsFirstSync(t *testing.T) {
	addr := startResponder(t, newClock([]string{"time.example.com"}))
	resp, err := ntp.Query(addr)
	if err != nil {
		t.Fatalf("querying the responder: %v", err)
	}
	if resp.Validate() == nil {
		t.Fatal("the answer of an unsynchronized server validates, want a rejection")
	}
	if resp.KissCode != "INIT" {
		t.Errorf("the kiss code is %q, want INIT", resp.KissCode)
	}
}

func TestRespondToIgnoresGarbage(t *testing.T) {
	clk := syncedClock()
	if respondTo([]byte{1, 2, 3}, clk, time.Now()) != nil {
		t.Error("a 3-byte packet got a reply, want none")
	}
	server := make([]byte, 48)
	server[0] = 4 // mode 4: another server, not a client request
	if respondTo(server, clk, time.Now()) != nil {
		t.Error("a mode 4 packet got a reply, want none")
	}
}

func TestNTPTimestampEncoding(t *testing.T) {
	// Unix time zero is NTP second 2,208,988,800.
	epoch := time.Unix(0, 0)
	if got := ntpTimestamp(epoch); got != 2_208_988_800<<32 {
		t.Errorf("the timestamp of the Unix epoch is %d, want %d", got, uint64(2_208_988_800)<<32)
	}
	// Half a second equals half of 2^32 in the fraction field.
	half := time.Unix(0, 500_000_000)
	want := uint64(2_208_988_800)<<32 | 1<<31
	if got := ntpTimestamp(half); got != want {
		t.Errorf("the timestamp of half a second is %d, want %d", got, want)
	}
}
