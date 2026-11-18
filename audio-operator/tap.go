package main

// A tap starts pw-record and an optional encoder, confirms that PipeWire
// linked the recording stream to the requested node, and copies the
// audio to the HTTP response.
//
// pw-record does not fail on a bad target, so the handler resolves the
// target node before any process starts. After the pipeline starts,
// confirm reads the graph until the link appears, and only then does
// stream write the 200, because a 500 cannot be sent after a 200. When
// confirm fails, the body stays empty.
//
// The span's begin and end apply to the raw samples, ahead of any
// encoder, so the encoder reads only the requested span. discardPump
// drops samples until this container's clock reaches the accept time
// plus begin, so it compares times and does not count bytes. The body
// is limited to the span's duration in bytes.
//
// discardPump reads from the moment pw-record starts, so the pipe drains
// while confirm runs. Otherwise pw-record would block on a full pipe
// after about 340 ms at 48 kHz stereo, and the time that confirm takes
// would add to the delay between the accept time and sample zero.
//
// When the pipeline ends before the span does, or an encoder fails, the
// response ends without its terminating chunk, which is how HTTP/1.1
// signals an incomplete message (RFC 9112 section 8). The log line
// includes the last line of the encoder's stderr.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// linkDeadline is how long confirm waits for PipeWire to build the link
// before it reports a wrong target.
//
// Each read of the graph runs a pw-dump process. The wait between reads
// starts at linkFirstPeriod and doubles up to linkPeriod, so a quick
// link adds little delay and a slow link does not start a pw-dump every
// 20 ms. PipeWire usually builds the link within a few tens of
// milliseconds, so a fixed wait of linkPeriod would add up to that much
// delay to every tap.
const (
	linkDeadline    = 3 * time.Second
	linkFirstPeriod = 20 * time.Millisecond
	linkPeriod      = 250 * time.Millisecond
)

// discardBlock is the size of each read in discardPump's loop. The loop
// checks the clock between reads, so it can drop up to one block past
// until: 2048 bytes is about 10.7 ms of 16-bit stereo at 48 kHz.
const discardBlock = 2048

// ErrGraphUnread wraps a failure of pw-dump, such as when PipeWire is
// down. The tap's link is then unknown and PipeWire can come back, so
// the handler and stream return a 503 with Retry-After, not the 500 of
// a wrong target. The error text includes pw-dump's stderr.
var ErrGraphUnread = errors.New("pw-dump cannot read the PipeWire graph")

// tapPlan is the target, format, and span of one tap, resolved from the
// request before any process starts.
type tapPlan struct {
	Route     apiRoute
	Node      string
	Direction pwDirection
	Form      representation
	Format    captureFormat
	Query     captureQuery
	RequestID string

	// Stream is the node.name that pw-record's own node gets in the
	// graph. confirm looks for the link from this node.
	Stream string

	// Until is the time of sample zero of the body: the accept time plus
	// begin. stream closes Release after confirm succeeds, and the pump
	// drops samples until Until has passed and Release is closed.
	Until   time.Time
	Now     func() time.Time
	Release <-chan struct{}
}

// stderrBuffer collects what one process prints on stderr. os/exec
// writes to it from its own goroutine while the tap runs, and a failure
// path can read it at the same time, so both go through the lock.
type stderrBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (s *stderrBuffer) Write(block []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.Write(block)
}

func (s *stderrBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.String()
}

// runningTap is a started pipeline. Body is the audio to send to the
// client. stop ends the processes and returns how they exited, and with
// drain it first waits up to encoderDrain for the encoder to exit.
type runningTap struct {
	Body io.ReadCloser

	stop     func(drain bool) tapExit
	pump     *discardPump
	recorded *stderrBuffer
	encoded  *stderrBuffer
}

func (t *runningTap) discarded() int64 {
	if t.pump == nil {
		return 0
	}
	return t.pump.discarded()
}

// delivered reports whether the tap copied the whole of a span that has
// an end. A span with no end is never delivered, because it ends when the
// client leaves.
func (t *runningTap) delivered() bool {
	return t.pump != nil && t.pump.delivered()
}

// tapExit records how the processes of one tap ended: the exit status of
// each, and the last stderr line of the encoder, or of pw-record when the
// encoder printed none.
//
// An exit status of -1 means a signal killed the process. The context
// kills the processes at the end of every tap, so -1 is normal. Only a
// status above zero is a process that failed on its own.
type tapExit struct {
	Recorder int
	Encoder  int
	LastLine string
}

