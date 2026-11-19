package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The expected commands are written out in full, so a change to any
// ffmpeg option shows in the test diff.
func TestTheEncoderCommands(t *testing.T) {
	cases := []struct {
		name string
		plan encodePlan
		want string
	}{
		{
			name: "a still",
			plan: encodePlan{mediaType: "image/png", width: 1920, height: 1080, pixelFormat: "bgr0"},
			want: "-hide_banner -nostdin -f rawvideo -pixel_format bgr0 -video_size 1920x1080 -i pipe:0 " +
				"-frames:v 1 -c:v png -f image2pipe pipe:1",
		},
		{
			name: "a still as JPEG",
			plan: encodePlan{mediaType: "image/jpeg", width: 1920, height: 1080, pixelFormat: "bgr0", quality: 85},
			want: "-hide_banner -nostdin -f rawvideo -pixel_format bgr0 -video_size 1920x1080 -i pipe:0 " +
				"-frames:v 1 -c:v mjpeg -q:v 7 -f image2pipe pipe:1",
		},
		{
			name: "a clip",
			plan: encodePlan{
				mediaType: "video/mp4", width: 1920, height: 1080, pixelFormat: "bgr0",
				framerate: 15, device: "/dev/dri/renderD128",
			},
			want: "-hide_banner -nostdin -f rawvideo -pixel_format bgr0 -video_size 1920x1080 " +
				"-framerate 15 -use_wallclock_as_timestamps 1 -thread_queue_size 8 -i pipe:0 " +
				"-init_hw_device vaapi=gpu:/dev/dri/renderD128 -filter_hw_device gpu " +
				"-vf hwupload,scale_vaapi=format=nv12 " +
				"-c:v h264_vaapi -profile:v high -level 4.1 -g 15 -bf 0 -flush_packets 1 " +
				"-f mp4 -movflags frag_keyframe+empty_moov+default_base_moof -frag_duration 1000000 pipe:1",
		},
		{
			name: "the low-end stream",
			plan: encodePlan{
				mediaType: "multipart/x-mixed-replace", width: 1920, height: 1080, pixelFormat: "bgr0",
				framerate: 15, quality: 85, device: "/dev/dri/renderD128",
			},
			want: "-hide_banner -nostdin -f rawvideo -pixel_format bgr0 -video_size 1920x1080 " +
				"-framerate 15 -use_wallclock_as_timestamps 1 -thread_queue_size 8 -i pipe:0 " +
				"-init_hw_device vaapi=gpu:/dev/dri/renderD128 -filter_hw_device gpu " +
				"-vf hwupload,scale_vaapi=format=nv12 " +
				"-c:v mjpeg_vaapi -global_quality 85 -jfif 1 -g 15 -bf 0 -flush_packets 1 -f mpjpeg pipe:1",
		},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			if got := strings.Join(row.plan.args(), " "); got != row.want {
				t.Errorf("the command is\n  %s\nwant\n  %s", got, row.want)
			}
		})
	}
}

func TestTheClipScale(t *testing.T) {
	cases := []struct {
		name  string
		width int
		asked int
		want  string
	}{
		{"a 1080p screen scales nothing", 1920, 0, "hwupload,scale_vaapi=format=nv12"},
		{"a 4K screen goes to 1080p", 3840, 0, "hwupload,scale_vaapi=w=1920:h=-2:format=nv12"},
		{"a caller who asks for more gets more", 3840, 2560, "hwupload,scale_vaapi=w=2560:h=-2:format=nv12"},
		{"a caller who asks for less gets less", 1920, 640, "hwupload,scale_vaapi=w=640:h=-2:format=nv12"},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			plan := encodePlan{mediaType: "video/mp4", width: row.width, scaleWidth: row.asked}
			if got := plan.graph(); got != row.want {
				t.Errorf("the graph is %q, want %q", got, row.want)
			}
		})
	}
}

