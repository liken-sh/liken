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

// The pipe carries the region and nothing else, which is the whole
// of what a crop costs.
func TestTheCropIsWhatThePipeCarries(t *testing.T) {
	// The frame is four pixels wide and three high, each pixel one
	// byte repeated four times, so a row and a column are readable
	// in the bytes.
	frame := []byte{}
	for y := range 3 {
		for x := range 4 {
			pixel := byte(10*y + x)
			frame = append(frame, pixel, pixel, pixel, pixel)
		}
	}

	var held bytes.Buffer
	writer := bufio.NewWriter(&held)
	if err := writeCropped(writer, frame, 4*capturePixelBytes, cropRect{X: 1, Y: 1, W: 2, H: 2}); err != nil {
		t.Fatal(err)
	}

	want := []byte{
		11, 11, 11, 11, 12, 12, 12, 12,
		21, 21, 21, 21, 22, 22, 22, 22,
	}
	if !bytes.Equal(held.Bytes(), want) {
		t.Errorf("the pipe carried %v, want %v", held.Bytes(), want)
	}
}

// A crop the frame cannot answer is reported rather than read past
// the end of the buffer.
func TestACropOutsideTheFrameIsReported(t *testing.T) {
	writer := bufio.NewWriter(&bytes.Buffer{})
	err := writeCropped(writer, make([]byte, 16), 4*capturePixelBytes, cropRect{W: 4, H: 4})
	if err == nil {
		t.Fatal("a crop past the end of the frame was written")
	}
}

// The plan a request becomes carries the cropped size, not the
// screen's, because the pipe carries the crop.
func TestThePlanCarriesTheCroppedSize(t *testing.T) {
	server := &captureServer{device: "/dev/dri/renderD128"}
	screen := captureScreen{Width: 3840, Height: 2160, PixelFormat: "bgr0"}
	chosen := captureSelection{Framerate: 15, Quality: 85, Width: 640}

	plan := server.plan("video/mp4", screen, cropRect{W: 1920, H: 1080}, chosen)

	if plan.width != 1920 || plan.height != 1080 {
		t.Errorf("the plan states %dx%d, want the crop's own size", plan.width, plan.height)
	}
	if plan.pixelFormat != "bgr0" {
		t.Errorf("the plan states %q, want the compositor's own format", plan.pixelFormat)
	}
	if plan.scaleWidth != 640 || plan.scaleHeight != 0 || plan.framerate != 15 || plan.quality != 85 {
		t.Errorf("the plan states %+v, want the caller's own knobs", plan)
	}
}

// Both scale knobs reach the encoder. The parser refuses the two
// together, so a request carries one axis or neither, and whichever
// one it carries is the one the command states.
func TestThePlanCarriesEitherScaleKnob(t *testing.T) {
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
				t.Errorf("the command is\n  %s\nwant it to carry %s", command, row.wantInFilter)
			}
		})
	}
}

// This program stands in for ffmpeg in a drill. It ignores every
// option and copies the frames to the body, so the bytes a caller
// reads are the bytes the compositor gave.
func copyingProgram(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ffmpeg"
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// One capture from the compositor's own socket to the body of the
// response, with the region cut out on the way.
func TestAStillReachesTheBodyCropped(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureAnswer{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = copyingProgram(t)
	defer func() { ffmpegProgram = held }()

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png?xywh=1,1,2,2", "the-api-token")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the capture answered %d: %s", resp.StatusCode, body(t, resp))
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("the capture is typed %q", got)
	}
	frame := capturePattern(1, 4*capturePixelBytes*3)
	want := append(append([]byte{}, frame[1*16+4:1*16+12]...), frame[2*16+4:2*16+12]...)
	if got := body(t, resp); got != string(want) {
		t.Errorf("the body carried %v, want the region %v", []byte(got), want)
	}
}

// A compositor that refuses the capture answers with its own word,
// and the word reaches the caller as the 500's detail, with no
// Retry-After because a retry never clears it.
func TestADeniedCaptureCarriesWestonsWord(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureAnswer{event: westonCaptureFailed, message: "unauthorized"})

	server := newCaptureFixture(t)
	server.socketPath = weston.path

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png", "the-api-token")

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("a denied capture answered %d, want 500", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Errorf("a denial carries Retry-After %q, and a retry never clears it", got)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatal(err)
	}
	if document.Type != problemCaptureDenied {
		t.Errorf("the problem type is %q, want %q", document.Type, problemCaptureDenied)
	}
	if document.Detail != "unauthorized" {
		t.Errorf("the detail is %q, want the compositor's own word", document.Detail)
	}
}