// failed reports whether either process exited by itself with a status
// above zero. Stderr cannot be used for this, because flac and opusenc
// print a banner and a progress bar and exit 0 on every successful tap.
func (e tapExit) failed() bool {
	return e.Recorder > 0 || e.Encoder > 0
}

// encoderFailed reports whether the encoder exited by itself with a
// status above zero. An encoder can consume every sample of a span and
// still fail to write its last frame, so a fully copied body can still be
// invalid. The recorder's status is not checked here. A recorder that
// died before the end already leaves the span undelivered, and one that
// died after the end cannot take back bytes that the client has.
func (e tapExit) encoderFailed() bool {
	return e.Encoder > 0
}

func (e tapExit) String() string {
	ended := fmt.Sprintf("pw-record:%s", exitWords(e.Recorder))
	if e.Encoder != exitNotRun {
		ended += fmt.Sprintf(" encoder:%s", exitWords(e.Encoder))
	}
	if e.LastLine != "" {
		ended += " " + e.LastLine
	}
	return ended
}

// exitNotRun is the encoder status of a WAV tap, which starts no
// encoder. exitUnknown is the status of a Wait error that is not an
// ExitError, such as exec.ErrWaitDelay.
const (
	exitNotRun  = -2
	exitUnknown = -3
)

// encoderDrain is how long stop waits for an encoder to exit by itself
// after its stdin closed at the end of the span. The encoder only has its
// last bytes to write, so a second is far more than it needs. When the
// wait runs out, stop cancels the context so a tap cannot hang.
const encoderDrain = time.Second

func exitWords(status int) string {
	switch status {
	case exitNotRun:
		return "none"
	case exitUnknown:
		return "unknown"
	case -1:
		return "signalled"
	default:
		return strconv.Itoa(status)
	}
}

// lastLine returns the last non-blank line of a process's stderr, cut to
// lastLineMax. It splits on a carriage return as well as a newline,
// because opusenc redraws its progress bar with a carriage return and
// flac prints a multi-line banner before the line that matters.
func lastLine(output string) string {
	last := ""
	for _, line := range strings.FieldsFunc(output, func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			last = trimmed
		}
	}
	if len(last) > lastLineMax {
		return last[:lastLineMax] + "..."
	}
	return last
}

// lastLineMax limits the length of the stderr line in the log line,
// because a process can print a line of any length.
const lastLineMax = 200

// startTap starts pw-record, and an encoder when the form needs one. A
// discardPump reads pw-record's output, drops samples until plan.Until
// has passed and plan.Release is closed, and passes the rest to the
// encoder, or to Body for a WAV tap.
func startTap(ctx context.Context, plan tapPlan) (*runningTap, error) {
	ctx, cancel := context.WithCancel(ctx)
	record := commandOf(ctx, recordCommand(plan.Node, plan.Stream, plan.Direction, plan.Format))
	recorded := &stderrBuffer{}
	record.Stderr = recorded
	raw, err := record.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("reading pw-record's output: %w", err)
	}
	if err := record.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("running pw-record: %w: %s", err, unexpectedStderr(recorded.String()))
	}

	tap := &runningTap{
		recorded: recorded,
		encoded:  &stderrBuffer{},
	}

	span := plan.Query.Span
	pump := &discardPump{
		from:    raw,
		until:   plan.Until,
		now:     plan.Now,
		release: plan.Release,
		limit:   span.bodyBytes(plan.Format.Rate, plan.Format.Channels),
	}
	tap.pump = pump
	samples := pump.start()

	encoderArgs := encoderCommand(plan.Form, plan.Format, plan.Query.Bitrate)
	if encoderArgs == nil {
		tap.Body = readCloser{Reader: samples, close: raw.Close}
		tap.stop = func(bool) tapExit {
			cancel()
			return tapExit{
				Recorder: exitStatus(waitForRecorder(record, raw)),
				Encoder:  exitNotRun,
				LastLine: lastLine(recorded.String()),
			}
		}
		return tap, nil
	}

	encoder := commandOf(ctx, encoderArgs)
	encoder.Env = encoderEnvironment()
	encoder.Stdin = samples
	encoder.Stderr = tap.encoded
	encoded, err := encoder.StdoutPipe()
	if err != nil {
		cancel()
		_ = record.Wait()
		return nil, fmt.Errorf("reading %s's output: %w", encoderArgs[0], err)
	}
	if err := encoder.Start(); err != nil {
		cancel()
		_ = record.Wait()
		return nil, fmt.Errorf("running %s: %w: %s", encoderArgs[0], err,
			unexpectedStderr(tap.encoded.String()))
	}
	tap.Body = readCloser{Reader: encoded, close: encoded.Close}
	tap.stop = func(drain bool) tapExit {
		// Call encoder.Wait here, after the response has stopped reading,
		// and not right after Start. Wait closes the encoder's stdout
		// pipe once the process exits, which discards output that the
		// response has not read yet. A short span can finish encoding
		// before the first read, so an early Wait would send a 200 with
		// an empty body.
		waited := make(chan error, 1)
		go func() { waited <- encoder.Wait() }()
		ended := tapExit{}
		// When the span ran out, the pump closed the encoder's stdin,
		// so the encoder is already writing its last bytes and
		// exiting. Waiting for that exit gives the encoder's own
		// status. Cancelling first would leave os/exec waiting on
		// pipes that are about to close, and Wait would return
		// ErrWaitDelay instead of the 0 the encoder exited with.
		if drain {
			select {
			case err := <-waited:
				ended.Encoder = exitStatus(err)
			case <-time.After(encoderDrain):
				cancel()
				ended.Encoder = exitStatus(<-waited)
			}
		} else {
			cancel()
			ended.Encoder = exitStatus(<-waited)
		}
		cancel()
		ended.Recorder = exitStatus(waitForRecorder(record, raw))
		ended.LastLine = lastLine(tap.encoded.String())
		if ended.LastLine == "" {
			ended.LastLine = lastLine(recorded.String())
		}
		return ended
	}
	return tap, nil
}

