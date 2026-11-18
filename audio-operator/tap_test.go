package main

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// countingReader is an endless source of samples that counts the bytes
// it has returned, so a test can see whether the pump kept reading. The
// pump reads it from its own goroutine, so the count is atomic.
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
	// If the pump did not read while confirm runs, pw-record would block
	// on a full pipe and the time that confirm takes would delay sample
	// zero.
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

	buffer := make([]byte, 16)
	if _, err := io.ReadFull(body, buffer); err != nil {
		t.Fatalf("reading the body after the link was confirmed: %v", err)
	}
}

func TestTheDiscardIsAClockAndNotAByteCount(t *testing.T) {
	// A discard that counted bytes would start the body as soon as begin's
	// worth of samples had gone by, however long the pipeline had been
	// running. The body must start when the clock reaches begin.
	samples := &countingReader{}
	at := time.Unix(1789000000, 0)
	// The pump reads the clock from its own goroutine while this test
	// moves it, so the time is stored atomically.
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

	// The pump has dropped far more than five seconds of samples, but the
	// clock has not moved, so no byte is delivered.
	waitFor(t, func() bool { return pump.discarded() > 5*48000*2*2 })
	select {
	case <-readByte(body):
		t.Fatal("the body delivered a byte before the clock reached begin, want nothing until then")
	case <-time.After(50 * time.Millisecond):
	}

	now.Store(at.Add(6 * time.Second).UnixNano())
	select {
	case err := <-readByte(body):
		if err != nil {
			t.Fatalf("reading the body after the clock passed begin: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the body delivered nothing within a second of begin, want a byte")
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
		t.Fatalf("reading the bounded body: %v", err)
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
	buffer := make([]byte, 1<<20)
	if _, err := io.ReadFull(body, buffer); err != nil {
		t.Fatalf("reading 1 MiB of an open span: %v, want the span still open", err)
	}
}

func readByte(from io.Reader) <-chan error {
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
	t.Fatal("the pump did not reach the state this test waits for within 2 seconds")
}

// lateReader delays the first Read of a body. On a busy node a short
// span can be encoded, and the encoder can exit, before the response
// reads the first block. The delay makes every tap run in that order.
type lateReader struct {
	from    io.ReadCloser
	delay   time.Duration
	delayed bool
}

func (l *lateReader) Read(into []byte) (int, error) {
	if !l.delayed {
		l.delayed = true
		time.Sleep(l.delay)
	}
	return l.from.Read(into)
}

func (l *lateReader) Close() error { return l.from.Close() }

func (h *captureHarness) readsLate(delay time.Duration) {
	start := h.server.start
	h.server.start = func(ctx context.Context, plan tapPlan) (*runningTap, error) {
		tap, err := start(ctx, plan)
		if err != nil {
			return nil, err
		}
		tap.Body = &lateReader{from: tap.Body, delay: delay}
		return tap, nil
	}
}

// The response must read all of the encoder's output after the encoder
// exits. If the pipe closed at exit, the client would get a 200 with an
// empty body.
func TestAnEncoderThatExitsBeforeTheBodyIsReadDeliversEveryByte(t *testing.T) {
	harness := newCaptureHarness(t, "graph.json", silence(0.25, 48000, 2))
	harness.readsLate(250 * time.Millisecond)
	answer := harness.call(t, "GET",
		"/v1/audio/sinks/liken-1-usb-0573-1573-a34004801402-usb-audio/audio.flac?t=0,0.125")
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	// The fake encoder's four-byte marker, then 0.125 s of samples at
	// 48,000 Hz, two channels, and two bytes per sample.
	if len(body) != 4+24000 || !strings.HasPrefix(string(body), "fLaC") {
		t.Errorf("the body is %d bytes and starts %q, want fLaC and 24000 bytes",
			len(body), body[:min(len(body), 4)])
	}
}

// flac and opusenc print a banner and a progress bar to stderr and exit
// 0 on every successful tap. If stderr counted as a failure, every FLAC
// and Opus tap that returned correct audio would be counted as an
// encoder failure.
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

// A body is not cut just because a process was still being stopped when
// the last bytes went out. A recorder that this container signals, or
// that a pipe close kills, does not show that the client received less
// than it asked for.
func TestABodyIsCutOnlyWhenItCarriesLessThanItAskedFor(t *testing.T) {
	cases := []struct {
		name          string
		delivered     bool
		left          bool
		encoderFailed bool
		cut           bool
	}{
		{"a span that ran out", true, false, false, false},
		{"a span that ran out under a failing encoder", true, false, true, true},
		{"a client that left", false, true, false, false},
		{"a pipeline that died under a client that stayed", false, false, false, true},
		{"a pipeline that died under a failing encoder", false, false, true, true},
	}
	for _, row := range cases {
		if got := bodyCut(row.delivered, row.left, row.encoderFailed); got != row.cut {
			t.Errorf("%s reads as cut=%v, want %v", row.name, got, row.cut)
		}
	}
}

// A client leaving shows up in the copy as a write error or, when the
// read side fails first, as a done request context. Either signal
// alone can miss it, so endedByClient checks both.
func TestTheRequestContextNamesTheDepartureTheWriteErrorMissed(t *testing.T) {
	if !endedByClient(errClientGone, false) {
		t.Error("endedByClient(errClientGone, false) = false, want true")
	}
	if !endedByClient(errors.New("the sample pipe was closed"), true) {
		t.Error("endedByClient for a read error with the request done = false, want true")
	}
	if endedByClient(errors.New("the sample pipe was closed"), false) {
		t.Error("endedByClient for a read error with the request still live = true, want false")
	}
}

func TestTheLoggedEndCarriesTheExitStatusAndOneLine(t *testing.T) {
	ended := tapExit{Recorder: -1, Encoder: 0, LastLine: "the last line it printed"}
	got := ended.String()
	for _, want := range []string{"pw-record:signalled", "encoder:0", "the last line it printed"} {
		if !strings.Contains(got, want) {
			t.Errorf("the ended field is %q, want it to contain %s", got, want)
		}
	}
	// A WAV tap runs no encoder, so there is no status to report.
	if got := (tapExit{Recorder: -1, Encoder: exitNotRun}).String(); strings.Contains(got, "encoder:") {
		t.Errorf("a WAV tap reports %q, want no encoder status", got)
	}
}

// flac's banner runs to several lines and opusenc redraws its progress
// bar with a carriage return. Logging all of either would bury the rest
// of the log line, so only the last line is kept.
func TestOnlyTheLastLineOfStderrIsLogged(t *testing.T) {
	banner := "flac 1.5.0\nCopyright (C) 2000-2009 Josh Coalson\n" +
		"flac comes with ABSOLUTELY NO WARRANTY.\n" + flacMD5Warning + "\n"
	if got := lastLine(banner); got != flacMD5Warning {
		t.Errorf("the last line is %q, want %q", got, flacMD5Warning)
	}

	progress := "Encoding using libopus 1.5.2\r[|] 00:00:01.00 1x realtime\r" +
		"[/] 00:00:02.00 1x realtime\r[-] 00:00:03.00 1x realtime"
	if got := lastLine(progress); got != "[-] 00:00:03.00 1x realtime" {
		t.Errorf("the last line of a progress bar is %q, want %q", got, "[-] 00:00:03.00 1x realtime")
	}

	if got := lastLine(""); got != "" {
		t.Errorf("a process that printed nothing reports %q, want none", got)
	}
	if got := lastLine("\n\n  \n"); got != "" {
		t.Errorf("a process that wrote only blanks reports %q, want none", got)
	}

	// A very long line is cut, because a person reads the log.
	long := strings.Repeat("x", 500)
	if got := lastLine(long); len(got) > lastLineMax+3 {
		t.Errorf("a long line logged %d characters, want at most %d", len(got), lastLineMax+3)
	}
}

func TestAProcessThisContainerKilledReportsASignal(t *testing.T) {
	if got := exitStatus(nil); got != 0 {
		t.Errorf("a process that ended cleanly reports %d, want 0", got)
	}
	// A process killed by a signal, as the context does to a tap's
	// processes, gives an ExitError with code -1.
	command := exec.Command("/bin/sh", "-c", "kill -TERM $$")
	if got := exitStatus(command.Run()); got != -1 {
		t.Errorf("a signalled process reports %d, want -1", got)
	}
	failing := exec.Command("/bin/sh", "-c", "exit 3")
	if got := exitStatus(failing.Run()); got != 3 {
		t.Errorf("a process that exited 3 reports %d, want 3", got)
	}
	// An error that is not an ExitError has no status. A process that
	// fails to start is reported by startTap with its stderr, before any
	// exit status is read.
	if got := exitStatus(errors.New("the binary is not there")); got != exitUnknown {
		t.Errorf("an error with no status reports %d, want %d", got, exitUnknown)
	}
}

// confirm reads the graph as soon as the pipeline starts and then waits
// longer between reads. If the first wait were a quarter second, every
// tap whose link PipeWire had not yet built would start its body that
// much later.
func TestTheConfirmationLooksAgainSoonAndThenLessOften(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &captureServer{
			version:  "dev",
			readings: newCaptureMetrics("dev"),
			taps:     make(chan struct{}, 1),
			now:      time.Now,
		}
		var at []time.Duration
		started := time.Now()
		server.graph = func(context.Context) ([]byte, error) {
			at = append(at, time.Since(started))
			return readGraphFixture(t, "graph-no-settings.json"), nil
		}
		if err := server.confirm(t.Context(), drillStream, 48); err == nil {
			t.Fatal("the confirmation returned no error for a graph with no link, want the deadline error")
		}
		// The wait starts at 20 ms and doubles up to 250 ms, and the
		// three-second deadline ends the reads.
		ms := time.Millisecond
		want := []time.Duration{0, 20 * ms, 60 * ms, 140 * ms, 300 * ms, 550 * ms, 800 * ms, 1050 * ms,
			1300 * ms, 1550 * ms, 1800 * ms, 2050 * ms, 2300 * ms, 2550 * ms, 2800 * ms}
		if !slices.Equal(at, want) {
			t.Errorf("the confirmation looked at %v, want %v", at, want)
		}
	})
}

