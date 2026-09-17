package main

// Running one tap: the processes, the confirmation that the link
// landed where the request asked, and the copy from the pipeline to
// the response.
//
// The order is fixed by what each step can still refuse. The
// container resolves the node first, because pw-record never refuses
// a bad target. It starts the pipeline, then reads the graph again to
// confirm the link, and only then writes the status line, because a
// 500 wrong-target cannot be sent after a 200 has gone out. A
// confirmation that fails ends the tap with nothing written.
//
// The discard is a clock and not a byte count. Sample zero of the
// body is the accept instant plus begin on this container's own
// clock, and the bound on the body's length is the span's own
// duration in bytes. Both are applied to the raw samples, ahead of
// any encoder, so an encoder only ever sees the span the client asked
// for.
//
// The discard runs from the moment pw-record starts, so the pipe is
// drained while the link is confirmed rather than held unread.
// Without that, pw-record would block on a full pipe within about
// 340 ms at 48 kHz stereo, and the whole confirmation time would be
// added to the shift between the accept instant and sample zero.
//
// An encoder that dies ends the response with no terminating chunk,
// which is HTTP/1.1's signal for an incomplete message (RFC 9112
// section 8), and the log line carries the encoder's stderr.

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

// linkDeadline is how long the container waits for PipeWire to build
// the link before it calls the target wrong.
//
// Each look is one pw-dump process, so the interval between them
// trades the time to the first body byte against the number of
// processes a slow link costs. It starts at linkFirstPeriod and
// doubles to linkPeriod, because PipeWire usually has the link within
// a few tens of milliseconds and a poll every quarter second was
// adding up to that much to every tap.
const (
	linkDeadline    = 3 * time.Second
	linkFirstPeriod = 20 * time.Millisecond
	linkPeriod      = 250 * time.Millisecond
)

// discardBlock is how much the discard reads at a time. 2048 bytes is
// about 10.7 ms at 48 kHz stereo, which is the most the clock can
// overshoot the instant it is aiming at.
const discardBlock = 2048

// ErrGraphUnread marks a graph this container could not read at all,
// which is PipeWire being down rather than a tap that landed
// somewhere else. It is a 503 that clears on its own, and the daemon's
// own words go in the detail.
var ErrGraphUnread = errors.New("PipeWire did not answer")

// tapPlan is everything one tap needs, settled before any process
// starts.
type tapPlan struct {
	Route     apiRoute
	Node      string
	Direction pwDirection
	Form      representation
	Format    captureFormat
	Knobs     captureKnobs
	RequestID string

	// Stream is the node.name pw-record's own node takes in the
	// graph, which is how the confirmation finds this tap.
	Stream string

	// Until is when sample zero of the body belongs: the accept
	// instant plus begin. Release is closed when the link is
	// confirmed, and the pump discards until both have passed.
	Until   time.Time
	Now     func() time.Time
	Release <-chan struct{}
}

// spoken is what one process printed on stderr. os/exec copies into it
// from a goroutine of its own while the tap runs, and this container
// reads it whenever a failure has to carry the process's own words, so
// the two go through the lock.
type spoken struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (s *spoken) Write(block []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.Write(block)
}

func (s *spoken) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer.String()
}

// runningTap is the pipeline: the samples to read, and the words each
// process printed.
type runningTap struct {
	Body io.ReadCloser

	stop     func(drain bool) tapExit
	pump     *discardPump
	recorded *spoken
	encoded  *spoken
}

// discarded is how many bytes this tap dropped before the body began.
func (t *runningTap) discarded() int64 {
	if t.pump == nil {
		return 0
	}
	return t.pump.discarded()
}

// delivered says this tap sent the whole span it was asked for.
func (t *runningTap) delivered() bool {
	return t.pump != nil && t.pump.delivered()
}

// tapExit is how one tap's processes ended: the exit status of each,
// and the last thing each said.
//
// An exit status of -1 is a process this container signalled, which is
// what ends every tap that finished its span or lost its client. Only
// a status of one or more is a process that failed on its own.
type tapExit struct {
	Recorder int
	Encoder  int
	Words    string
}

// failed says whether a process ended on its own with something to
// report. Both encoders print a banner and a progress bar to stderr
// and exit zero on every successful tap, so stderr alone says nothing
// about whether a tap worked.
func (e tapExit) failed() bool {
	return e.Recorder > 0 || e.Encoder > 0
}