// exitStatus returns the exit code of a process, which is -1 when a
// signal killed it. An error that is not an ExitError returns
// exitUnknown. os/exec returns exec.ErrWaitDelay when a process exits
// normally but its pipes stay open past WaitDelay, and counting that as
// a failure would mark every finished Opus span as cut short.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ended *exec.ExitError
	if errors.As(err, &ended) {
		return ended.ExitCode()
	}
	return exitUnknown
}

// waitForRecorder waits for pw-record after the context has killed it.
// It closes the sample pipe first, because os/exec waits for WaitDelay
// when a pipe is still open after the process exits. Without the close,
// every tap would end one second after its last block.
func waitForRecorder(record *exec.Cmd, samples io.ReadCloser) error {
	_ = samples.Close()
	return record.Wait()
}

// commandOf sets WaitDelay, so after the context kills the process, Wait
// waits at most a second for its output pipes to close.
func commandOf(ctx context.Context, args []string) *exec.Cmd {
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.WaitDelay = time.Second
	return command
}

// discardPump reads pw-record's output from the moment the process
// starts. It drops samples until the clock reaches until and release is
// closed, then passes the rest on through a pipe, up to limit bytes when
// limit is above zero.
//
// The pipe lets the pump run ahead of the response. The response is the
// reader of the body, and it does not exist while the link is being
// confirmed. With a plain reader, nothing would read pw-record's output
// in that time and pw-record would block on a full pipe.
type discardPump struct {
	from    io.ReadCloser
	until   time.Time
	now     func() time.Time
	release <-chan struct{}
	limit   int64

	// dropped counts the bytes discarded. The pump's goroutine writes it
	// and logTap reads it when the tap ends, so it is atomic.
	dropped atomic.Int64

	// reachedLimit is atomic for the same reason as dropped.
	reachedLimit atomic.Bool
}

func (p *discardPump) start() io.Reader {
	reader, writer := io.Pipe()
	go func() {
		block := make([]byte, discardBlock)
		for !p.ready() {
			read, err := p.from.Read(block)
			p.dropped.Add(int64(read))
			if err != nil {
				_ = writer.CloseWithError(err)
				return
			}
		}
		var samples io.Reader = p.from
		if p.limit > 0 {
			samples = io.LimitReader(p.from, p.limit)
		}
		copied, err := io.Copy(writer, samples)
		if p.limit > 0 && copied >= p.limit {
			p.reachedLimit.Store(true)
		}
		_ = writer.CloseWithError(err)
	}()
	return reader
}

// ready reports whether the body may begin: release is closed and the
// clock has reached until.
func (p *discardPump) ready() bool {
	select {
	case <-p.release:
	default:
		return false
	}
	return !p.now().Before(p.until)
}

// discarded returns the number of bytes dropped. The log line reports it
// so a reader can tell a long begin from a slow link.
func (p *discardPump) discarded() int64 { return p.dropped.Load() }

func (p *discardPump) delivered() bool { return p.reachedLimit.Load() }

type readCloser struct {
	io.Reader
	close func() error
}

func (r readCloser) Close() error { return r.close() }

