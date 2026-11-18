package main

// Each capture request runs one ffmpeg process, which reads raw frames
// on stdin and writes the encoded body on stdout. A clip converts its
// pixel format on the GPU with scale_vaapi when the node's driver
// supports it, because the same conversion on the CPU takes more CPU
// time than a small node has to spare.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// ffmpegProgram is the ffmpeg binary in the display-capture image.
// It is a variable so that a test can start a different program.
var ffmpegProgram = "/usr/bin/ffmpeg"

// threadQueueSize is the number of frames ffmpeg queues from the
// pipe, so a slow encoder cannot make the process grow without limit.
// clipWidthLimit is the width that a clip wider than 1920 pixels is
// scaled to, unless the caller asks for another size, because the
// container's memory limit is sized for a 1080p encode.
const (
	threadQueueSize = 8
	clipWidthLimit  = 1920
)

// stderrTailSize is how many bytes of the end of ffmpeg's stderr an
// error can quote. ffmpeg prints its banner and filter graph first
// and the failure last.
const stderrTailSize = 4 << 10

// encodePlan is the settings of one ffmpeg run. width, height, and
// pixelFormat describe the raw frames on stdin.
type encodePlan struct {
	mediaType   string
	width       int
	height      int
	pixelFormat string
	framerate   int
	quality     int
	// At most one of these is set, because a request with both width=
	// and height= gets a 400. The other axis keeps the aspect ratio.
	scaleWidth  int
	scaleHeight int
	device      string
	// software selects the fallback graph, which converts to nv12 on
	// the CPU and then uploads, for a node whose driver cannot run
	// scale_vaapi.
	software bool
}

// args returns the ffmpeg arguments for the plan's media type.
func (p encodePlan) args() []string {
	args := []string{
		"-hide_banner", "-nostdin",
		"-f", "rawvideo",
		"-pixel_format", p.pixelFormat,
		"-video_size", fmt.Sprintf("%dx%d", p.width, p.height),
	}
	if p.framerate > 0 {
		// Without -use_wallclock_as_timestamps, ffmpeg stamps each frame
		// as if it arrived on the -framerate schedule. A node that falls
		// behind sends fewer frames, so the clip would play faster than
		// real time.
		args = append(args, "-framerate", strconv.Itoa(p.framerate),
			"-use_wallclock_as_timestamps", "1",
			"-thread_queue_size", strconv.Itoa(threadQueueSize))
	}
	args = append(args, "-i", "pipe:0")

	switch p.mediaType {
	case "image/png":
		args = append(args, p.softwareScale()...)
		return append(args, "-frames:v", "1", "-c:v", "png", "-f", "image2pipe", "pipe:1")
	case "image/jpeg":
		args = append(args, p.softwareScale()...)
		return append(args, "-frames:v", "1", "-c:v", "mjpeg",
			"-q:v", strconv.Itoa(jpegScale(p.quality)), "-f", "image2pipe", "pipe:1")
	case "video/mp4":
		args = append(args, p.hardware()...)
		args = append(args, "-c:v", "h264_vaapi")
		args = append(args, h264Profile(p.encodedWidth(), p.encodedHeight(), p.framerate)...)
		return append(args,
			"-g", strconv.Itoa(p.framerate), "-bf", "0",
			"-flush_packets", "1",
			"-f", "mp4",
			"-movflags", "frag_keyframe+empty_moov+default_base_moof",
			"-frag_duration", "1000000", "pipe:1")
	default:
		args = append(args, p.hardware()...)
		return append(args, "-c:v", "mjpeg_vaapi",
			"-global_quality", strconv.Itoa(p.quality), "-jfif", "1",
			"-g", strconv.Itoa(p.framerate), "-bf", "0",
			"-flush_packets", "1",
			"-f", "mpjpeg", "pipe:1")
	}
}

// hardware returns the arguments that open the render node and pass
// it to the filter graph, where hwupload needs it.
func (p encodePlan) hardware() []string {
	return []string{
		"-init_hw_device", "vaapi=gpu:" + p.device,
		"-filter_hw_device", "gpu",
		"-vf", p.graph(),
	}
}

// graph returns the filter graph of a clip. Every graph ends on the
// GPU, because h264_vaapi and mjpeg_vaapi accept only VA-API frames.
// The default graph uploads first and converts to nv12 in scale_vaapi.
// The fallback graph converts and scales on the CPU, then uploads. Both
// graphs scale to the same size, because the memory limit is sized for
// that size and the fallback runs on the nodes with the least memory.
func (p encodePlan) graph() string {
	size := p.encodeSize()
	if p.software {
		if size == "" {
			return "format=nv12,hwupload"
		}
		return "scale=" + size + ",format=nv12,hwupload"
	}
	if size == "" {
		return "hwupload,scale_vaapi=format=nv12"
	}
	return "hwupload,scale_vaapi=" + size + ":format=nv12"
}

