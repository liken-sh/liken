package main

import (
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// countingReader delivers as many bytes as it is asked for, and
// records how many it has given out, which is what says whether the
// pipe was drained or left to fill. The pump reads it from a goroutine
// of its own, so the count is atomic.
type countingReader struct{ given atomic.Int64 }

func (c *countingReader) Read(into []byte) (int, error) {
	start := c.given.Add(int64(len(into))) - int64(len(into))
	for index := range into {
		into[index] = byte(start + int64(index))
	}
	return len(into), nil
}

func (c *countingReader) Close() error { return nil }

func TestTheDiscardDrainsWhileTheLinkIsConfirmed(t *testing.T) {
	// The pipe holds about 340 ms at 48 kHz stereo, so a confirmation
	// that read nothing would block pw-record and add its whole time
	// to the shift. The pump reads from the moment the pipeline starts.
	samples := &countingReader{}
	at := time.Unix(1789000000, 0)
	release := make(chan struct{})
	pump := &discardPump{
		from:    samples,
		until:   at,
		now:     func() time.Time { return at },
		release: release,
	}
	body := pump.start()

	waitFor(t, func() bool { return pump.discarded() > 0 })
	close(release)

	held := make([]byte, 16)
	if _, err := io.ReadFull(body, held); err != nil {
		t.Fatalf("the body did not begin once the link was confirmed: %v", err)
	}
}

func TestTheDiscardIsAClockAndNotAByteCount(t *testing.T) {
	// A byte-count discard would deliver as soon as begin's worth of
	// samples had gone by, however long the pipeline had been running.
	// The clock is what decides.
	samples := &countingReader{}
	at := time.Unix(1789000000, 0)
	// The pump reads the clock from its own goroutine while this test
	// moves it, so the instant is held atomically.
	var now atomic.Int64
	now.Store(at.UnixNano())
	release := make(chan struct{})
	close(release)
	pump := &discardPump{
		from:    samples,
		until:   at.Add(5 * time.Second),
		now:     func() time.Time { return time.Unix(0, now.Load()) },
		release: release,
	}
	body := pump.start()

	// Far more than five seconds of samples have gone by, and the
	// clock has not moved, so nothing is delivered.
	waitFor(t, func() bool { return pump.discarded() > 5*48000*2*2 })
	select {
	case <-readOne(body):
		t.Fatal("the body began before the clock reached begin")
	case <-time.After(50 * time.Millisecond):
	}

	now.Store(at.Add(6 * time.Second).UnixNano())
	select {
	case err := <-readOne(body):
		if err != nil {
			t.Fatalf("the body did not begin at begin: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the body never began")
	}
}

func TestASpanWithAnEndBoundsTheBody(t *testing.T) {
	samples := &countingReader{}
	at := time.Unix(1789000000, 0)
	release := make(chan struct{})
	close(release)
	pump := &discardPump{
		from:    samples,
		until:   at,
		now:     func() time.Time { return at },
		release: release,
		limit:   48000,
	}
	delivered, err := io.ReadAll(pump.start())
	if err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 48000 {
		t.Errorf("the body is %d bytes, want the span's 48000", len(delivered))
	}
}

func TestAnOpenSpanDeliversUntilTheClientCloses(t *testing.T) {
	samples := &countingReader{}
	at := time.Unix(1789000000, 0)
	release := make(chan struct{})
	close(release)
	pump := &discardPump{
		from:    samples,
		until:   at,
		now:     func() time.Time { return at },
		release: release,
	}
	body := pump.start()
	held := make([]byte, 1<<20)
	if _, err := io.ReadFull(body, held); err != nil {
		t.Fatalf("an open span ended on its own: %v", err)
	}
}

func readOne(from io.Reader) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := from.Read(make([]byte, 1))
		done <- err
	}()
	return done
}

func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the pump never reached the state this test waits for")
}
