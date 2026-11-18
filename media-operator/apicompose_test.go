package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

func TestComposeArgumentsSetAnOffsetForEachAudioTrack(t *testing.T) {
	got := composeArguments(composedMP4Extension, true, []float64{0.04, -0.012})

	want := []string{
		"-nostdin", "-loglevel", "error",
		"-f", "mp4", "-i", "pipe:3",
		"-itsoffset", "0.040000", "-f", "ogg", "-i", "pipe:4",
		"-itsoffset", "-0.012000", "-f", "ogg", "-i", "pipe:5",
		"-map", "0:v:0", "-map", "1:a:0", "-map", "2:a:0",
		"-c", "copy",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("arguments = %q, want %q", got, want)
	}
}

func TestComposeArgumentsWriteMatroskaLive(t *testing.T) {
	got := composeArguments(composedMKVExtension, true, []float64{0})

	mustMatch(t, strings.Join(got[len(got)-7:], " "), "-c copy -live 1 -f matroska pipe:1")
}

func TestTheComposedContentTypeAddsOpusToTheUpstreamsVideoCodecs(t *testing.T) {
	rows := []struct {
		name  string
		form  mediaForm
		video bool
		codec string
		want  string
	}{
		{"mp4 with a profile", composedForms[0], true, "avc1.640029", `video/mp4; codecs="avc1.640029,Opus"`},
		{"mp4 with several video elements", composedForms[0], true, "avc1.640029,avc1.42c00c",
			`video/mp4; codecs="avc1.640029,avc1.42c00c,Opus"`},
		{"mp4 with no profile", composedForms[0], true, "", "video/mp4"},
		{"mp4 with no screen", composedForms[0], false, "", `video/mp4; codecs="Opus"`},
		{"matroska", composedForms[1], true, "avc1.64001f", "video/matroska"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			mustMatch(t, composedContentType(row.form, row.video, row.codec), row.want)
		})
	}
}

// The display API chose the codecs that it encoded, so this API copies
// the codecs parameter unchanged and never fills in a missing one.
func TestTheCodecsParameterIsCopiedUnchangedFromTheUpstream(t *testing.T) {
	rows := []struct {
		field string
		want  string
	}{
		{`video/mp4; codecs="avc1.640029"`, "avc1.640029"},
		{`video/mp4; codecs="avc1.4d401e,mp4a.40.2"`, "avc1.4d401e,mp4a.40.2"},
		{"video/mp4", ""},
		{"not a media type", ""},
	}
	for _, row := range rows {
		t.Run(row.field, func(t *testing.T) {
			mustMatch(t, upstreamCodecs(row.field), row.want)
		})
	}
}

// The -itsoffset passed to ffmpeg is the audio header arrival time
// minus the screen header arrival time, measured on this API's clock.
func TestTheOffsetIsTheDifferenceOfTheHeaderArrivalTimes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 1, true)
		arguments := filepath.Join(t.TempDir(), "arguments")
		fixture.server.ffmpeg = recordingFFmpeg(t, arguments)
		fixture.server.now = time.Now

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusOK)
		written, err := os.ReadFile(arguments)
		mustSucceed(t, err)
		mustContain(t, strings.Split(strings.TrimSpace(string(written)), "\n"), "0.040000")
		mustMatch(t, fixture.lines[0].OffsetSeconds, 0.04)
	})
}