// String is the log line's ended field: the two exit statuses, and the
// last line either process wrote.
func (e tapExit) String() string {
	ended := fmt.Sprintf("pw-record:%s", exitWords(e.Recorder))
	if e.Encoder != exitNotRun {
		ended += fmt.Sprintf(" encoder:%s", exitWords(e.Encoder))
	}
	if e.Words != "" {
		ended += " " + e.Words
	}
	return ended
}

// The two statuses that are not a process's own.
//
// exitNotRun marks a process this tap never started, which is the
// encoder of a WAV tap. exitUnknown marks a wait that answered
// something other than an exit: os/exec reports exec.ErrWaitDelay when
// a process exits well and its pipes are still open a moment later,
// and that says nothing about how the process ended.
const (
	exitNotRun  = -2
	exitUnknown = -3
)

// encoderDrain is how long the pipeline waits for an encoder that is
// already finishing. The span closed its stdin, so it is writing its
// last bytes and exiting; a second is many times what either encoder
// needs, and a wait that runs out cancels rather than hanging a tap.
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

// lastLine is the last thing a process said. An encoder's banner and
// its progress bar are everything before it, and a progress bar
// returns the carriage rather than the line, so both endings split it.
func lastLine(output string) string {
	kept := ""
	for _, line := range strings.FieldsFunc(output, func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			kept = trimmed
		}
	}
	if len(kept) > lastLineMax {
		return kept[:lastLineMax] + "..."
	}
	return kept
}

// lastLineMax bounds the one line the log carries, because a process
// may write a line of any length and a log line is read by a person.
const lastLineMax = 200

// startTap runs pw-record, and an encoder when the form needs one.
//
// until is when the body's sample zero belongs, which is the accept
// instant plus begin, and release is closed once the link is confirmed.
// The pump below discards until both have passed, so the confirmation
// never lets the pipe fill and never shifts the samples.
func startTap(ctx context.Context, plan tapPlan) (*runningTap, error) {
	ctx, cancel := context.WithCancel(ctx)
	record := commandOf(ctx, recordCommand(plan.Node, plan.Stream, plan.Direction, plan.Format))
	recorded := &spoken{}
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
		encoded:  &spoken{},
	}

	// The span is taken off the raw samples, before any encoder, so
	// the encoder produces the span and nothing around it.
	span := plan.Knobs.Span
	pump := &discardPump{
		from:    raw,
		until:   plan.Until,
		now:     plan.Now,
		release: plan.Release,
		limit:   span.bodyBytes(plan.Format.Rate, plan.Format.Channels),
	}
	tap.pump = pump
	samples := pump.start()

	encoderArgs := encoderCommand(plan.Form, plan.Format, plan.Knobs.Bitrate)
	if encoderArgs == nil {
		tap.Body = readCloser{Reader: samples, close: raw.Close}
		tap.stop = func(bool) tapExit {
			cancel()
			return tapExit{
				Recorder: exitStatus(waitForRecorder(record, raw)),
				Encoder:  exitNotRun,
				Words:    lastLine(recorded.String()),
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
	waited := make(chan error, 1)
	go func() { waited <- encoder.Wait() }()
	tap.stop = func(drain bool) tapExit {
		ended := tapExit{}
		// A span that ran out closed the encoder's stdin, so the
		// encoder is already writing its last bytes and exiting on its
		// own. Waiting for that is what makes the status its own:
		// cancelling first leaves os/exec watching pipes it is about
		// to close, and the wait then answers ErrWaitDelay rather than
		// the zero the encoder exited with.
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
		ended.Words = lastLine(tap.encoded.String())
		if ended.Words == "" {
			ended.Words = lastLine(recorded.String())
		}
		return ended
	}
	return tap, nil
}

// exitStatus reads what a process ended with. A process this container
// signalled reports -1, which is how a tap whose client hung up ends.
//
// An error that is not an ExitError carries no status at all: os/exec
// answers exec.ErrWaitDelay when a process exits well and its pipes
// are still open a moment later, and reading that as a failure made
// every finished Opus span look like a capture that was cut short.
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

// waitForRecorder reaps pw-record after the context has killed it.
//
// The pipe this container reads its samples from is closed first,
// because os/exec waits out WaitDelay for a pipe that is still open
// when the process ends, and that second was landing on the end of
// every tap: the client had its last block and sat waiting for the
// response to close.
func waitForRecorder(record *exec.Cmd, samples io.ReadCloser) error {
	_ = samples.Close()
	return record.Wait()
}

// commandOf builds one process under the context, with the same wait
// delay every exec in this program takes: the kill bounds the process,
// and the delay bounds the read after the kill.
func commandOf(ctx context.Context, args []string) *exec.Cmd {
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.WaitDelay = time.Second
	return command
}

// discardPump reads pw-record's samples from the moment the process
// starts. It drops what it reads until the clock reaches until and the
// confirmation has closed release, then delivers the rest through a
// pipe, bounded at limit bytes when the span names an end.
//
// The pipe is what lets the pump run ahead of the response. The
// reader of the body is the response, which does not exist yet while
// the link is being confirmed, so a plain reader would leave the
// samples unread and pw-record would block on a full pipe.
type discardPump struct {
	from    io.ReadCloser
	until   time.Time
	now     func() time.Time
	release <-chan struct{}
	limit   int64

	// dropped is written by the pump's own goroutine and read by the
	// log line when the tap ends, so it is counted atomically.
	dropped atomic.Int64

	// whole says the span ran out, which is the one way a bounded tap
	// ends with nothing left to send. It is written by the pump's own
	// goroutine for the reason dropped is.
	whole atomic.Bool
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
			p.whole.Store(true)
		}
		_ = writer.CloseWithError(err)
	}()
	return reader
}