// encodeSize returns the size to scale to, in the syntax of both scale
// filters, or "" for no scale. The other axis is -2, which keeps the
// aspect ratio and rounds to an even number, because nv12 does not
// allow an odd dimension. A height overrides the 1920-pixel default
// width.
func (p encodePlan) encodeSize() string {
	if height := p.encodeHeight(); height > 0 {
		return fmt.Sprintf("h=%d:w=-2", height)
	}
	if width := p.encodeWidth(); width > 0 {
		return fmt.Sprintf("w=%d:h=-2", width)
	}
	return ""
}

// clipWidth and clipHeight return the size of a default clip of the
// whole screen, which the info document's codecs parameter is derived
// from. A 4K screen reports the codec string of a 1080p clip.
func clipWidth(width int) int {
	if width > clipWidthLimit {
		return clipWidthLimit
	}
	return width
}

func clipHeight(screen captureScreen) int {
	if screen.Width <= clipWidthLimit || screen.Width == 0 {
		return screen.Height
	}
	return screen.Height * clipWidthLimit / screen.Width
}

// encodedWidth and encodedHeight return the size of the video after
// the scale. The Content-Type's codecs parameter and the H.264 level
// come from this size, so the headers can be sent before ffmpeg
// writes a byte.
func (p encodePlan) encodedWidth() int {
	if width := p.encodeWidth(); width > 0 {
		return width
	}
	if height := p.encodeHeight(); height > 0 && p.height > 0 {
		return p.width * height / p.height
	}
	return p.width
}

func (p encodePlan) encodedHeight() int {
	if height := p.encodeHeight(); height > 0 {
		return height
	}
	if width := p.encodeWidth(); width > 0 && p.width > 0 {
		return p.height * width / p.width
	}
	return p.height
}

// encodeHeight returns the height to scale to, or 0 for no scale. A
// height that is not smaller than the frame's is ignored.
func (p encodePlan) encodeHeight() int {
	if p.scaleHeight <= 0 || p.scaleHeight >= p.height {
		return 0
	}
	return p.scaleHeight
}

// softwareScale returns the scale filter of a still. A still is
// encoded on the CPU, because one frame costs less than opening the
// render node. It has no 1920-pixel default, because one 4K frame at
// 4 bytes per pixel is about 33 MB, which fits the memory limit.
func (p encodePlan) softwareScale() []string {
	switch {
	case p.encodeHeight() > 0:
		return []string{"-vf", fmt.Sprintf("scale=h=%d:w=-2", p.encodeHeight())}
	case p.scaleWidth > 0 && p.scaleWidth < p.width:
		return []string{"-vf", fmt.Sprintf("scale=w=%d:h=-2", p.scaleWidth)}
	}
	return nil
}

// encodeWidth returns the width to scale a clip to, or 0 for no
// scale. A width that is not smaller than the frame's is ignored.
// With no width= and no height=, a clip wider than clipWidthLimit is
// scaled to it.
func (p encodePlan) encodeWidth() int {
	if p.scaleWidth > 0 {
		if p.scaleWidth >= p.width {
			return 0
		}
		return p.scaleWidth
	}
	if p.width > clipWidthLimit {
		return clipWidthLimit
	}
	return 0
}

// jpegScale converts the API's quality, 1 to 100 where higher is
// better, to the software JPEG encoder's -q:v, 2 to 31 where lower is
// better. A quality of 0 means the default of 85. mjpeg_vaapi takes
// the API's scale directly as -global_quality.
func jpegScale(quality int) int {
	if quality <= 0 {
		quality = 85
	}
	value := 31 - (quality-1)*29/99
	return min(max(value, 2), 31)
}

// renderNode returns the path of the lowest-numbered render node in
// driRoot. It reads the directory because the kernel can number the
// render nodes differently after a reboot.
func renderNode(driRoot string) (string, error) {
	entries, err := os.ReadDir(driRoot)
	if err != nil {
		return "", err
	}
	var nodes []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "renderD") {
			nodes = append(nodes, entry.Name())
		}
	}
	slices.Sort(nodes)
	if len(nodes) == 0 {
		return "", fmt.Errorf("no render node in %s, so the container has no render device claimed", driRoot)
	}
	return driRoot + "/" + nodes[0], nil
}