// recordingFFmpeg returns the path of a script that replaces ffmpeg.
// It writes its arguments to the file arguments, reads the two input
// pipes to the end so the copies into them finish, and prints a few
// bytes so the response has a body.
func recordingFFmpeg(t *testing.T, arguments string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + arguments +
		"\ncat <&3 > /dev/null\ncat <&4 > /dev/null\nprintf muxed\n"
	mustSucceed(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func TestAnUpstreamResponseMapsToThisAPIsStatus(t *testing.T) {
	rows := []struct {
		name        string
		status      int
		contentType string
		body        string
		retryAfter  string
		want        int
		kind        string
		detail      string
		relayed     string
	}{
		{
			name: "a refused query", status: http.StatusBadRequest,
			contentType: problemContentType,
			body:        `{"type":"about:blank","title":"Bad Request","status":400,"detail":"t=5,3 ends before it begins"}`,
			want:        http.StatusBadRequest, kind: aboutBlank, detail: "t=5,3 ends before it begins",
		},
		{
			name: "no such Display", status: http.StatusNotFound,
			contentType: problemContentType,
			body:        `{"type":"about:blank","title":"Not Found","status":404,"detail":"no Display named boe-1080"}`,
			want:        http.StatusNotFound, kind: aboutBlank, detail: "no Display named boe-1080",
		},
		{
			name: "a busy capture", status: http.StatusServiceUnavailable,
			contentType: problemContentType, retryAfter: "9",
			body: `{"type":"https://liken.sh/problems/capture-busy","title":"Busy","status":503,"detail":"the output is being captured"}`,
			want: http.StatusServiceUnavailable, kind: problemCaptureBusy,
			detail: "the output is being captured", relayed: "9",
		},
		{
			name: "a screen with no panel", status: http.StatusConflict,
			contentType: problemContentType,
			body:        `{"type":"https://liken.sh/problems/away","title":"The screen is away","status":409,"detail":"no panel on HDMI-A-1"}`,
			want:        http.StatusConflict, kind: problemAway, detail: "no panel on HDMI-A-1",
		},
		{
			name: "a compositor that denied the capture", status: http.StatusInternalServerError,
			contentType: problemContentType,
			body:        `{"type":"https://liken.sh/display/problems/capture-denied","title":"Denied","status":500,"detail":"unauthorized"}`,
			want:        http.StatusBadGateway, kind: problemUpstreamFailed, detail: "unauthorized",
		},
		{
			name: "an answer that is not a problem document", status: http.StatusBadGateway,
			contentType: "text/html", body: "<h1>502 Bad Gateway</h1>",
			want: http.StatusBadGateway, kind: problemUpstreamFailed,
			detail: "502 Bad Gateway <h1>502 Bad Gateway</h1>",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newAPIFixture(t)
				fixture.display.status = row.status
				fixture.display.contentType = row.contentType
				fixture.display.body = []byte(row.body)
				fixture.display.retryAfter = row.retryAfter

				recorder := fixture.get(playerPathFor("media.mp4"))

				mustMatch(t, recorder.Code, row.want)
				document := problemOf(t, recorder)
				mustMatch(t, document.Type, row.kind)
				mustMatch(t, document.Detail, row.detail)
				mustMatch(t, strings.HasPrefix(document.Upstream, fixture.display.url), true)
				mustMatch(t, recorder.Header().Get("Retry-After"), row.relayed)
			})
		})
	}
}

// A refused connection is a 503 with Retry-After, because a sibling
// that is restarting will accept connections again, so a retry can
// succeed.
func TestAnUnreachableUpstreamIsAServiceUnavailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.display = "http://127.0.0.1:1"

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusServiceUnavailable)
		mustMatch(t, recorder.Header().Get("Retry-After"), retryAfterSeconds)
	})
}

// When this API is already running its maximum number of compositions,
// it returns a 503 with the capture-busy problem type and Retry-After,
// the same response a busy sidecar gives.
func TestTheCompositionLimitReturnsCaptureBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.compositions = make(chan struct{}, 1)
		fixture.server.maxCompositions = 1
		fixture.server.compositions <- struct{}{}

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusServiceUnavailable)
		mustMatch(t, recorder.Header().Get("Retry-After"), retryAfterSeconds)
		mustMatch(t, problemOf(t, recorder).Type, problemCaptureBusy)
		mustMatch(t, len(fixture.display.made()), 0)
	})
}