// The sidecar's info answer is the numbers the compositor states
// about the screen, with the refresh in whole hertz.
func TestTheSidecarReportsTheScreensNumbers(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 2, width: 3840, height: 2160, refresh: 59940}})

	server := newCaptureFixture(t)
	server.socketPath = weston.path

	resp := askSidecar(t, server, http.MethodGet, apiRoot+"/displays/HDMI-A-1", "the-api-token")

	var info screenInfo
	if err := json.Unmarshal([]byte(body(t, resp)), &info); err != nil {
		t.Fatal(err)
	}
	if info.Width != 3840 || info.Height != 2160 || info.Scale != 2 || info.Refresh != 60 {
		t.Errorf("the sidecar reports %+v", info)
	}
	if len(info.Formats) != len(screenForms) {
		t.Errorf("the sidecar reports %v, want every form the aspect serves", info.Formats)
	}
}

// A clip ends at the t= end the caller asked for, and every frame in
// the body is the whole region.
func TestAClipEndsAtTheEndItWasAskedFor(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureAnswer{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = copyingProgram(t)
	defer func() { ffmpegProgram = held }()

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.mp4?t=,0.1&framerate=30", "the-api-token")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the clip answered %d: %s", resp.StatusCode, body(t, resp))
	}
	frame := 4 * capturePixelBytes * 3
	written := len(body(t, resp))
	if written == 0 || written%frame != 0 {
		t.Errorf("the clip carried %d bytes, want whole frames of %d", written, frame)
	}
}

// The node that refuses a bgr0 upload is named in the environment,
// because the status line is on the wire before ffmpeg has read a
// frame and no retry can follow it.
func TestTheConversionGraphIsNamedInTheEnvironment(t *testing.T) {
	server := &captureServer{}
	if server.usesSoftwareConversion() {
		t.Error("the fallback graph runs by default")
	}
	server.software = true
	plan := server.plan("video/mp4", captureScreen{Width: 1920, PixelFormat: "bgr0"}, cropRect{W: 1920, H: 1080}, captureSelection{Framerate: 15})
	if !plan.software {
		t.Error("the plan does not carry the graph the node was told to run")
	}
}

// This program stands in for an encoder whose graph the node's
// driver cannot build: it reads its input to the end and writes
// nothing, which is what ffmpeg does when scale_vaapi fails to
// configure on a chip with no VA-API post-processing.
func silentProgram(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ffmpeg"
	// The three lines ffmpeg ends such a run with: the cause, the
	// muxer's note, and the epilogue. The detail must carry the
	// first of them.
	script := "#!/bin/sh\ncat >/dev/null\n" +
		"echo '[Parsed_scale_vaapi_1 @ 0x1] Failed to create processing pipeline config: 12 (the requested VAProfile is not supported).' >&2\n" +
		"echo '[out#0/mp4] Nothing was written into output file, because at least one of its streams received no packets.' >&2\n" +
		"echo 'Conversion failed!' >&2\n" +
		"exit 251\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// An encoder that writes no byte ends the response without its
// terminating chunk, so a caller reads a truncated transfer and not
// a whole file that is empty. The status line left with the first
// frame, which is what keeps this API's zero at the accept instant,
// so a problem document is no longer available by the time ffmpeg
// has failed; the log and display_capture_failures_total carry
// ffmpeg's own words instead.
func TestAnEncoderThatWroteNothingTruncatesTheBody(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureAnswer{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = silentProgram(t)
	defer func() { ffmpegProgram = held }()

	sidecar := httptest.NewServer(server)
	defer sidecar.Close()

	resp, err := askOverTheWire(t, sidecar, apiRoot+"/displays/HDMI-A-1/screen.mp4?t=,0.1&framerate=30")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the clip answered %d, want 200 with a truncated body", resp.StatusCode)
	}
	read, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("the caller read %q to a clean end, want a truncated body", read)
	}
	if len(read) != 0 {
		t.Errorf("the caller read %d bytes, want none", len(read))
	}
}

// An encoder that cannot be started at all is still a problem
// document, because nothing has reached the wire yet.
func TestAnEncoderThatCannotStartIsAFailure(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureAnswer{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = t.TempDir() + "/no-such-ffmpeg"
	defer func() { ffmpegProgram = held }()

	resp := askSidecar(t, server, http.MethodGet,
		apiRoot+"/displays/HDMI-A-1/screen.png", "the-api-token")

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("an encoder that could not start answered %d, want 500", resp.StatusCode)
	}
	var document problemDocument
	if err := json.Unmarshal([]byte(body(t, resp)), &document); err != nil {
		t.Fatal(err)
	}
	if document.Type != problemEncoderFailed {
		t.Errorf("the problem type is %q, want %q", document.Type, problemEncoderFailed)
	}
}