func TestTheSoftwareFallbackConvertsFirst(t *testing.T) {
	cases := []struct {
		name  string
		width int
		asked int
		want  string
	}{
		{"a 1080p screen scales nothing", 1920, 0, "format=nv12,hwupload"},
		{"a 4K screen goes to 1080p", 3840, 0, "scale=w=1920:h=-2,format=nv12,hwupload"},
		{"a caller's own width", 3840, 2560, "scale=w=2560:h=-2,format=nv12,hwupload"},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			plan := encodePlan{mediaType: "video/mp4", width: row.width, scaleWidth: row.asked, software: true}
			if got := plan.graph(); got != row.want {
				t.Errorf("the fallback graph is %q, want %q", got, row.want)
			}
		})
	}
}

func TestTheHeightKnobScalesTheOtherAxis(t *testing.T) {
	cases := []struct {
		name      string
		mediaType string
		width     int
		height    int
		asked     int
		software  bool
		want      string
	}{
		{"a clip on the GPU", "video/mp4", 1920, 1080, 540, false, "-vf hwupload,scale_vaapi=h=540:w=-2:format=nv12"},
		{"a clip on a 4K screen", "video/mp4", 3840, 2160, 540, false, "-vf hwupload,scale_vaapi=h=540:w=-2:format=nv12"},
		{"a clip through the fallback", "video/mp4", 3840, 2160, 540, true, "-vf scale=h=540:w=-2,format=nv12,hwupload"},
		{"a still", "image/png", 1920, 1080, 540, false, "-vf scale=h=540:w=-2"},
		{"a height that is not smaller", "image/png", 1920, 1080, 1080, false, ""},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			plan := encodePlan{
				mediaType: row.mediaType, width: row.width, height: row.height,
				pixelFormat: "bgr0", framerate: 15, scaleHeight: row.asked,
				device: "/dev/dri/renderD128", software: row.software,
			}
			command := strings.Join(plan.args(), " ")
			if row.want == "" {
				if strings.Contains(command, "scale=h=") {
					t.Errorf("the command is %s, want no scale filter", command)
				}
				return
			}
			if !strings.Contains(command, row.want) {
				t.Errorf("the command is\n  %s\nwant it to contain\n  %s", command, row.want)
			}
		})
	}
}

func TestAStillScalesInSoftware(t *testing.T) {
	plan := encodePlan{mediaType: "image/png", width: 1920, height: 1080, pixelFormat: "bgr0", scaleWidth: 640}
	if !strings.Contains(strings.Join(plan.args(), " "), "-vf scale=w=640:h=-2") {
		t.Errorf("the command is %v, want it to contain -vf scale=w=640:h=-2", plan.args())
	}
}

func TestTheJPEGQualityScale(t *testing.T) {
	cases := []struct {
		quality int
		want    int
	}{
		{1, 31},
		{85, 7},
		{100, 2},
		{0, 7},
	}
	for _, row := range cases {
		if got := jpegScale(row.quality); got != row.want {
			t.Errorf("quality %d is q:v %d, want %d", row.quality, got, row.want)
		}
	}
}

func TestTheRenderNodeIsRead(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"card1", "renderD129", "renderD128"} {
		if err := os.WriteFile(directory+"/"+name, nil, 0o600); err != nil {
			t.Fatalf("writing the device file %s: %v", name, err)
		}
	}
	node, err := renderNode(directory)
	if err != nil {
		t.Fatalf("finding the render node: %v", err)
	}
	if node != directory+"/renderD128" {
		t.Errorf("the render node is %q, want the first of the card's nodes", node)
	}
}

func TestNoRenderNodeIsReported(t *testing.T) {
	_, err := renderNode(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no render node") {
		t.Errorf("the error is %v, want one that reports no render node", err)
	}
}

func TestTheStderrTailKeepsTheLastWords(t *testing.T) {
	tail := &stderrTail{}
	if _, err := tail.Write([]byte(strings.Repeat("x", stderrTailSize))); err != nil {
		t.Fatalf("writing the first chunk to the tail: %v", err)
	}
	if _, err := tail.Write([]byte("\nImpossible to convert between the formats\n")); err != nil {
		t.Fatalf("writing the second chunk to the tail: %v", err)
	}
	if !strings.HasSuffix(tail.text(), "Impossible to convert between the formats") {
		t.Errorf("the tail ends with %q, want the last line written", tail.text())
	}
	if len(tail.text()) > stderrTailSize {
		t.Errorf("the tail holds %d bytes, want at most %d", len(tail.text()), stderrTailSize)
	}
}

