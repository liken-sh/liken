package main

// This file serves the composed aspect: the Player's screen and its
// sound as one muxed stream. The API requests screen.mp4 from the
// display API and audio.opus from the audio API for each sink, opens
// every request at once, and hands the bodies to one ffmpeg process.
// The requests open at once because the offset between the tracks is
// measured from the instant each upstream's headers arrive, and the
// measurement is only meaningful when the requests were sent
// together. The mux copies packets and never encodes, so the API node
// needs no codec and no GPU, and the public face never encodes.

import (
	"context"
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

// composeLeadIn is the lead-in L, in seconds, and composeLeadInWord is
// the same value as the discovery document states it. The API asks
// both upstreams for t=begin+L,end+L, so each sidecar has a running
// pipeline before its zero and the header-arrival offset is the right
// correction. The composed stream is therefore one second behind now.
const (
	composeLeadIn     = 1.0
	composeLeadInWord = "1s"
)

// The two extensions the composed aspect serves: fragmented MP4, the
// default, and Matroska.
const (
	composedMP4Extension = "mp4"
	composedMKVExtension = "mkv"
)

// composedAudioCodec is the codecs element for the audio track, spelt
// as the ISOBMFF sample entry spells it. RFC 6381 section 3.3 says the
// values are case sensitive, and the sample entry is Opus with a
// capital O; browsers took lowercase opus from the MSE byte-stream
// convention, and the manual records which spelling each takes.
const composedAudioCodec = "Opus"

// serveComposed applies the stream-count rule. The API counts the
// streams the Player resolves: one video for `status.screen`, and one
// audio per sink while a `Play` runs. Zero streams is a 409 whose
// detail says to run a `Play`. One or more streams the API composes
// and serves itself, so a client that reached this API never follows
// a redirect to a sibling's in-cluster name. A single stream is a
// one-input composition: a screen alone, or one sink alone.
func (s *apiServer) serveComposed(e *apiExchange, player *Player, form mediaForm, query captureQuery, negotiated bool) {
	monitor := rememberedMonitor(player)
	sinks := tappableSinks(player)
	if monitor == "" && len(sinks) == 0 {
		e.fail(problemNotPlaying, http.StatusConflict, "This Player plays nothing",
			notPlayingDetail(player, mediaAspectName))
		return
	}

	// One or more resolved streams are composed and served here, never
	// redirected, so the caller reaches only media-api. This widens
	// `players/media`: it now also captures the idle screen, a screen
	// with no running `Play`, with no separate `displays/screen` check,
	// because media-api opens the sibling under its own ServiceAccount.

	// A screenless unit opens no display request at all. A request
	// against an empty Display name would be a 404 from the sibling,
	// and the composition would fail for a unit that plays sound alone
	// by design.
	screen := ""
	if monitor != "" {
		screen = displayTarget(s.display, monitor, composedMP4Extension, query, composeLeadIn)
	}
	tracks := make([]string, len(sinks))
	for index, sink := range sinks {
		tracks[index] = audioTarget(s.audio, sink.Name, "opus", query, composeLeadIn)
	}

	if e.head {
		e.composedHeaders(form, screen, tracks, negotiated)
		e.writer.Header().Set("Content-Type", form.Type)
		e.answer(http.StatusOK)
		return
	}
	s.streamComposed(e, player, form, query, screen, tracks, negotiated)
}

// composedHeaders sets the capture fields of a composed 200: no-store,
// no ranges because two requests for one URI produce two byte
// sequences, a file name for a client that saves, and one link per
// upstream. The negotiated route adds Content-Location and the
// alternate links, which name the fixed forms. The caller sets these
// only once every upstream has answered, so an error carries the
// problem document alone. A HEAD gets the same fields but no codecs
// parameter, because the codecs are determined while generating the
// content, RFC 9110 section 9.3.2, and a HEAD makes no upstream call.
func (e *apiExchange) composedHeaders(form mediaForm, screen string, tracks []string, negotiated bool) {
	header := e.writer.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Accept-Ranges", "none")
	header.Set("Content-Disposition",
		`inline; filename="`+captureFilename(e.namespace, e.name, form.Extension, e.server.clock())+`"`)
	if screen != "" {
		addLink(header, screen, relScreen, "")
	}
	for _, track := range tracks {
		addLink(header, track, relAudio, "")
	}
	if !negotiated {
		return
	}
	header.Set("Content-Location", e.aspectPath(mediaAspectName, form.Extension))
	for _, alternate := range composedForms {
		addLink(header, e.aspectPath(mediaAspectName, alternate.Extension), relAlternate, alternate.Type)
	}
}

// captureFilename is the Content-Disposition file name, namespace, name,
// and the time in RFC 3339 UTC with the colons replaced by hyphens. A
// colon is not legal in a file name everywhere a browser saves, so
// this is the one place the time is not in the standard form.
func captureFilename(namespace, name, extension string, at time.Time) string {
	stamp := strings.ReplaceAll(at.UTC().Format(time.RFC3339), ":", "-")
	return namespace + "-" + name + "-" + stamp + "." + extension
}

// streamComposed runs one composition from start to end: it takes a
// slot under the composition limit, opens every upstream at once,
// measures the offset between them, sends the headers, and muxes until
// the span ends or a side fails. A failure before the headers is a
// problem document; a failure after the stream began ends the
// response, and the fault reaches the client as a truncated body and
// the request log line.
func (s *apiServer) streamComposed(e *apiExchange, player *Player, form mediaForm, query captureQuery, screen string, tracks []string, negotiated bool) {
	release, taken := s.takeComposition()
	if !taken {
		e.writer.Header().Set("Retry-After", retryAfterSeconds)
		document := newProblem(problemCaptureBusy, http.StatusServiceUnavailable,
			"This API is composing all it can at once",
			"the composition limit of "+strconv.Itoa(s.maxCompositions)+" is taken", e.instance())
		e.answerProblem(document)
		return
	}
	defer release()

	ctx, cancel := context.WithCancel(e.request.Context())
	defer cancel()

	headerTimeout := upstreamHeaderTimeout
	if query.HasBegin {
		headerTimeout += time.Duration(query.Begin * float64(time.Second))
	}
	streams, fault := s.openAll(ctx, screen, tracks, headerTimeout)
	if fault != nil {
		e.answerFault(fault)
		return
	}
	defer func() {
		for _, stream := range streams {
			stream.close()
		}
	}()

	// The reference clock is the first stream, the screen where the
	// unit has one and the first sink where it has none. Each other
	// stream's offset is its header instant minus the reference's, in
	// seconds, and that is the -itsoffset its input takes. On an
	// audio-only composition the first stream is measured against
	// itself, an offset of zero that says nothing, so it is not
	// observed in the metric.
	first := boolToInput(screen != "")
	offsets := make([]float64, 0, len(streams)-first)
	for index, stream := range streams[first:] {
		offset := stream.headersAt.Sub(streams[0].headersAt).Seconds()
		offsets = append(offsets, offset)
		if screen != "" || index > 0 {
			s.metrics.observeOffset(offset)
		}
	}
	if len(offsets) > 0 {
		e.offset = offsets[0]
	}
	e.upstreams = upstreamTargets(screen, tracks)

	// The capture fields go on only now, once every upstream has
	// answered. A refusal above carries the problem document alone,
	// with no Content-Disposition or link to a stream that never
	// began.
	e.composedHeaders(form, screen, tracks, negotiated)
	e.writer.Header().Set("Content-Type", composedContentType(form, screen != "", streams[0].codecs))
	done := s.metrics.holdStream(mediaAspectName)
	defer done()
	s.recordCapture(e, player, mediaAspectName, form.Type)
	e.answerStreaming(http.StatusOK)

	if err := s.mux(ctx, cancel, e, form.Extension, screen != "", streams, offsets, query); err != nil {
		e.ffmpeg = err.Error()
	}
}

// composedContentType is the Content-Type of a composed GET. For MP4 it
// carries the RFC 6381 codecs parameter: the avc1 element copied from
// the display upstream, then Opus as the ISOBMFF sample entry spells
// it, case sensitive per section 3.3. Matroska carries no codecs
// parameter, and an MP4 whose upstream named no codec carries the bare
// type.
func composedContentType(form mediaForm, video bool, videoCodec string) string {
	if form.Extension != composedMP4Extension {
		return form.Type
	}
	if !video {
		return form.Type + `; codecs="` + composedAudioCodec + `"`
	}
	if videoCodec == "" {
		return form.Type
	}
	return form.Type + `; codecs="` + videoCodec + "," + composedAudioCodec + `"`
}

// openAll opens every upstream in its own goroutine and waits for all
// of them. They open at once because the offset is the difference of
// their header instants, and a request sent after another's headers
// arrived would fold that wait into the measurement. Any failure
// closes the streams that did open, because a body nobody reads holds
// a capture slot on its sidecar.
func (s *apiServer) openAll(ctx context.Context, screen string, tracks []string, headerTimeout time.Duration) ([]*upstreamStream, *upstreamFault) {
	targets := upstreamTargets(screen, tracks)
	names := make([]string, len(targets))
	for index := range names {
		names[index] = upstreamAudio
	}
	if screen != "" {
		names[0] = upstreamDisplay
	}
	streams := make([]*upstreamStream, len(targets))
	faults := make([]*upstreamFault, len(targets))
	var group sync.WaitGroup
	for index := range targets {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			streams[index], faults[index] = s.upstream.open(ctx, names[index], targets[index], headerTimeout)
		}(index)
	}
	group.Wait()
	for _, fault := range faults {
		if fault == nil {
			continue
		}
		for _, stream := range streams {
			stream.close()
		}
		return nil, fault
	}
	return streams, nil
}

