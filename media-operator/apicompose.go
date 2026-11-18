package main

// This file serves the composed aspect of a Player: its screen and its
// sound as one stream. The API requests screen.mp4 from the display API
// and audio.opus from the audio API for each sink, and passes the
// response bodies to one ffmpeg process that muxes them. The API sends
// all the requests at the same time, because it aligns the tracks by
// the difference between the times their response headers arrive, and
// a request sent later would add its delay to that difference. ffmpeg
// copies packets and never encodes, so the node that runs this API
// needs no GPU.

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

// composeLeadIn is how many seconds the API adds to both ends of the t=
// span that it sends upstream. Each upstream then has its capture
// running before its stream starts, so the header arrival times measure
// the offset between the tracks and not the startup time of each
// capture. The cost is that a composed stream is composeLeadIn seconds
// behind the present. composeLeadInWord is the same value as the
// discovery document writes it.
const (
	composeLeadIn     = 1.0
	composeLeadInWord = "1s"
)

// The composed aspect serves fragmented MP4, the default, and Matroska.
const (
	composedMP4Extension = "mp4"
	composedMKVExtension = "mkv"
)

// composedAudioCodec is the audio element of the codecs parameter. It
// is spelled "Opus" because that is the name of the Opus sample entry
// in MP4, and RFC 6381 section 3.3 makes codec names case sensitive.
// Browsers use lowercase "opus" from the MSE byte stream convention.
// The Composition section of the API reference explains the choice.
const composedAudioCodec = "Opus"

// serveComposed serves the composed aspect. The composition has a
// video input when `status.screen` names a Display, and an audio input
// for each sink while a `Play` runs. With neither, the response is a
// 409. Even with one input, this API muxes the stream itself and does
// not redirect, because a redirect would send the client to a sibling's
// in-cluster name, which a client outside the cluster cannot reach.
func (s *apiServer) serveComposed(e *apiExchange, player *Player, form mediaForm, query captureQuery, negotiated bool) {
	monitor := rememberedMonitor(player)
	sinks := tappableSinks(player)
	if monitor == "" && len(sinks) == 0 {
		e.fail(problemNotPlaying, http.StatusConflict, "This Player plays nothing",
			notPlayingDetail(player, mediaAspectName))
		return
	}

	// serveCapture checked the caller only for `players/media`. The
	// upstream requests use this operator's own token, so the caller
	// needs no permission on `displays/screen`.
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
		e.writeStatus(http.StatusOK)
		return
	}
	s.streamComposed(e, player, form, query, screen, tracks, negotiated)
}

// composedHeaders sets the headers of a composed 200. Accept-Ranges is
// none because two requests for the same URI return different bytes.
// A HEAD gets the same headers except the codecs parameter of
// Content-Type, because a HEAD makes no upstream request and the codecs
// come from the display upstream's response. RFC 9110 section 9.3.2
// allows a HEAD to omit a header that is known only while generating
// the content.
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

// captureFilename replaces each colon of the time with a hyphen,
// because some file systems do not allow a colon in a file name.
func captureFilename(namespace, name, extension string, at time.Time) string {
	stamp := strings.ReplaceAll(at.UTC().Format(time.RFC3339), ":", "-")
	return namespace + "-" + name + "-" + stamp + "." + extension
}

// streamComposed runs one composition. A failure before the headers
// are sent returns a problem document. A failure after that can only
// end the body early, so the client receives a truncated stream and the
// request log line records the error.
func (s *apiServer) streamComposed(e *apiExchange, player *Player, form mediaForm, query captureQuery, screen string, tracks []string, negotiated bool) {
	release, reserved := s.takeComposition()
	if !reserved {
		e.writer.Header().Set("Retry-After", retryAfterSeconds)
		document := newProblem(problemCaptureBusy, http.StatusServiceUnavailable,
			"This API is already running its limit of compositions",
			"the limit of "+strconv.Itoa(s.maxCompositions)+" compositions at once is reached", e.instance())
		e.writeProblemDocument(document)
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
		e.respondUpstreamFault(fault)
		return
	}
	defer func() {
		for _, stream := range streams {
			stream.close()
		}
	}()

	// Each audio offset is measured against streams[0]: the screen when
	// there is one, otherwise the first sink. With no screen, the first
	// sink's offset against itself is always zero, so the metric does
	// not record it.
	first := firstAudioInput(screen != "")
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

	// The headers are set only after every upstream has responded, so an
	// upstream refusal above returns the problem document with no
	// Content-Disposition and no Link to a stream that never started.
	e.composedHeaders(form, screen, tracks, negotiated)
	e.writer.Header().Set("Content-Type", composedContentType(form, screen != "", streams[0].codecs))
	done := s.metrics.trackStream(mediaAspectName)
	defer done()
	s.recordCapture(e, player, mediaAspectName, form.Type)
	e.writeStreamingStatus(http.StatusOK)

	if err := s.mux(ctx, cancel, e, form.Extension, screen != "", streams, offsets, query); err != nil {
		e.ffmpeg = err.Error()
	}
}