func TestThePixelFormatComesFromTheCompositor(t *testing.T) {
	plan := encodePlan{mediaType: "image/png", width: 8, height: 8, pixelFormat: "bgra"}
	args := plan.args()
	index := 0
	for i, arg := range args {
		if arg == "-pixel_format" {
			index = i + 1
		}
	}
	if args[index] != "bgra" {
		t.Errorf("the command passes %q, want the compositor's own format", args[index])
	}
	if !reflect.DeepEqual(args[:2], []string{"-hide_banner", "-nostdin"}) {
		t.Errorf("the command starts with %v, want -hide_banner -nostdin", args[:2])
	}
}

// probeStderr is ffmpeg's stderr from a probe on a GPU with no VA-API
// post-processing.
const probeStderr = `Input #0, lavfi, from 'color=size=64x64:duration=0.1:rate=5':
  Duration: N/A, start: 0.000000, bitrate: N/A
  Stream #0:0: Video: wrapped_avframe, yuv420p, 64x64 [SAR 1:1 DAR 1:1], 5 fps, 5 tbr, 5 tbn
Stream mapping:
  Stream #0:0 -> #0:0 (wrapped_avframe (native) -> h264 (h264_vaapi))
[Parsed_scale_vaapi_1 @ 0x7c1e48002540] Failed to create processing pipeline config: 12 (the requested VAProfile is not supported).
[Parsed_scale_vaapi_1 @ 0x7c1e48002540] Failed to configure output pad on Parsed_scale_vaapi_1
[vf#0:0] Error reinitializing filters!
[vost#0:0/h264_vaapi] Could not open encoder before EOF
[out#0/mp4] Nothing was written into output file, because at least one of its streams received no packets.
frame=    0 fps=0.0 q=0.0 Lsize=       0KiB time=N/A bitrate=N/A speed=N/A
Conversion failed!
`

func TestTheStderrTailSkipsTheEpilogue(t *testing.T) {
	cases := []struct {
		name  string
		wrote string
		want  string
	}{
		{
			name:  "a probe on a chip with no post-processing",
			wrote: probeStderr,
			want:  "[vost#0:0/h264_vaapi] Could not open encoder before EOF",
		},
		{
			name:  "the muxer's note under a component tag",
			wrote: "[vf#0:0] Error reinitializing filters!\n[out#0/mp4] Nothing was written into output file, because at least one of its streams received no packets.\n",
			want:  "[vf#0:0] Error reinitializing filters!",
		},
		{
			name:  "a progress counter after the cause",
			wrote: "Device creation failed: -22.\nframe=   12 fps=8.0 q=-0.0 size=       0KiB time=00:00:00.40\n",
			want:  "Device creation failed: -22.",
		},
		{
			name:  "nothing but the epilogue",
			wrote: "frame=    0 fps=0.0 q=0.0\nConversion failed!\n",
			want:  "Conversion failed!",
		},
		{
			name:  "one line of text",
			wrote: "Unrecognized option 'bogus'.\n",
			want:  "Unrecognized option 'bogus'.",
		},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			tail := &stderrTail{}
			if _, err := tail.Write([]byte(row.wrote)); err != nil {
				t.Fatalf("writing to the tail: %v", err)
			}
			if got := tail.lastLine(); got != row.want {
				t.Errorf("the line is\n  %s\nwant\n  %s", got, row.want)
			}
		})
	}
}