// One request to a sidecar over a real connection, which is how a
// truncated body is told from a whole one.
func askOverTheWire(t *testing.T, sidecar *httptest.Server, target string) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, sidecar.URL+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer the-api-token")
	return sidecar.Client().Do(request)
}

// This program writes one block and then dies, which is an encoder
// that failed after its status line went out.
func halfProgram(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ffmpeg"
	script := "#!/bin/sh\nhead -c 16 >/dev/null\nprintf 'half a clip'\necho 'device lost' >&2\nexit 218\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// An encoder that failed after its status line went out ends the
// body without its terminating chunk, so the caller reads an
// unexpected end of file instead of a whole file that is not whole.
func TestAFailureAfterTheStatusLineTruncatesTheBody(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureAnswer{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = halfProgram(t)
	defer func() { ffmpegProgram = held }()

	sidecar := httptest.NewServer(server)
	defer sidecar.Close()

	request, err := http.NewRequest(http.MethodGet,
		sidecar.URL+apiRoot+"/displays/HDMI-A-1/screen.mp4?t=,0.3&framerate=30", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer the-api-token")
	resp, err := sidecar.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the clip answered %d, want 200 before the failure", resp.StatusCode)
	}
	held2, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("the caller read %q to a clean end, want a truncated body", held2)
	}
	if string(held2) != "half a clip" {
		t.Errorf("the caller read %q, want the bytes the encoder wrote", held2)
	}
}

// This program stands in for an ffmpeg that refused its arguments:
// it exits without reading a byte of its input, so nothing but the
// request's own end will wake a feed that is waiting out a t= begin.
func deadProgram(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ffmpeg"
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'Unrecognized option' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A still that was asked to wait out a t= begin does not hold the
// failure of an encoder that died at once. The feed is sleeping on
// its own context, and the end of the encoder's body ends that
// context rather than waiting a minute for the sleeper. The caller
// reads a truncated body at once instead of a whole one in half a
// minute.
func TestAFailedEncoderDoesNotWaitOutTheBeginning(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureAnswer{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = deadProgram(t)
	defer func() { ffmpegProgram = held }()

	sidecar := httptest.NewServer(server)
	defer sidecar.Close()

	answered := make(chan error, 1)
	go func() {
		resp, err := askOverTheWire(t, sidecar, apiRoot+"/displays/HDMI-A-1/screen.png?t=30")
		if err != nil {
			answered <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		_, err = io.ReadAll(resp.Body)
		answered <- err
	}()

	select {
	case err := <-answered:
		if err == nil {
			t.Error("the caller read a whole body from an encoder that never ran")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failure waited out the t= begin instead of ending the feed")
	}
}

// The zero of every time code in this API is the instant the request
// was accepted, and the response headers are how a caller reads that
// instant. They leave within one frame of the accept whatever t=
// asks for, and the body then starts at the beginning the caller
// named. media-api composes this stream with sound by comparing the
// two APIs' header instants, so headers that waited out a lead-in
// would read as a clock difference and pull the composed stream out
// of sync by the length of the lead-in.
func TestTheHeadersLeaveBeforeTheBeginning(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24,
		[]captureWestonOutput{{connector: "HDMI-A-1", scale: 1, width: 4, height: 3, refresh: 60000}},
		captureAnswer{event: westonCaptureComplete, seed: 1})

	server := newCaptureFixture(t)
	server.socketPath = weston.path
	held := ffmpegProgram
	ffmpegProgram = copyingProgram(t)
	defer func() { ffmpegProgram = held }()

	// The server reads its own clock once, at the accept, and every
	// wait is measured from what it read. The drill reads the same
	// clock, so the two agree on where the origin is.
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
	resp, err := askOverTheWire(t, sidecar,
		apiRoot+"/displays/HDMI-A-1/screen.mp4?t=0.6,1.2&framerate=30")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	headersAt := time.Now()

	origin := <-accepted
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the clip answered %d", resp.StatusCode)
	}
	if waited := headersAt.Sub(origin); waited >= begin {
		t.Errorf("the headers left %s after the accept, want well inside the %s beginning", waited, begin)
	}

	one := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, one); err != nil {
		t.Fatal(err)
	}
	firstByte := time.Since(origin)
	if firstByte < begin {
		t.Errorf("frame zero of the body arrived %s after the accept, want it at the %s beginning",
			firstByte, begin)
	}
}