func TestTheConfirmationStopsAtTheFirstLookWhenTheLinkIsThere(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &captureServer{
			version:  "dev",
			readings: newCaptureMetrics("dev"),
			taps:     make(chan struct{}, 1),
			now:      time.Now,
		}
		looks := 0
		server.graph = func(context.Context) ([]byte, error) {
			looks++
			return readGraphFixture(t, "graph.json"), nil
		}
		started := time.Now()
		if err := server.confirm(t.Context(), drillStream, 46); err != nil {
			t.Fatalf("a link on the target did not confirm: %v", err)
		}
		if looks != 1 {
			t.Errorf("the confirmation read the graph %d times for a link already there, want 1", looks)
		}
		// A link that already exists needs no wait.
		if elapsed := time.Since(started); elapsed != 0 {
			t.Errorf("a confirmed link took %s, want no wait", elapsed)
		}
	})
}

// os/exec returns exec.ErrWaitDelay when a process exits normally but its
// pipes are still open afterward. That is not an exit status of the
// process. Counting it as a failure would mark every finished Opus span
// as cut short, and the client would get the whole body followed by an
// HTTP/2 INTERNAL_ERROR.
func TestAWaitThatReturnedNoStatusIsNotAFailure(t *testing.T) {
	if got := exitStatus(exec.ErrWaitDelay); got != exitUnknown {
		t.Errorf("a wait delay reads as %d, want %d", got, exitUnknown)
	}
	if got := exitStatus(errors.New("the wait went wrong somehow")); got != exitUnknown {
		t.Errorf("an error with no status reads as %d, want %d", got, exitUnknown)
	}
	ended := tapExit{Recorder: -1, Encoder: exitUnknown}
	if ended.failed() {
		t.Error("a wait that returned no status reads as failed, want no failure")
	}
	if got := ended.String(); !strings.Contains(got, "encoder:unknown") {
		t.Errorf("the ended field is %q, want it to contain encoder:unknown", got)
	}
}