// A capture that produces bytes posts a Captured Event on the Player,
// so `kubectl describe player` shows who captured it and when.
func TestACaptureWritesTheCapturedEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))

		fixture.get(playerPathFor("media.mp4"))
		synctest.Wait()

		posted := fixture.events.About("Player", testAPIPlayer)
		mustMatch(t, len(posted), 1)
		mustMatch(t, posted[0].Reason, reasonCaptured)
		mustMatch(t, posted[0].Type, "Normal")
		mustMatch(t, posted[0].Message,
			testAPISubject+" took the media of "+testAPIPlayer+" as video/mp4")
	})
}

func TestARealMuxOutputsOneVideoTrackAndTwoAudioTracks(t *testing.T) {
	requireFFmpeg(t)
	directory := t.TempDir()
	video := fixtureVideo(t, directory)
	first := fixtureAudio(t, directory, "first.opus", 440)
	second := fixtureAudio(t, directory, "second.opus", 660)

	fixture := newAPIFixture(t)
	fixture.server.now = time.Now
	fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 2, true)
	fixture.display.body = video
	fixture.display.contentType = `video/mp4; codecs="avc1.640029"`
	fixture.audio.contentType = "audio/ogg"
	fixture.audio.bodies = map[string][]byte{testAPISink: first, testAPISecond: second}

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusOK)
	mustMatch(t, recorder.Header().Get("Content-Type"), `video/mp4; codecs="avc1.640029,Opus"`)
	composed := filepath.Join(directory, "composed.mp4")
	mustSucceed(t, os.WriteFile(composed, recorder.Body.Bytes(), 0o644))
	mustMatchAll(t, probeTracks(t, composed), []string{"video", "audio", "audio"})
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("this test muxes two real streams and reads the result back; %s is not installed", tool)
		}
	}
}

// fixtureVideo returns a short fragmented MP4, the form the display
// API serves. ffmpeg reads its input from a pipe, which cannot seek,
// so it cannot read a plain MP4 that has its moov atom at the end.
func fixtureVideo(t *testing.T, directory string) []byte {
	t.Helper()
	path := filepath.Join(directory, "screen.mp4")
	run(t, "ffmpeg", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=160x120:rate=15:duration=2",
		"-c:v", "libx264", "-profile:v", "baseline", "-pix_fmt", "yuv420p", "-g", "15",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", path)
	body, err := os.ReadFile(path)
	mustSucceed(t, err)
	return body
}

func fixtureAudio(t *testing.T, directory, name string, tone int) []byte {
	t.Helper()
	path := filepath.Join(directory, name)
	run(t, "ffmpeg", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency="+strconv.Itoa(tone)+":duration=2",
		"-c:a", "libopus", "-f", "ogg", path)
	body, err := os.ReadFile(path)
	mustSucceed(t, err)
	return body
}

func probeTracks(t *testing.T, path string) []string {
	t.Helper()
	output := run(t, "ffprobe", "-v", "error", "-show_streams", "-print_format", "json", path)
	var probed struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	mustSucceed(t, json.Unmarshal(output, &probed))
	kinds := make([]string, len(probed.Streams))
	for index, stream := range probed.Streams {
		kinds[index] = stream.CodecType
	}
	return kinds
}

func run(t *testing.T, name string, arguments ...string) []byte {
	t.Helper()
	command := exec.Command(name, arguments...)
	var failure strings.Builder
	command.Stderr = &failure
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(arguments, " "), err, failure.String())
	}
	return output
}

// On the fake clock of the synctest bubble, the 504 arrives exactly
// when upstreamHeaderTimeout ends.
func TestAnUpstreamThatSendsNoHeadersInTimeIsAGatewayTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.display.delay = 2 * upstreamHeaderTimeout
		began := time.Now()

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusGatewayTimeout)
		mustMatch(t, problemOf(t, recorder).Type, aboutBlank)
		mustMatch(t, time.Since(began), upstreamHeaderTimeout)
	})
}