// stream runs one tap and copies it to the response. It writes no header
// until confirm succeeds, so a wrong target is a 500 and not a 200 that
// ends early.
func (s *captureServer) stream(w http.ResponseWriter, r *http.Request, plan tapPlan, at time.Time) {
	release := make(chan struct{})
	plan.Until = at.Add(plan.Query.Span.Begin)
	plan.Now = s.now
	plan.Release = release

	tap, err := s.start(r.Context(), plan)
	if err != nil {
		s.readings.failed(failureConnect)
		w.Header().Set("Retry-After", retryAfterSeconds)
		s.refuse(w, r, http.StatusServiceUnavailable, problemBlank, plan.RequestID, err.Error())
		s.logTap(plan, at, 0, 0, "not-started", err.Error())
		return
	}
	stopped := false
	ended := tapExit{}
	finish := func(drain bool) tapExit {
		if !stopped {
			// Stop the processes before closing their pipes. Closing
			// first would send SIGPIPE to a process that is blocked on a
			// write, and this container's own teardown would look like a
			// failure of that process.
			ended = tap.stop(drain)
			_ = tap.Body.Close()
			stopped = true
		}
		return ended
	}
	defer finish(false)

	if err := s.confirm(r.Context(), plan.Stream, plan.Format.NodeID); err != nil {
		if errors.Is(err, ErrGraphUnread) {
			s.readings.failed(failureConnect)
			w.Header().Set("Retry-After", retryAfterSeconds)
			s.refuse(w, r, http.StatusServiceUnavailable, problemBlank, plan.RequestID, err.Error())
			s.logTap(plan, at, 0, 0, "unread", err.Error())
			return
		}
		s.readings.failed(failureWrongTarget)
		s.refuse(w, r, http.StatusInternalServerError, problemWrongTarget, plan.RequestID, err.Error())
		s.logTap(plan, at, 0, 0, "wrong-target", err.Error())
		return
	}
	close(release)

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")

	s.readings.started(plan.Route.Aspect)
	started := s.now()
	body := io.Reader(tap.Body)
	if plan.Form.Extension == "wav" {
		body = io.MultiReader(bytes.NewReader(wavHeader(plan.Format.Rate, plan.Format.Channels)), tap.Body)
	}
	sent, copyErr := copyFlushing(w, plan.Form.ContentType, body, nil)
	ran := s.now().Sub(started)
	s.readings.finished(plan.Route.Aspect, plan.Form.Extension, sent, ran)

	// Stop the processes before logging, because the log line includes
	// their exit statuses. Drain only when the span ran out, because the
	// pump has then closed the encoder's stdin and the encoder exits by
	// itself.
	exit := finish(tap.delivered())
	if exit.failed() {
		s.readings.failed(failureEncoder)
	}

	cut := bodyCut(tap.delivered(),
		endedByClient(copyErr, r.Context().Err() != nil),
		exit.encoderFailed())

	words := exit.String()
	if copyErr != nil {
		words += " cut=" + copyErr.Error()
	}
	if cut {
		words += " truncated"
	}
	s.logTap(plan, at, sent, tap.discarded(), "on-target", words)

	if cut {
		// The 200 and part of the body are already sent, so the only way
		// to tell the client that the body is incomplete is to abort the
		// response. Panicking with http.ErrAbortHandler closes an
		// HTTP/1.1 chunked body without its terminating chunk (RFC 9112
		// section 8) and resets an HTTP/2 stream. net/http recovers this
		// panic without logging a stack trace.
		panic(http.ErrAbortHandler)
	}
}

// logTap prints the log line that this container writes for each tap,
// whatever its result. link is not-started, unread, wrong-target, or
// on-target. ended is the error text when the tap failed before the
// body, and otherwise tapExit.String, followed by cut= and the copy
// error when the copy failed, and by truncated when the body was cut.
func (s *captureServer) logTap(plan tapPlan, at time.Time, sent, discarded int64,
	link, ended string) {
	line := fmt.Sprintf("%s: tap %s target=%s node=%d stream=%s format=%s rate=%d "+
		"channels=%d span=%s bytes=%d discarded=%d link=%s at=%s seconds=%.3f ended=%s",
		DriverName, plan.RequestID, plan.Node, plan.Format.NodeID, plan.Stream,
		plan.Form.Extension, plan.Format.Rate, plan.Format.Channels,
		spanWords(plan.Query.Span), sent, discarded, link,
		at.UTC().Format(time.RFC3339), s.now().Sub(at).Seconds(), ended)
	if s.log == nil {
		fmt.Println(line)
		return
	}
	s.log(line)
}