// encoder is one running ffmpeg process.
type encoder struct {
	cmd    *exec.Cmd
	frames io.WriteCloser
	body   io.ReadCloser
	stderr *stderrTail
}

// startEncoder starts ffmpeg for the plan. ffmpeg's stderr goes to
// the log, and its end is also kept so that an error can quote
// ffmpeg's own message.
func startEncoder(ctx context.Context, plan encodePlan) (*encoder, error) {
	cmd := exec.CommandContext(ctx, ffmpegProgram, plan.args()...)
	frames, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	body, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tail := &stderrTail{}
	cmd.Stderr = io.MultiWriter(tail, prefixed(os.Stderr, "ffmpeg: "))
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", strings.Join(cmd.Args, " "), err)
	}
	return &encoder{cmd: cmd, frames: frames, body: body, stderr: tail}, nil
}

// failure returns the error for an encoder that wrote no bytes. ffmpeg
// can exit zero when it had nothing to write, so a zero exit is an
// error here too.
func (e *encoder) failure() error {
	if err := e.wait(); err != nil {
		return err
	}
	return fmt.Errorf("%s exited with no error and wrote no bytes: %s", ffmpegProgram, e.stderr.lastLine())
}

// wait waits for ffmpeg to exit. A nonzero exit returns an error that
// quotes lastLine.
func (e *encoder) wait() error {
	err := e.cmd.Wait()
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w: %s", ffmpegProgram, err, e.stderr.lastLine())
}

func (e *encoder) end() {
	_ = e.frames.Close()
	_ = e.body.Close()
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	_ = e.cmd.Wait()
}

// stderrTail keeps the last stderrTailSize bytes of ffmpeg's stderr.
// It has a size limit because a stream can run for hours.
type stderrTail struct {
	mu   sync.Mutex
	data []byte
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if len(t.data) > stderrTailSize {
		t.data = t.data[len(t.data)-stderrTailSize:]
	}
	return len(p), nil
}

func (t *stderrTail) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.data))
}

// lastLine returns the last line of the tail that states a cause. A
// failed run ends with lines that state none: the progress counter, the
// muxer's note that it wrote nothing, and "Conversion failed!".
// lastLine skips them, unless the tail has no other line.
func (t *stderrTail) lastLine() string {
	lines := strings.Split(t.text(), "\n")
	last := ""
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if last == "" {
			last = line
		}
		if !ffmpegEpilogue(line) {
			return line
		}
	}
	return last
}

// ffmpegEpilogue reports whether lastLine skips a line. It strips a
// leading component tag first, because ffmpeg writes the muxer's note
// as "[out#0/mp4] Nothing was written into output file, ...".
func ffmpegEpilogue(line string) bool {
	if strings.HasPrefix(line, "[") {
		if _, rest, tagged := strings.Cut(line, "] "); tagged {
			line = rest
		}
	}
	return line == "Conversion failed!" ||
		strings.HasPrefix(line, "Nothing was written into output file") ||
		strings.HasPrefix(line, "frame=")
}

// prefixed returns a writer that starts each non-blank line with
// prefix, so ffmpeg's lines and the sidecar's own lines can be told
// apart in the log.
func prefixed(to io.Writer, prefix string) io.Writer {
	return &prefixWriter{to: to, prefix: prefix}
}

type prefixWriter struct {
	to     io.Writer
	prefix string
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	for line := range strings.SplitSeq(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		_, _ = fmt.Fprintf(w.to, "%s%s\n", w.prefix, line)
	}
	return len(p), nil
}

// probeConversion checks that this node's driver can run scale_vaapi,
// with one synthetic frame through the default graph. Some drivers
// cannot: the iHD driver on Apollo Lake encodes H.264 and MJPEG but
// has no VA-API post-processing, so scale_vaapi fails to configure and
// the encode writes nothing.
func probeConversion(ctx context.Context, device string) error {
	probe := exec.CommandContext(ctx, ffmpegProgram,
		"-hide_banner", "-nostdin",
		"-f", "lavfi", "-i", "color=size=64x64:duration=0.1:rate=5",
		"-init_hw_device", "vaapi=gpu:"+device,
		"-filter_hw_device", "gpu",
		"-vf", "hwupload,scale_vaapi=format=nv12",
		"-c:v", "h264_vaapi",
		"-f", "null", "-")
	tail := &stderrTail{}
	probe.Stderr = tail
	// The stderr goes to no log, because the fallback to the CPU is a
	// normal outcome and a full ffmpeg log at startup looks like a
	// failure.
	if err := probe.Run(); err != nil {
		return fmt.Errorf("probing scale_vaapi on %s: %w: %s", device, err, tail.lastLine())
	}
	return nil
}