// ready says whether the body may begin: the confirmation has finished
// and the clock has reached the instant sample zero belongs at.
func (p *discardPump) ready() bool {
	select {
	case <-p.release:
	default:
		return false
	}
	return !p.now().Before(p.until)
}

// discarded is how many bytes the pump dropped, which the log line
// carries so a reader can tell a long begin from a slow link.
func (p *discardPump) discarded() int64 { return p.dropped.Load() }

// delivered says the span ran out with every sample of it sent.
func (p *discardPump) delivered() bool { return p.whole.Load() }

// readCloser pairs a reader with the close that ends the pipeline.
type readCloser struct {
	io.Reader
	close func() error
}

func (r readCloser) Close() error { return r.close() }

// stream runs one tap and copies it to the response. The headers go
// out only after the link is confirmed, so a wrong target is a 500
// and never a 200 that ends early.
func (s *captureServer) stream(w http.ResponseWriter, r *http.Request, plan tapPlan, at time.Time) {
	// The pump starts discarding as soon as pw-record does, and holds
	// the body back until this channel closes.
	release := make(chan struct{})
	plan.Until = at.Add(plan.Knobs.Span.Begin)
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
			_ = tap.Body.Close()
			ended = tap.stop(drain)
			stopped = true
		}
		return ended
	}
	defer finish(false)

	if err := s.confirm(r.Context(), plan.Stream, plan.Format.NodeID); err != nil {
		// A graph this container could not read says nothing about
		// where the tap landed. PipeWire that is down clears on its
		// own, so it is a 503 and the tap is not called wrong.
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

	w.Header().Set("Content-Type", plan.Form.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)
	// The headers go out now, not when the first block of audio
	// arrives. A tap with a begin sends nothing for as long as the
	// discard runs, and a client that had not yet seen the status
	// would have nothing to show for it.
	if err := flush(w); err != nil {
		fmt.Fprintf(os.Stderr, "the capture headers could not be flushed: %v\n", err)
	}

	s.readings.started(plan.Route.Aspect)
	started := s.now()
	var sent int64
	if plan.Form.Extension == "wav" {
		written, _ := w.Write(wavHeader(plan.Format.Rate, plan.Format.Channels))
		sent += int64(written)
		_ = flush(w)
	}
	copied, copyErr := copyFlushing(w, tap.Body, nil)
	sent += copied
	ran := s.now().Sub(started)
	s.readings.finished(plan.Route.Aspect, plan.Form.Extension, sent, ran)

	// The processes are stopped before the line is written, because
	// their exit statuses are what it reports. A tap that finished its
	// span and a tap whose client hung up both end with a signal, and
	// neither is a failure; only a process that ended on its own with
	// a status of one or more is.
	// The span running out is the one end that leaves an encoder
	// finishing of its own accord, and the only one worth waiting for.
	exit := finish(tap.delivered())
	if exit.failed() {
		s.readings.failed(failureEncoder)
	}

	// A body ends cleanly when there was no more of it to send: the
	// span ran out, or the client stopped reading. Anything else is
	// the pipeline ending under the tap, which the client has to be
	// told about.
	complete := tap.delivered() || errors.Is(copyErr, errClientGone)
	cut := !complete || exit.failed()

	words := exit.String()
	if copyErr != nil {
		words += " cut=" + copyErr.Error()
	}
	if cut {
		words += " truncated"
	}
	s.logTap(plan, at, sent, tap.discarded(), "on-target", words)

	if cut {
		// The response is already a 200 with bytes on the wire, so
		// there is no status left to tell a client with. Ending the
		// handler this way closes the body without its terminating
		// chunk, which is RFC 9112 section 8's signal for an
		// incomplete message, and over HTTP/2 it resets the stream.
		// ErrAbortHandler is the one panic net/http expects, so it
		// prints no stack trace for it.
		panic(http.ErrAbortHandler)
	}
}

