package main

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
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

// Both encoders write a banner and a progress bar to stderr and exit
// zero on every successful tap, so stderr alone says nothing about
// whether a tap worked. The drill counted one encoder failure for
// every FLAC and every Opus tap that returned correct audio.
func TestAnEncoderThatExitedZeroIsNoFailure(t *testing.T) {
	cases := []struct {
		name   string
		exit   tapExit
		failed bool
	}{
		{"a WAV tap the client closed", tapExit{Recorder: -1, Encoder: exitNotRun}, false},
		{"a FLAC tap that finished its span", tapExit{Recorder: -1, Encoder: 0}, false},
		{"an Opus tap this container signalled", tapExit{Recorder: -1, Encoder: -1}, false},
		{"a tap that ran to the end of its input", tapExit{Recorder: 0, Encoder: 0}, false},
		{"an encoder that failed on its own", tapExit{Recorder: -1, Encoder: 1}, true},
		{"pw-record that failed on its own", tapExit{Recorder: 1, Encoder: -1}, true},
	}
	for _, row := range cases {
		if row.exit.failed() != row.failed {
			t.Errorf("%s reads as failed=%v, want %v", row.name, row.exit.failed(), row.failed)
		}
	}
}

func TestTheLoggedEndCarriesTheExitStatusAndOneLine(t *testing.T) {
	ended := tapExit{Recorder: -1, Encoder: 0, Words: "the last thing it said"}
	got := ended.String()
	for _, want := range []string{"pw-record:signalled", "encoder:0", "the last thing it said"} {
		if !strings.Contains(got, want) {
			t.Errorf("the ended field is %q, which carries no %s", got, want)
		}
	}
	// A WAV tap runs no encoder, so there is no status to report.
	if got := (tapExit{Recorder: -1, Encoder: exitNotRun}).String(); strings.Contains(got, "encoder:") {
		t.Errorf("a WAV tap reports an encoder: %q", got)
	}
}

// flac's banner runs to several lines and opusenc's progress bar
// returns the carriage rather than the line, so the whole of either
// one in a log line buries everything else on it.
func TestOnlyTheLastLineOfStderrIsLogged(t *testing.T) {
	banner := "flac 1.5.0\nCopyright (C) 2000-2009 Josh Coalson\n" +
		"flac comes with ABSOLUTELY NO WARRANTY.\n" + flacMD5Warning + "\n"
	if got := lastLine(banner); got != flacMD5Warning {
		t.Errorf("the last line is %q, want %q", got, flacMD5Warning)
	}

	progress := "Encoding using libopus 1.5.2\r[|] 00:00:01.00 1x realtime\r" +
		"[/] 00:00:02.00 1x realtime\r[-] 00:00:03.00 1x realtime"
	if got := lastLine(progress); got != "[-] 00:00:03.00 1x realtime" {
		t.Errorf("the last line of a progress bar is %q", got)
	}

	if got := lastLine(""); got != "" {
		t.Errorf("a process that said nothing reports %q", got)
	}
	if got := lastLine("\n\n  \n"); got != "" {
		t.Errorf("a process that wrote only blanks reports %q", got)
	}

	// A line of any length is bounded, because a person reads this.
	long := strings.Repeat("x", 500)
	if got := lastLine(long); len(got) > lastLineMax+3 {
		t.Errorf("a long line logged %d characters", len(got))
	}
}

func TestAProcessThisContainerKilledReportsASignal(t *testing.T) {
	if got := exitStatus(nil); got != 0 {
		t.Errorf("a process that ended cleanly reports %d", got)
	}
	// A context that cancelled the command leaves an ExitError whose
	// code is -1, which is what every finished tap looks like.
	command := exec.Command("/bin/sh", "-c", "kill -TERM $$")
	if got := exitStatus(command.Run()); got != -1 {
		t.Errorf("a signalled process reports %d, want -1", got)
	}
	failing := exec.Command("/bin/sh", "-c", "exit 3")
	if got := exitStatus(failing.Run()); got != 3 {
		t.Errorf("a process that exited 3 reports %d", got)
	}
	if got := exitStatus(errors.New("the binary is not there")); got != 1 {
		t.Errorf("a process that never started reports %d", got)
	}
}

// The confirmation looks as soon as the pipeline starts and backs off
// from there. A quarter-second interval on the first look was adding
// itself to the time to the first body byte on every tap whose link
// PipeWire had not built in the moment before it.
func TestTheConfirmationLooksAgainSoonAndThenLessOften(t *testing.T) {
	server := &captureServer{
		version:      "dev",
		readings:     newCaptureMetrics("dev"),
		taps:         make(chan struct{}, 1),
		now:          time.Now,
		linkDeadline: 400 * time.Millisecond,
	}
	looks := 0
	var at []time.Duration
	started := time.Now()
	server.graph = func(context.Context) ([]byte, error) {
		looks++
		at = append(at, time.Since(started))
		return readGraphFixture(t, "graph-no-settings.json"), nil
	}
	if err := server.confirm(context.Background(), drillStream, 48); err == nil {
		t.Fatal("a graph with no link confirmed")
	}
	if looks < 4 {
		t.Errorf("the confirmation looked %d times in %s", looks, 400*time.Millisecond)
	}
	// The second look follows the first closely, rather than a quarter
	// of a second later.
	if len(at) > 1 && at[1] > 100*time.Millisecond {
		t.Errorf("the second look came %s in, which is too late to help", at[1])
	}
}

func TestTheConfirmationStopsAtTheFirstLookWhenTheLinkIsThere(t *testing.T) {
	server := &captureServer{
		version:      "dev",
		readings:     newCaptureMetrics("dev"),
		taps:         make(chan struct{}, 1),
		now:          time.Now,
		linkDeadline: time.Second,
	}
	looks := 0
	server.graph = func(context.Context) ([]byte, error) {
		looks++
		return readGraphFixture(t, "graph.json"), nil
	}
	started := time.Now()
	if err := server.confirm(context.Background(), drillStream, 46); err != nil {
		t.Fatalf("a link on the target did not confirm: %v", err)
	}
	if looks != 1 {
		t.Errorf("the confirmation read the graph %d times for a link already there", looks)
	}
	// A tap whose link is already built waits for nothing.
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Errorf("a confirmed link took %s", elapsed)
	}
}