// RFC 6381 writes an H.264 codec as avc1 followed by three bytes: 0x64
// for High profile, 0x00 for no constraint flags, and the level.
func TestTheClipNamesItsCodec(t *testing.T) {
	cases := []struct {
		name      string
		region    cropRect
		asked     int
		framerate int
		codecs    string
		level     string
	}{
		{"1080p at 15", cropRect{W: 1920, H: 1080}, 0, 15, "avc1.640029", "4.1"},
		{"1080p at 60", cropRect{W: 1920, H: 1080}, 0, 60, "avc1.640029", "4.1"},
		{"a 4K region, which the encoder scales to 1080p", cropRect{W: 3840, H: 2160}, 0, 30, "avc1.640029", "4.1"},
		{"a caller who asks for more than 1080p", cropRect{W: 2560, H: 1440}, 2560, 30, "avc1.640033", "5.1"},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			plan := encodePlan{
				mediaType: "video/mp4", width: row.region.W, height: row.region.H,
				pixelFormat: "bgr0", framerate: row.framerate, scaleWidth: row.asked,
				device: "/dev/dri/renderD128",
			}
			want := `video/mp4; codecs="` + row.codecs + `"`
			served := captureContentType("video/mp4", plan.encodedWidth(), plan.encodedHeight(), plan.framerate)
			if served != want {
				t.Errorf("the type is %q, want %q", served, want)
			}
			command := strings.Join(plan.args(), " ")
			if !strings.Contains(command, "-profile:v high -level "+row.level) {
				t.Errorf("the command is\n  %s\nwant it to pin level %s", command, row.level)
			}
		})
	}
}

// Level 4.1 (0x29) covers up to 1920x1080 at 60 frames per second, and
// anything larger is level 5.1 (0x33).
func TestTheLevelFollowsTheSizeAndTheRate(t *testing.T) {
	cases := []struct {
		width, height, framerate int
		want                     string
	}{
		{1920, 1080, 60, "avc1.640029"},
		{1921, 1080, 30, "avc1.640033"},
		{1920, 1081, 30, "avc1.640033"},
		{1920, 1080, 61, "avc1.640033"},
	}
	for _, row := range cases {
		if got := h264Codecs(row.width, row.height, row.framerate); got != row.want {
			t.Errorf("%dx%d at %d is %q, want %q", row.width, row.height, row.framerate, got, row.want)
		}
	}
}

func TestTheOtherFormsNameNoCodec(t *testing.T) {
	cases := []struct{ mediaType, want string }{
		{"image/png", "image/png"},
		{"image/jpeg", "image/jpeg"},
		{"multipart/x-mixed-replace", mjpegContentType},
	}
	for _, row := range cases {
		if got := captureContentType(row.mediaType, 1920, 1080, 15); got != row.want {
			t.Errorf("%s is served as %q, want %q", row.mediaType, got, row.want)
		}
	}
}

func TestAFourKScreenClipsAtTheScaledSize(t *testing.T) {
	screen := captureScreen{Width: 3840, Height: 2160}
	if got := clipWidth(screen.Width); got != 1920 {
		t.Errorf("a 4K screen clips at width %d, want 1920", got)
	}
	if got := clipHeight(screen); got != 1080 {
		t.Errorf("a 4K screen clips at height %d, want 1080", got)
	}
	if got := h264Codecs(clipWidth(screen.Width), clipHeight(screen), 15); got != "avc1.640029" {
		t.Errorf("a 4K screen's clip names %q, want a 1080p clip's codec", got)
	}
}

// emptyFfmpeg writes a stand-in for an ffmpeg that exits 0 with no
// output and one line on stderr.
func emptyFfmpeg(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/ffmpeg"
	script := "#!/bin/sh\necho 'Output file is empty, nothing was encoded' >&2\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}
	return path
}

// stream waits for the encoder before it asks for the failure of an
// empty body, so the failure reads the exit that the first wait
// returned.
func TestTheFailureOfAnEmptyEncodeAfterAWaitSaysThatFfmpegWroteNothing(t *testing.T) {
	held := ffmpegProgram
	ffmpegProgram = emptyFfmpeg(t)
	defer func() { ffmpegProgram = held }()
	encoder, err := startEncoder(t.Context(), encodePlan{mediaType: "video/mp4", width: 4, height: 4, framerate: 30})
	if err != nil {
		t.Fatalf("starting the fake ffmpeg: %v", err)
	}
	if err := encoder.wait(); err != nil {
		t.Fatalf("waiting for the fake ffmpeg: %v", err)
	}

	got := encoder.failure()
	want := ffmpegProgram + " exited with no error and wrote no bytes: Output file is empty, nothing was encoded"
	if got == nil || got.Error() != want {
		t.Errorf("the failure is %v, want %q", got, want)
	}
}
