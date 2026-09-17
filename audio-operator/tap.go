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
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// linkDeadline is how long the container waits for PipeWire to build
// the link before it calls the target wrong, and linkPeriod is how
// often it looks. Each look is one pw-dump process and one parse of
// the whole graph, so the period is what bounds the cost of a link
// that takes its time.
const (
	linkDeadline = 3 * time.Second
	linkPeriod   = 250 * time.Millisecond
)

// discardBlock is how much the discard reads at a time. 2048 bytes is
// about 10.7 ms at 48 kHz stereo, which is the most the clock can
// overshoot the instant it is aiming at.
const discardBlock = 2048

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

// runningTap is the pipeline: the samples to read, the process id the
// confirmation looks for, and the words each process printed.
type runningTap struct {
	Body      io.ReadCloser
	RecordPID int

	stop     func()
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

// words is what a failure carries: whatever the processes printed that
// this container does not expect, verbatim.
func (t *runningTap) words() string {
	both := unexpectedStderr(t.recorded.String())
	if encoder := unexpectedStderr(t.encoded.String()); encoder != "" {
		if both != "" {
			both += "; "
		}
		both += encoder
	}
	return both
}

// startTap runs pw-record, and an encoder when the form needs one.
//
// until is when the body's sample zero belongs, which is the accept
// instant plus begin, and release is closed once the link is confirmed.
// The pump below discards until both have passed, so the confirmation
// never lets the pipe fill and never shifts the samples.
func startTap(ctx context.Context, plan tapPlan) (*runningTap, error) {
	ctx, cancel := context.WithCancel(ctx)
	record := commandOf(ctx, recordCommand(plan.Node, plan.Direction, plan.Format))
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
		RecordPID: record.Process.Pid,
		recorded:  recorded,
		encoded:   &spoken{},
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
		tap.stop = func() { cancel(); _ = record.Wait() }
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
	tap.stop = func() {
		cancel()
		_ = encoder.Wait()
		_ = record.Wait()
	}
	return tap, nil
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
		_, err := io.Copy(writer, samples)
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
		return
	}
	defer func() { _ = tap.Body.Close(); tap.stop() }()

	if err := s.confirm(r.Context(), tap.RecordPID, plan.Format.NodeID); err != nil {
		s.readings.failed(failureWrongTarget)
		s.refuse(w, r, http.StatusInternalServerError, problemWrongTarget, plan.RequestID, err.Error())
		return
	}
	close(release)

	w.Header().Set("Content-Type", plan.Form.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)
	flush(w)

	s.readings.started(plan.Route.Aspect)
	started := s.now()
	var sent int64
	if plan.Form.Extension == "wav" {
		written, _ := w.Write(wavHeader(plan.Format.Rate, plan.Format.Channels))
		sent += int64(written)
		flush(w)
	}
	copied, copyErr := copyFlushing(w, tap.Body, nil)
	sent += copied
	ran := s.now().Sub(started)
	s.readings.finished(plan.Route.Aspect, plan.Form.Extension, sent, ran)

	if words := tap.words(); words != "" {
		s.readings.failed(failureEncoder)
		fmt.Printf("%s: tap %s target=%s format=%s rate=%d channels=%d bytes=%d seconds=%.3f: %s\n",
			DriverName, plan.RequestID, plan.Node, plan.Form.Extension,
			plan.Format.Rate, plan.Format.Channels, sent, ran.Seconds(), words)
		return
	}
	fmt.Printf("%s: tap %s target=%s format=%s rate=%d channels=%d span=%s discarded=%d bytes=%d seconds=%.3f at=%s%s\n",
		DriverName, plan.RequestID, plan.Node, plan.Form.Extension,
		plan.Format.Rate, plan.Format.Channels, spanWords(plan.Knobs.Span),
		tap.discarded(), sent, ran.Seconds(), at.UTC().Format(time.RFC3339), endedWords(copyErr))
}

// confirm reads the graph until the link this tap's stream made
// appears. A link to another node ends the tap at once, and no link by
// the deadline ends it too: pw-record with an unknown target links to
// the default sink's monitor, and either way the sound on the wire
// would not be the sound that was asked for.
func (s *captureServer) confirm(ctx context.Context, processID, targetNodeID int) error {
	wait := s.linkDeadline
	if wait == 0 {
		wait = linkDeadline
	}
	deadline := time.After(wait)
	for {
		document, err := s.graph(ctx)
		if err != nil {
			return err
		}
		// One parse answers all three states, so a poll reads the
		// graph once rather than walking it twice.
		state, err := confirmLink(document, processID, targetNodeID)
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
		case <-time.After(linkPeriod):
		}
	}
}

// copyFlushing copies the pipeline to the response, flushing each
// block, so a client hears the tap as it arrives rather than when a
// buffer fills. onFirst runs once, when the first block has been
// written, which is the moment a request has produced bytes.
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
			flush(w)
			if writeErr != nil {
				return sent, writeErr
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

func flush(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
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
		return nil, fmt.Errorf("running pw-dump: %w: %s", err, unexpectedStderr(complaints.String()))
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