// composedContentType adds an RFC 6381 codecs parameter to MP4 only.
// When the display upstream sent no codecs, the result has none,
// because the parameter lists every codec of the content and this API
// does not guess the video codec.
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

// openAll opens every upstream at the same time, for the reason the
// file comment gives. When any upstream fails, openAll closes the
// streams that did open, because a response body that nobody reads
// keeps a capture reserved on its sidecar.
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

// respondUpstreamFault puts the upstream's URL in the problem
// document's upstream member, so the client can see which sibling
// refused.
func (e *apiExchange) respondUpstreamFault(fault *upstreamFault) {
	if fault.retryAfter != "" {
		e.writer.Header().Set("Retry-After", fault.retryAfter)
	}
	document := newProblem(fault.kind, fault.status, fault.title, fault.detail, e.instance())
	document.Upstream = fault.upstream
	e.writeProblemDocument(document)
}

// composeArguments builds the ffmpeg command line. Each upstream body
// is an input on an inherited file descriptor, starting at 3. For MP4
// the movflags make a fragmented MP4 that a client can play while it
// arrives: frag_keyframe starts a fragment at each video keyframe,
// empty_moov writes the moov atom first so decoding can start before
// the file ends, and default_base_moof makes each fragment's offsets
// relative to itself. For Matroska, -live 1 tells the muxer that the
// output is a live stream.
func composeArguments(extension string, video bool, offsets []float64) []string {
	arguments := []string{"-nostdin", "-loglevel", "error"}
	descriptor := 3
	if video {
		arguments = append(arguments, "-f", "mp4", "-i", "pipe:3")
		descriptor++
	}
	// A positive -itsoffset delays that input.
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
		arguments = append(arguments, "-map", strconv.Itoa(index+firstAudioInput(video))+":a:0")
	}
	arguments = append(arguments, "-c", "copy")
	if extension == composedMKVExtension {
		return append(arguments, "-live", "1", "-f", "matroska", "pipe:1")
	}
	return append(arguments,
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
}

// mux gives each upstream body its own OS pipe, which ffmpeg reads at
// its own pace. A burst on one input waits in its pipe buffer while
// ffmpeg reads another, so this process never holds a whole body in
// memory.
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
		go copyToPipe(writer, stream.body, mark, s.clock)
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
	// When the copy to the client fails, for example because the client
	// disconnected, nothing reads ffmpeg's output any more. ffmpeg would
	// block on its next write and Wait would never return, so stop it.
	if copyErr != nil {
		cancel()
	}
	waitErr := command.Wait()
	tail := stderr.tail()
	failure := copyErr
	if failure == nil {
		failure = waitErr
	}
	// ffmpeg also writes warnings to stderr, so the stderr text is added
	// to the error and never decides whether the mux failed.
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

// copyToPipe stores the time of each read in mark, for watchIdle. It
// closes the pipe when the body ends or fails, so ffmpeg reads end of
// file on that input and does not wait for more bytes.
func copyToPipe(writer *os.File, body io.Reader, mark *atomic.Int64, clock func() time.Time) {
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

// watchIdle cancels the composition when any upstream body has sent no
// bytes for upstreamIdleTimeout. It does not check before the begin
// time of the span, because until then a sidecar discards what it
// captures and sends nothing. Each mark starts when mux creates the
// pipe, so a body that is still silent at begin counts as idle since
// then. It returns a function that stops the watch.
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

// flushWriter flushes the response after every write, so each chunk
// reaches the client when ffmpeg writes it and not when the net/http
// buffer fills.
type flushWriter struct {
	writer http.ResponseWriter
}

func (f *flushWriter) Write(payload []byte) (int, error) {
	written, err := f.writer.Write(payload)
	_ = http.NewResponseController(f.writer).Flush()
	return written, err
}

// stderrTail is the most bytes of ffmpeg's stderr that the request log
// line includes. An ffmpeg that fails on every packet writes a line for
// each one, so the whole stderr grows for as long as the stream runs.
// tailWriter keeps the last bytes, because the final lines say why the
// stream ended.
const stderrTail = 4 << 10

type tailWriter struct {
	buffer []byte
}

func (t *tailWriter) Write(payload []byte) (int, error) {
	t.buffer = append(t.buffer, payload...)
	if len(t.buffer) > stderrTail {
		t.buffer = t.buffer[len(t.buffer)-stderrTail:]
	}
	return len(payload), nil
}

func (t *tailWriter) tail() string {
	return strings.TrimSpace(string(t.buffer))
}

// upstreamTargets lists the upstream URLs of one composition in the
// order of the ffmpeg inputs: the screen first when the Player has one,
// then each sink in the order of `status.sinks`.
func upstreamTargets(screen string, tracks []string) []string {
	if screen == "" {
		return append([]string(nil), tracks...)
	}
	return append([]string{screen}, tracks...)
}

// firstAudioInput returns the ffmpeg input index of the first audio
// track, which follows the video input when there is one.
func firstAudioInput(video bool) int {
	if video {
		return 1
	}
	return 0
}