// logTap writes the one line this container keeps for a tap, whatever
// became of it. link says what the confirmation read, and ended is the
// exit status: the encoder's own words when one failed, the reason a
// stream stopped, or ok.
func (s *captureServer) logTap(plan tapPlan, at time.Time, sent, discarded int64,
	link, ended string) {
	line := fmt.Sprintf("%s: tap %s target=%s node=%d stream=%s format=%s rate=%d "+
		"channels=%d span=%s bytes=%d discarded=%d link=%s at=%s seconds=%.3f ended=%s",
		DriverName, plan.RequestID, plan.Node, plan.Format.NodeID, plan.Stream,
		plan.Form.Extension, plan.Format.Rate, plan.Format.Channels,
		spanWords(plan.Knobs.Span), sent, discarded, link,
		at.UTC().Format(time.RFC3339), s.now().Sub(at).Seconds(), ended)
	if s.log == nil {
		fmt.Println(line)
		return
	}
	s.log(line)
}

// confirm reads the graph until the link this tap's stream made
// appears. A link to another node ends the tap at once, and no link by
// the deadline ends it too: pw-record with an unknown target links to
// the default sink's monitor, and either way the sound on the wire
// would not be the sound that was asked for.
func (s *captureServer) confirm(ctx context.Context, stream string, targetNodeID int) error {
	wait := s.linkDeadline
	if wait == 0 {
		wait = linkDeadline
	}
	deadline := time.After(wait)
	period := linkFirstPeriod
	for {
		document, err := s.graph(ctx)
		if err != nil {
			return err
		}
		// One parse answers all three states, so a poll reads the
		// graph once rather than walking it twice.
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
				targetNodeID, wait)
		case <-time.After(period):
		}
		if period *= 2; period > linkPeriod {
			period = linkPeriod
		}
	}
}

// errClientGone marks a copy that ended because whoever was reading the
// response stopped reading. That is a client hanging up, which is a
// normal end of a tap, and it is told apart from the pipeline ending
// on its own, which is not.
var errClientGone = errors.New("the client stopped reading")

// copyFlushing copies the pipeline to the response, flushing each
// block, so a client hears the tap as it arrives rather than when a
// buffer fills. onFirst runs once, when the first block has been
// written, which is the moment a request has produced bytes.
//
// An error from the writing side is wrapped in errClientGone; an error
// from the reading side is returned as it came.
func copyFlushing(w http.ResponseWriter, from io.Reader, onFirst func()) (int64, error) {
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

// flush pushes what has been written to the client at once.
//
// It goes through a ResponseController rather than a type assertion on
// http.Flusher, because an assertion answers no for any writer that
// wraps another and a flush that quietly does not happen is invisible:
// the headers and the first block would then sit in a buffer until
// something else filled it. The controller unwraps the chain and says
// so when nothing in it can flush.
func flush(w http.ResponseWriter) error {
	return http.NewResponseController(w).Flush()
}

// dumpGraph is the container's own read of PipeWire, which is the same
// pw-dump the reconcile loop runs, under the same bound.
func dumpGraph(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, pwDumpTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "pw-dump")
	command.WaitDelay = time.Second
	complaints := &spoken{}
	command.Stderr = complaints
	raw, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: running pw-dump: %w: %s",
			ErrGraphUnread, err, unexpectedStderr(complaints.String()))
	}
	return raw, nil
}

// requestID names one request. The problem document's instance carries
// it and so does the log line, so a reader joins the answer a client
// saw to the record this container kept.
func requestID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(buffer)
}

// spanWords is the span as the log line names it.
func spanWords(span timeSpan) string {
	if span.Ended {
		return fmt.Sprintf("%s,%s", span.Begin, span.End)
	}
	if span.Begin > 0 {
		return span.Begin.String()
	}
	return "open"
}

// endedWords names how a stream ended, when it ended for a reason
// other than the client closing.
func endedWords(err error) string {
	if err == nil {
		return ""
	}
	return " ended=" + err.Error()
}
