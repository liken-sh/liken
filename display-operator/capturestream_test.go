package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWriteCroppedWritesOnlyTheCrop(t *testing.T) {
	// The frame is four pixels wide and three high. Each pixel is
	// one byte value repeated four times, so the row and column of
	// every pixel can be read from the bytes.
	frame := []byte{}
	for y := range 3 {
		for x := range 4 {
			pixel := byte(10*y + x)
			frame = append(frame, pixel, pixel, pixel, pixel)
		}
	}

	var piped bytes.Buffer
	writer := bufio.NewWriter(&piped)
	if err := writeCropped(writer, frame, 4*capturePixelBytes, cropRect{X: 1, Y: 1, W: 2, H: 2}); err != nil {
		t.Fatalf("writing the cropped frame: %v", err)
	}

	want := []byte{
		11, 11, 11, 11, 12, 12, 12, 12,
		21, 21, 21, 21, 22, 22, 22, 22,
	}
	if !bytes.Equal(piped.Bytes(), want) {
		t.Errorf("the pipe received %v, want %v", piped.Bytes(), want)
	}
}

func TestACropOutsideTheFrameIsReported(t *testing.T) {
	writer := bufio.NewWriter(&bytes.Buffer{})
	err := writeCropped(writer, make([]byte, 16), 4*capturePixelBytes, cropRect{W: 4, H: 4})
	if err == nil {
		t.Fatal("a crop past the end of the frame was written")
	}
}

func TestThePlanUsesTheCroppedSize(t *testing.T) {
	server := &captureServer{device: "/dev/dri/renderD128"}
	screen := captureScreen{Width: 3840, Height: 2160, PixelFormat: "bgr0"}
	chosen := captureSelection{Framerate: 15, Quality: 85, Width: 640}

	plan := server.plan("video/mp4", screen, cropRect{W: 1920, H: 1080}, chosen)

	if plan.width != 1920 || plan.height != 1080 {
		t.Errorf("the plan is %dx%d, want the size of the crop", plan.width, plan.height)
	}
	if plan.pixelFormat != "bgr0" {
		t.Errorf("the plan pixel format is %q, want the compositor's format", plan.pixelFormat)
	}
	if plan.scaleWidth != 640 || plan.scaleHeight != 0 || plan.framerate != 15 || plan.quality != 85 {
		t.Errorf("the plan is %+v, want the framerate, quality, and scale the request chose", plan)
	}
}

// The parser refuses width= and height= together, so a request sets
// one of them or neither.
func TestThePlanScalesToTheWidthOrTheHeight(t *testing.T) {
	cases := []struct {
		name         string
		chosen       captureSelection
		wantWidth    int
		wantHeight   int
		wantInFilter string
	}{
		{"a width", captureSelection{Framerate: 15, Width: 640}, 640, 0, "w=640:h=-2"},
		{"a height", captureSelection{Framerate: 15, Height: 540}, 0, 540, "h=540:w=-2"},
		{"neither", captureSelection{Framerate: 15}, 0, 0, "w=1920:h=-2"},
	}
	server := &captureServer{device: "/dev/dri/renderD128"}
	screen := captureScreen{Width: 3840, Height: 2160, PixelFormat: "bgr0"}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			plan := server.plan("video/mp4", screen, cropRect{W: 3840, H: 2160}, row.chosen)
			if plan.scaleWidth != row.wantWidth || plan.scaleHeight != row.wantHeight {
				t.Errorf("the plan scales to %dx%d, want %dx%d",
					plan.scaleWidth, plan.scaleHeight, row.wantWidth, row.wantHeight)
			}
			if command := strings.Join(plan.args(), " "); !strings.Contains(command, row.wantInFilter) {
				t.Errorf("the command is\n  %s\nwant it to include %s", command, row.wantInFilter)
			}
		})
	}
}

// copyingFfmpeg writes a stand-in for ffmpeg that ignores its options
// and copies stdin to stdout, so the response body holds the cropped
// frames.
func copyingFfmpeg(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ffmpeg"
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}
	return path
}

func TestAStillReachesTheBodyCropped(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureReply{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = copyingFfmpeg(t)
	defer func() { ffmpegProgram = held }()

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png?xywh=1,1,2,2", "the-api-token")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the capture responded with %d: %s", resp.StatusCode, body(t, resp))
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("the capture has Content-Type %q, want image/png", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("the capture has X-Content-Type-Options %q, want nosniff", got)
	}
	frame := capturePattern(1, 4*capturePixelBytes*3)
	want := append(append([]byte{}, frame[1*16+4:1*16+12]...), frame[2*16+4:2*16+12]...)
	if got := body(t, resp); got != string(want) {
		t.Errorf("the body has %v, want the region %v", []byte(got), want)
	}
}