// answerFault relays an upstream refusal: the mapped status, the
// upstream's own words as the detail, its Retry-After where it sent
// one, and its URL in the upstream member so the client can see
// which sibling refused.
func (e *apiExchange) answerFault(fault *upstreamFault) {
	if fault.retryAfter != "" {
		e.writer.Header().Set("Retry-After", fault.retryAfter)
	}
	document := newProblem(fault.kind, fault.status, fault.title, fault.detail, e.instance())
	document.Upstream = fault.upstream
	e.answerProblem(document)
}

// composeArguments builds the ffmpeg command line: one input per
// upstream body on an inherited descriptor, the offsets, the maps, and
// -c copy. For MP4, frag_keyframe starts a new fragment at each video
// keyframe, empty_moov puts the moov atom first so a client can start
// decoding before the file ends, and default_base_moof leaves the
// absolute base_data_offset out of each fragment so the fragments
// stand on their own; together they make a fragmented MP4 that plays
// from a pipe as it arrives. Matroska takes live instead, which
// writes the file on the assumption that it is a live stream.
func composeArguments(extension string, video bool, offsets []float64) []string {
	arguments := []string{"-nostdin", "-loglevel", "error"}
	descriptor := 3
	if video {
		arguments = append(arguments, "-f", "mp4", "-i", "pipe:3")
		descriptor++
	}
	// One -itsoffset per audio track, each measured against the
	// reference stream: the screen where there is one, and the first
	// sink where there is none. A positive offset delays that input by
	// the offset.
	for _, offset := range offsets {
		arguments = append(arguments,
			"-itsoffset", strconv.FormatFloat(offset, 'f', 6, 64),
			"-f", "ogg", "-i", "pipe:"+strconv.Itoa(descriptor))
		descriptor++
	}
	if video {
		arguments = append(arguments, "-map", "0:v:0")
	}
	for index := range offsets {
		arguments = append(arguments, "-map", strconv.Itoa(index+boolToInput(video))+":a:0")
	}
	arguments = append(arguments, "-c", "copy")
	if extension == composedMKVExtension {
		return append(arguments, "-live", "1", "-f", "matroska", "pipe:1")
	}
	return append(arguments,
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
}

// mux runs ffmpeg over the upstream bodies and copies its output to
// the response. Each body reaches ffmpeg down an inherited pipe,
// pipe:3 and up, rather than through a buffer in this process: ffmpeg
// reads each input at its own pace, and a pipe holds a burst on one
// input while ffmpeg waits on the other without this process holding
// either body in memory.
func (s *apiServer) mux(ctx context.Context, cancel context.CancelFunc, e *apiExchange, extension string, video bool, streams []*upstreamStream, offsets []float64, query captureQuery) error {
	command := exec.CommandContext(ctx, s.ffmpeg, composeArguments(extension, video, offsets)...)
	var idle []*atomic.Int64
	for _, stream := range streams {
		reader, writer, err := os.Pipe()
		if err != nil {
			return err
		}
		defer reader.Close()
		command.ExtraFiles = append(command.ExtraFiles, reader)
		mark := &atomic.Int64{}
		mark.Store(s.clock().UnixNano())
		idle = append(idle, mark)
		go feed(writer, stream.body, mark, s.clock)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &tailWriter{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return err
	}
	stop := s.watchIdle(ctx, cancel, idle, query)
	written, copyErr := io.Copy(&flushWriter{writer: e.writer}, stdout)
	stop()
	e.bytes = written
	// A copy that ended badly, such as a client that went away, ends
	// ffmpeg too. A muxer whose output pipe nobody reads blocks on the
	// write, and Wait would never return.
	if copyErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	tail := stderr.tail()
	failure := copyErr
	if failure == nil {
		failure = waitErr
	}
	// The stderr text goes beside the failure and never in its place.
	// ffmpeg writes lines that are not failures, such as the codec
	// frame size warning two Opus tracks produce, so the text alone
	// does not say whether the mux failed.
	switch {
	case failure != nil && tail != "":
		return fmt.Errorf("%w: %s", failure, tail)
	case failure != nil:
		return failure
	case tail != "":
		return fmt.Errorf("%s", tail)
	}
	return nil
}

// feed copies one upstream body into its pipe and stamps the mark on
// every read, for the idle watch. It closes the write end when the
// body ends, so ffmpeg reads an end of file on that input rather than
// waiting for bytes that will never come.
func feed(writer *os.File, body io.Reader, mark *atomic.Int64, clock func() time.Time) {
	defer writer.Close()
	buffer := make([]byte, 64<<10)
	for {
		read, err := body.Read(buffer)
		if read > 0 {
			mark.Store(clock().UnixNano())
			if _, writeErr := writer.Write(buffer[:read]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// watchIdle cancels the composition when any upstream body has sent
// nothing for upstreamIdleTimeout. The bound counts from the first
// body byte or from begin, whichever is later, so it never fires while
// a sidecar is still discarding frames or samples up to begin on its
// own clock.
func (s *apiServer) watchIdle(ctx context.Context, cancel context.CancelFunc, marks []*atomic.Int64, query captureQuery) func() {
	floor := s.clock().Add(time.Duration(query.Begin * float64(time.Second)))
	ticker := time.NewTicker(time.Second)
	done := make(chan struct{})
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				now := s.clock()
				if now.Before(floor) {
					continue
				}
				for _, mark := range marks {
					if now.Sub(time.Unix(0, mark.Load())) > upstreamIdleTimeout {
						cancel()
						return
					}
				}
			}
		}
	}()
	return func() { close(done) }
}

// flushWriter flushes the response after every write, so a chunk
// reaches the client as soon as ffmpeg writes it rather than when
// net/http's buffer fills. A live stream is watched as it arrives.
type flushWriter struct {
	writer http.ResponseWriter
}

func (f *flushWriter) Write(payload []byte) (int, error) {
	written, err := f.writer.Write(payload)
	_ = http.NewResponseController(f.writer).Flush()
	return written, err
}

// stderrTail bounds how much of ffmpeg's stderr the log line carries.
// An ffmpeg that fails on every packet writes a line per packet, and
// an unbounded copy would grow for the life of the stream. The writer
// keeps the last bytes ffmpeg wrote and drops the earlier ones,
// because the lines that end a stream say why it ended.
const stderrTail = 4 << 10

type tailWriter struct {
	kept []byte
}

func (t *tailWriter) Write(payload []byte) (int, error) {
	t.kept = append(t.kept, payload...)
	if len(t.kept) > stderrTail {
		t.kept = t.kept[len(t.kept)-stderrTail:]
	}
	return len(payload), nil
}

// tail answers what the writer kept.
func (t *tailWriter) tail() string {
	return strings.TrimSpace(string(t.kept))
}

// upstreamTargets lists the upstreams of one composition in the order
// ffmpeg reads them: the screen first where the unit has one, then
// each sink in spec order.
func upstreamTargets(screen string, tracks []string) []string {
	if screen == "" {
		return append([]string(nil), tracks...)
	}
	return append([]string{screen}, tracks...)
}

// boolToInput is the ffmpeg input index the first audio track takes:
// 1 where the unit has a screen on input 0, and 0 where it has none.
func boolToInput(video bool) int {
	if video {
		return 1
	}
	return 0
}
