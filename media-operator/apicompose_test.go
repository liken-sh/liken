package main

// These tests cover the composition: the ffmpeg command line for every
// stream count, the offset measured between two header instants, the
// status each upstream answer maps to, the fields a refusal carries and
// does not, and one real mux over fixture streams read back with
// ffprobe.

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestComposeArgumentsCarryTheOffsetOfEachTrack(t *testing.T) {
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

func TestTheComposedContentTypeCarriesTheUpstreamsProfile(t *testing.T) {
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

// The codecs parameter is copied whole from the sibling that encoded
// the track. Nothing here reads it, because only display-api knows
// what it sent, and a sibling that names none leaves it empty.
func TestTheCodecsParameterIsCopiedWholeFromTheUpstream(t *testing.T) {
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

// The -itsoffset the mux is given is the difference of the two header
// instants, audio minus screen, measured on this API's own clock.
func TestTheOffsetComesFromTheTwoHeaderInstants(t *testing.T) {
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
}

// recordingFFmpeg is a stand-in for ffmpeg. It writes down the
// arguments it was called with, drains the two bodies it inherited so
// the feeds finish, and writes a few bytes so the response has a body.
func recordingFFmpeg(t *testing.T, arguments string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + arguments +
		"\ncat <&3 > /dev/null\ncat <&4 > /dev/null\nprintf muxed\n"
	mustSucceed(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// Every answer a sibling can give, and the status, problem type, and
// detail the client of this API reads instead: a 400 and a 404 relay
// the upstream's own problem, a 503 relays its Retry-After, and an
// answer that is not a problem document is 502.
func TestAnUpstreamAnswerMapsToThisAPIsOwnStatus(t *testing.T) {
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
			name: "a compositor that denied the capture", status: http.StatusInternalServerError,
			contentType: problemContentType,
			body:        `{"type":"https://display.liken.sh/problems/capture-denied","title":"Denied","status":500,"detail":"unauthorized"}`,
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
			mustMatch(t, strings.HasPrefix(document.Upstream, fixture.display.server.URL), true)
			if row.relayed != "" {
				mustMatch(t, recorder.Header().Get("Retry-After"), row.relayed)
			}
		})
	}
}

// A refused connection is a 503 with Retry-After, because a sibling
// that is restarting comes back and a retry clears it.
func TestAnUnreachableUpstreamIsAServiceUnavailable(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.display = "http://127.0.0.1:1"

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusServiceUnavailable)
	mustMatch(t, recorder.Header().Get("Retry-After"), retryAfterSeconds)
}

// The composition limit is this API's own bound, and it answers the
// shared capture-busy type with Retry-After, the way a busy sidecar
// does.
func TestTheCompositionLimitAnswersCaptureBusy(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.compositions = make(chan struct{}, 1)
	fixture.server.maxCompositions = 1
	fixture.server.compositions <- struct{}{}

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusServiceUnavailable)
	mustMatch(t, recorder.Header().Get("Retry-After"), retryAfterSeconds)
	mustMatch(t, problemOf(t, recorder).Type, problemCaptureBusy)
	mustMatch(t, len(fixture.display.made()), 0)
}

// A capture that produces bytes leaves a Captured Event on the Player,
// so kubectl describe player answers who looked and when.
func TestACaptureWritesTheCapturedEvent(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.server.ffmpeg = recordingFFmpeg(t, filepath.Join(t.TempDir(), "arguments"))
	fixture.server.now = time.Now

	fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, len(fixture.plane.events), 1)
	event := fixture.plane.events[0]
	mustMatch(t, event.Reason, capturedReason)
	mustMatch(t, event.Type, normalEventType)
	mustMatch(t, event.InvolvedObject.Name, testAPIPlayer)
	mustMatch(t, event.Message,
		testAPISubject+" took the media of "+testAPIPlayer+" as video/mp4")
}

// One real mux over a fixture video and two fixture audio streams,
// read back with ffprobe: one video track, then two audio tracks, in
// order.
func TestOneRealMuxCarriesOneVideoAndTwoAudioTracks(t *testing.T) {
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

// fixtureVideo is a short fragmented MP4, the form the display API
// serves, because the mux demuxes its input from a pipe and a plain
// MP4 with its moov at the end cannot be read that way.
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

// An upstream that sends no headers within the header timeout is a
// 504 with a plain problem document.
func TestAnUpstreamThatSendsNoHeadersInTimeIsAGatewayTimeout(t *testing.T) {
	held := upstreamHeaderTimeout
	upstreamHeaderTimeout = 50 * time.Millisecond
	defer func() { upstreamHeaderTimeout = held }()

	fixture := newAPIFixture(t)
	fixture.display.delay = time.Second

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusGatewayTimeout)
	mustMatch(t, problemOf(t, recorder).Type, aboutBlank)
}

// A unit with sinks and no screen composes the sound alone: the
// command line has no video input and no video map, and the audio
// tracks take inputs 0 and 1.
func TestASoundOnlyCompositionCarriesNoVideoInput(t *testing.T) {
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

// Two sinks and no screen reach the mux as two audio requests and no
// display request, with a codecs parameter that names Opus alone and
// no rel/screen link.
func TestTwoSinksAndNoScreenComposeTheSoundAlone(t *testing.T) {
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
			t.Errorf("a composition with no screen sent %s", link)
		}
	}
}

// A composition refused at the limit carries the problem document
// alone: no Content-Disposition, no Accept-Ranges, no
// Content-Location, and only the service links.
func TestARefusedCompositionCarriesNoCaptureFields(t *testing.T) {
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
				t.Errorf("a refused composition sent %s", link)
			}
		}
	}
}

// An upstream refusal relayed to the client carries the problem
// document alone, with none of the capture fields and only the three
// service links.
func TestARelayedFaultCarriesNoCaptureFields(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.display.status = http.StatusNotFound
	fixture.display.contentType = problemContentType
	fixture.display.body = []byte(`{"type":"about:blank","status":404,"detail":"no Display named boe-1080"}`)

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusNotFound)
	mustMatch(t, recorder.Header().Get("Content-Disposition"), "")
	mustMatch(t, recorder.Header().Get("Accept-Ranges"), "")
	mustMatch(t, len(recorder.Header().Values("Link")), 3)
}

// The stderr the log line carries is the end of what ffmpeg wrote. An
// ffmpeg that fails on every packet writes a line per packet, and the
// lines that end a stream say why it ended.
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

// A short stderr is kept whole, so a single failing line reaches the
// log line unchanged.
func TestAShortStderrIsKeptWhole(t *testing.T) {
	writer := &tailWriter{}

	_, err := writer.Write([]byte("Output file is empty, nothing was encoded\n"))
	mustSucceed(t, err)

	mustMatch(t, writer.tail(), "Output file is empty, nothing was encoded")
}

// A zero-stream 409 names the action for the whole unit, because a
// Player that resolves neither a screen nor a sink is not answered by
// a sentence about sound alone.
func TestAZeroStreamComposedRefusalNamesTheWholeUnit(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.plane.players[testAPIPlayer] = shapedPlayer(false, 0, false)

	recorder := fixture.get(playerPathFor("media.mp4"))

	mustMatch(t, recorder.Code, http.StatusConflict)
	document := problemOf(t, recorder)
	mustMatch(t, document.Type, problemNotPlaying)
	mustMatch(t, document.Detail,
		"run a Play on this Player; it resolves nothing to capture until one runs")
}