func TestASoundOnlyCompositionHasNoVideoInput(t *testing.T) {
	got := composeArguments(composedMP4Extension, false, []float64{0, 0.02})

	want := []string{
		"-nostdin", "-loglevel", "error",
		"-itsoffset", "0.000000", "-f", "ogg", "-i", "pipe:3",
		"-itsoffset", "0.020000", "-f", "ogg", "-i", "pipe:4",
		"-map", "0:a:0", "-map", "1:a:0",
		"-c", "copy",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("arguments = %q, want %q", got, want)
	}
}

func TestTwoSinksAndNoScreenComposeTheSoundAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.now = time.Now
		fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))
		fixture.plane.players[testAPIPlayer] = shapedPlayer(false, 2, true)

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusOK)
		mustMatch(t, recorder.Header().Get("Content-Type"), `video/mp4; codecs="Opus"`)
		mustMatch(t, len(fixture.display.made()), 0)
		mustMatch(t, len(fixture.audio.made()), 2)
		for _, link := range recorder.Header().Values("Link") {
			if strings.Contains(link, relScreen) {
				t.Errorf("the response has the Link %s, want none with rel %s because the composition has no screen", link, relScreen)
			}
		}
	})
}

func TestARefusedCompositionHasNoCaptureHeaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.compositions = make(chan struct{}, 1)
		fixture.server.maxCompositions = 1
		fixture.server.compositions <- struct{}{}

		recorder := fixture.get(playerPathFor("media"))

		mustMatch(t, recorder.Code, http.StatusServiceUnavailable)
		for _, field := range []string{"Content-Disposition", "Accept-Ranges", "Content-Location"} {
			mustMatch(t, recorder.Header().Get(field), "")
		}
		mustMatch(t, recorder.Header().Get("Cache-Control"), "")
		for _, link := range recorder.Header().Values("Link") {
			for _, relation := range []string{relScreen, relAudio, relAlternate} {
				if strings.Contains(link, relation) {
					t.Errorf("the refusal has the Link %s, want none with rel %s", link, relation)
				}
			}
		}
	})
}

// The only Link fields of a relayed refusal are service-desc,
// service-doc, and describedby, which every Player route sends.
func TestARelayedFaultHasNoCaptureHeaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.display.status = http.StatusNotFound
		fixture.display.contentType = problemContentType
		fixture.display.body = []byte(`{"type":"about:blank","status":404,"detail":"no Display named boe-1080"}`)

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusNotFound)
		mustMatch(t, recorder.Header().Get("Content-Disposition"), "")
		mustMatch(t, recorder.Header().Get("Accept-Ranges"), "")
		mustMatch(t, len(recorder.Header().Values("Link")), 3)
	})
}

func TestTheStderrTailKeepsTheLastLines(t *testing.T) {
	writer := &tailWriter{}

	for index := range 2000 {
		_, err := writer.Write([]byte("line " + strconv.Itoa(index) + "\n"))
		mustSucceed(t, err)
	}

	tail := writer.tail()
	mustMatch(t, len(tail) <= stderrTail, true)
	mustMatch(t, strings.HasSuffix(tail, "line 1999"), true)
	mustMatch(t, strings.Contains(tail, "line 0\n"), false)
}

func TestAShortStderrIsKeptWhole(t *testing.T) {
	writer := &tailWriter{}

	_, err := writer.Write([]byte("Output file is empty, nothing was encoded\n"))
	mustSucceed(t, err)

	mustMatch(t, writer.tail(), "Output file is empty, nothing was encoded")
}