// A denied capture has no Retry-After, because the compositor refuses
// a retry the same way.
func TestADeniedCaptureReturnsWestonsMessageAsDetail(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureReply{event: westonCaptureFailed, message: "unauthorized"})

	server := newCaptureFixture(t)
	server.socketPath = weston.path

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png", "the-api-token")

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("a denied capture responded with %d, want 500", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Errorf("a denial has Retry-After %q, and a retry never clears it", got)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatalf("decoding the problem document: %v", err)
	}
	if document.Type != problemCaptureDenied {
		t.Errorf("the problem type is %q, want %q", document.Type, problemCaptureDenied)
	}
	if document.Detail != "unauthorized" {
		t.Errorf("the detail is %q, want the compositor's message", document.Detail)
	}
}

// The compositor reports the refresh rate in millihertz, and the info
// document in whole hertz.
func TestTheSidecarReportsTheScreenMode(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 2, width: 3840, height: 2160, refresh: 59940}})

	server := newCaptureFixture(t)
	server.socketPath = weston.path

	resp := askSidecar(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1", "the-api-token")

	var info screenInfo
	if err := json.Unmarshal([]byte(body(t, resp)), &info); err != nil {
		t.Fatalf("decoding the info document: %v", err)
	}
	if info.Width != 3840 || info.Height != 2160 || info.Scale != 2 || info.Refresh != 60 {
		t.Errorf("the info document is %+v, want 3840x2160 at scale 2 and 60 Hz", info)
	}
	if len(info.Formats) != len(screenForms) {
		t.Errorf("the sidecar reports %v, want every form the aspect serves", info.Formats)
	}
}

func TestAClipEndsAtItsTEnd(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureReply{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = copyingFfmpeg(t)
	defer func() { ffmpegProgram = held }()

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.mp4?t=,0.1&framerate=30", "the-api-token")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the clip responded with %d: %s", resp.StatusCode, body(t, resp))
	}
	frame := 4 * capturePixelBytes * 3
	written := len(body(t, resp))
	if written == 0 || written%frame != 0 {
		t.Errorf("the clip has %d bytes, want whole frames of %d", written, frame)
	}
}

// The environment selects the software conversion graph for a node
// whose driver cannot upload bgr0 frames. The server cannot detect
// that itself, because it sends the status line before ffmpeg reads a
// frame and so cannot retry with another graph.
func TestTheConversionGraphIsNamedInTheEnvironment(t *testing.T) {
	server := &captureServer{}
	if server.usesSoftwareConversion() {
		t.Error("the software conversion graph runs by default, want the VA-API graph")
	}
	server.software = true
	plan := server.plan("video/mp4", captureScreen{Width: 1920, PixelFormat: "bgr0"}, cropRect{W: 1920, H: 1080}, captureSelection{Framerate: 15})
	if !plan.software {
		t.Error("the plan does not use the conversion graph that the node was set to run")
	}
}

// silentFfmpeg writes a stand-in for an ffmpeg whose filter graph the
// node's driver cannot build, as when scale_vaapi fails on a GPU
// without VA-API post-processing. It reads all its input, writes no
// output, and exits with an error.
func silentFfmpeg(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ffmpeg"
	// ffmpeg prints the cause first, then a note from the muxer and
	// a final message.
	script := "#!/bin/sh\ncat >/dev/null\n" +
		"echo '[Parsed_scale_vaapi_1 @ 0x1] Failed to create processing pipeline config: 12 (the requested VAProfile is not supported).' >&2\n" +
		"echo '[out#0/mp4] Nothing was written into output file, because at least one of its streams received no packets.' >&2\n" +
		"echo 'Conversion failed!' >&2\n" +
		"exit 251\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}
	return path
}

// The status line is sent before ffmpeg runs, so an encoder that
// writes no bytes cannot produce a problem document. The caller reads a
// truncated transfer and not a complete empty file. ffmpeg's last
// stderr line goes to the log, and display_capture_failures_total
// counts the failure.
func TestAnEncoderThatWroteNothingTruncatesTheBody(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureReply{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = silentFfmpeg(t)
	defer func() { ffmpegProgram = held }()

	sidecar := httptest.NewServer(server)
	defer sidecar.Close()

	resp, err := requestOverHTTP(t, sidecar, apiRoot+"/displays/HDMI-A-1/screen.mp4?t=,0.1&framerate=30")
	if err != nil {
		t.Fatalf("requesting the clip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the clip responded with %d, want 200 with a truncated body", resp.StatusCode)
	}
	read, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("the caller read %q to a clean end, want a truncated body", read)
	}
	if len(read) != 0 {
		t.Errorf("the caller read %d bytes, want none", len(read))
	}
}