// confirm reads the graph until the link from this tap's stream to the
// target node appears. It returns an error as soon as the link goes to
// another node, and when no link appears within linkDeadline, because
// pw-record with a target that does not exist links to the default
// sink's monitor and would record the wrong audio.
func (s *captureServer) confirm(ctx context.Context, stream string, targetNodeID int) error {
	deadline := time.After(linkDeadline)
	period := linkFirstPeriod
	for {
		document, err := s.graph(ctx)
		if err != nil {
			return err
		}
		state, err := confirmLink(document, stream, targetNodeID)
		if err != nil {
			return err
		}
		if state == linkOnTarget {
			return nil
		}
		if state == linkElsewhere {
			return fmt.Errorf("pw-record linked to a node other than the %d this request named", targetNodeID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("pw-record made no link to the node %d within %s",
				targetNodeID, linkDeadline)
		case <-time.After(period):
		}
		if period *= 2; period > linkPeriod {
			period = linkPeriod
		}
	}
}

// errClientGone marks a write error in copyFlushing: the client stopped
// reading the response, which is a normal end of a tap.
var errClientGone = errors.New("the client stopped reading")

// endedByClient reports whether the client left. Usually the write
// fails with errClientGone. But the processes run under the request's
// context, so when the client leaves they are killed, and the read from
// their pipe can fail before the next write does. Then only requestDone
// shows that the client left.
func endedByClient(copyErr error, requestDone bool) bool {
	return requestDone || errors.Is(copyErr, errClientGone)
}

// bodyCut reports whether the client must be told that the body is
// incomplete. A body is complete when the span ran out or the client
// left. Otherwise the pipeline ended before the span did. An encoder
// failure cuts the body even when the whole span was delivered, because
// the encoder may not have written its last frame.
func bodyCut(delivered, left, encoderFailed bool) bool {
	return (!delivered && !left) || encoderFailed
}

// copyFlushing writes a 200 with the content type, then copies from to w
// and flushes after each block, so the client receives the audio as it
// arrives and not when a buffer fills. It calls onFirst once, after the
// first block is written.
//
// The headers are sent and flushed before any audio. A tap with a begin
// sends no audio while discardPump drops the samples before begin, and
// until then the client would have no status or headers to show.
// X-Content-Type-Options: nosniff stops a browser from guessing another
// type for the audio bytes.
//
// A write error is wrapped in errClientGone. A read error is returned
// unchanged.
func copyFlushing(w http.ResponseWriter, contentType string, from io.Reader,
	onFirst func()) (int64, error) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if err := flush(w); err != nil {
		fmt.Fprintf(os.Stderr, "flushing the tap's headers: %v\n", err)
	}
	block := make([]byte, 32*1024)
	var sent int64
	for {
		read, err := from.Read(block)
		if read > 0 {
			written, writeErr := w.Write(block[:read])
			if sent == 0 && written > 0 && onFirst != nil {
				onFirst()
				onFirst = nil
			}
			sent += int64(written)
			_ = flush(w)
			if writeErr != nil {
				return sent, fmt.Errorf("%w: %w", errClientGone, writeErr)
			}
		}
		if err != nil {
			if err == io.EOF {
				return sent, nil
			}
			return sent, err
		}
	}
}

// flush uses http.ResponseController and not a type assertion to
// http.Flusher, because the assertion fails on a ResponseWriter that
// wraps another one, and the headers and the first block would then stay
// in a buffer. The controller unwraps the writers and returns an error
// when none of them can flush.
func flush(w http.ResponseWriter) error {
	return http.NewResponseController(w).Flush()
}

// dumpGraph reads the graph for the capture handler and for confirm,
// with the pwDumpTimeout that readGraph also uses.
func dumpGraph(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, pwDumpTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-dump")
	command.WaitDelay = time.Second
	stderr := &stderrBuffer{}
	command.Stderr = stderr
	raw, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: running pw-dump: %w: %s",
			ErrGraphUnread, err, unexpectedStderr(stderr.String()))
	}
	return raw, nil
}

// requestID returns a random 16-digit hexadecimal ID for one request. It
// goes in the instance of the problem document and in the log line, so a
// response can be matched to its log line.
func requestID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(buffer)
}

func spanWords(span timeSpan) string {
	if span.Ended {
		return fmt.Sprintf("%s,%s", span.Begin, span.End)
	}
	if span.Begin > 0 {
		return span.Begin.String()
	}
	return "open"
}

// endedWords formats the error that ended a stream, for a reason other
// than the client closing.
func endedWords(err error) string {
	if err == nil {
		return ""
	}
	return " ended=" + err.Error()
}