// A caller behind one port-forward to this API cannot reach the display
// API by its in-cluster name, so even a screen alone is streamed here
// and not redirected.
func TestAScreenOnlyMediaIsStreamedWithoutARedirect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.now = time.Now
		fixture.server.ffmpeg = copyingFFmpegOne(t)
		fixture.display.body = []byte("screen")
		fixture.plane.players[testAPIPlayer] = shapedPlayer(true, 0, false)

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusOK)
		mustMatch(t, recorder.Header().Get("Location"), "")
		mustMatch(t, recorder.Body.String(), "screen")
		mustMatch(t, len(fixture.display.made()), 1)
		mustMatch(t, len(fixture.audio.made()), 0)
		mustMatch(t, recorder.Header().Get("Content-Type"), "video/mp4")
	})
}

func TestASinkOnlyMediaIsStreamedWithoutARedirect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.server.now = time.Now
		fixture.server.ffmpeg = copyingFFmpegOne(t)
		fixture.audio.contentType = "audio/ogg"
		fixture.audio.body = []byte("sound")
		fixture.plane.players[testAPIPlayer] = shapedPlayer(false, 1, true)

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusOK)
		mustMatch(t, recorder.Header().Get("Location"), "")
		mustMatch(t, recorder.Body.String(), "sound")
		mustMatch(t, len(fixture.audio.made()), 1)
		mustMatch(t, len(fixture.display.made()), 0)
		mustMatch(t, recorder.Header().Get("Content-Type"), `video/mp4; codecs="Opus"`)
	})
}

// copyingFFmpegOne returns the path of a script that replaces ffmpeg
// for a composition with one input. It copies file descriptor 3 to
// stdout, so the single stream reaches the recorder unchanged.
func copyingFFmpegOne(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\ncat <&3\n"
	mustSucceed(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func TestAComposedRefusalWithNoStreamsIsAboutTheWholePlayer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.plane.players[testAPIPlayer] = shapedPlayer(false, 0, false)

		recorder := fixture.get(playerPathFor("media.mp4"))

		mustMatch(t, recorder.Code, http.StatusConflict)
		document := problemOf(t, recorder)
		mustMatch(t, document.Type, problemNotPlaying)
		mustMatch(t, document.Detail,
			"this Player resolves nothing to capture until a Play runs on it")
	})
}

// net/http does not send a written status until the body fills its
// buffer or the handler flushes. Without a flush after the headers, the
// client would receive the status only with the first body bytes.
//
// The test runs on the real clock, outside a synctest bubble. This API
// reads the stand-in ffmpeg's output from an OS pipe, and a bubble does
// not count that read as blocked, so its fake clock would never reach
// the end of the siblings' 600 ms delay.
func TestTheComposedHeadersReachTheClientBeforeTheFirstByte(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.now = time.Now
	fixture.server.ffmpeg = copyingFFmpeg(t)
	// Both fake siblings send headers at once and delay the first body
	// byte, as a real capture does while it waits for its first
	// keyframe.
	fixture.display.bodyDelay = 600 * time.Millisecond
	fixture.audio.bodyDelay = 600 * time.Millisecond
	fixture.display.body = []byte("frame")

	public := apiservertest.Start(t, fixture.server.handler())
	request, err := http.NewRequest(http.MethodGet, apiservertest.Host+playerPathFor("media.mp4"), nil)
	mustSucceed(t, err)
	request.Header.Set("Authorization", "Bearer "+testAPIToken)

	began := time.Now()
	response, err := public.Client().Do(request)
	mustSucceed(t, err)
	defer drain(response.Body)
	headers := time.Since(began)
	body, err := io.ReadAll(response.Body)
	mustSucceed(t, err)
	firstByte := time.Since(began)

	mustMatch(t, response.StatusCode, http.StatusOK)
	mustMatch(t, string(body), "frame")
	if headers > 400*time.Millisecond {
		t.Errorf("the headers arrived after %v, want under 400ms, because the sibling captures send their first body byte after 600ms", headers)
	}
	if firstByte < 500*time.Millisecond {
		t.Errorf("the body arrived after %v, want at least 500ms, because the sibling captures send their first body byte after 600ms", firstByte)
	}
}