// An encoder that cannot start produces a problem document, because
// the status line is not sent yet.
func TestAnEncoderThatCannotStartIsAFailure(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureReply{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = t.TempDir() + "/no-such-ffmpeg"
	defer func() { ffmpegProgram = held }()

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png", "the-api-token")

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("an encoder that could not start responded with %d, want 500", resp.StatusCode)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatalf("decoding the problem document: %v", err)
	}
	if document.Type != problemEncoderFailed {
		t.Errorf("the problem type is %q, want %q", document.Type, problemEncoderFailed)
	}
}

// requestOverHTTP sends a request over a real connection, because a
// recorder cannot tell a truncated body from a complete one.
func requestOverHTTP(t *testing.T, sidecar *httptest.Server, target string) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, sidecar.URL+target, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer the-api-token")
	return sidecar.Client().Do(request)
}

// truncatingFfmpeg writes a stand-in for an ffmpeg that writes some
// output and then fails.
func truncatingFfmpeg(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ffmpeg"
	script := "#!/bin/sh\nhead -c 16 >/dev/null\nprintf 'half a clip'\necho 'device lost' >&2\nexit 218\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}
	return path
}

func TestAFailureAfterTheStatusLineTruncatesTheBody(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureReply{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = truncatingFfmpeg(t)
	defer func() { ffmpegProgram = held }()

	sidecar := httptest.NewServer(server)
	defer sidecar.Close()

	request, err := http.NewRequest(http.MethodGet,
		sidecar.URL+apiRoot+"/displays/HDMI-A-1/screen.mp4?t=,0.3&framerate=30", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer the-api-token")
	resp, err := sidecar.Client().Do(request)
	if err != nil {
		t.Fatalf("requesting the clip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the clip responded with %d, want 200 before the failure", resp.StatusCode)
	}
	got, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("the caller read %q to a clean end, want a truncated body", got)
	}
	if string(got) != "half a clip" {
		t.Errorf("the caller read %q, want the bytes the encoder wrote", got)
	}
}

// exitingFfmpeg writes a stand-in for an ffmpeg that rejects its
// arguments and exits without reading its input.
func exitingFfmpeg(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ffmpeg"
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'Unrecognized option' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}
	return path
}

func TestAFailedEncoderDoesNotWaitForTheBegin(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureReply{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = exitingFfmpeg(t)
	defer func() { ffmpegProgram = held }()

	sidecar := httptest.NewServer(server)
	defer sidecar.Close()

	result := make(chan error, 1)
	go func() {
		resp, err := requestOverHTTP(t, sidecar, apiRoot+"/displays/HDMI-A-1/screen.png?t=30")
		if err != nil {
			result <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		_, err = io.ReadAll(resp.Body)
		result <- err
	}()

	select {
	case err := <-result:
		if err == nil {
			t.Error("the caller read a whole body from an encoder that never ran")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failure waited out the t= begin instead of ending the feed")
	}
}

// The headers leave before the t= begin and the body starts at it.
// stream's doc comment gives the reason.
func TestTheHeadersLeaveBeforeTheBegin(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureReply{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = copyingFfmpeg(t)
	defer func() { ffmpegProgram = held }()

	// The first reading of the server's clock is the origin of every
	// wait, so the test records it.
	accepted := make(chan time.Time, 1)
	server.now = func() time.Time {
		now := time.Now()
		select {
		case accepted <- now:
		default:
		}
		return now
	}

	sidecar := httptest.NewServer(server)
	defer sidecar.Close()

	const begin = 600 * time.Millisecond
	resp, err := requestOverHTTP(t, sidecar,
		apiRoot+"/displays/HDMI-A-1/screen.mp4?t=0.6,1.2&framerate=30")
	if err != nil {
		t.Fatalf("requesting the clip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	headersAt := time.Now()

	origin := <-accepted
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the clip responded with %d", resp.StatusCode)
	}
	if waited := headersAt.Sub(origin); waited >= begin {
		t.Errorf("the headers left %s after the accept, want well inside the %s beginning", waited, begin)
	}

	one := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, one); err != nil {
		t.Fatalf("reading the first body byte: %v", err)
	}
	firstByte := time.Since(origin)
	if firstByte < begin {
		t.Errorf("frame zero of the body arrived %s after the accept, want it at the %s beginning",
			firstByte, begin)
	}
}
