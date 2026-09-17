package main

// This file builds the one ffmpeg command every capture runs
// through. Every format takes the same road, one ffmpeg process per
// request that reads raw frames on its stdin and writes the body on
// its stdout, so there is one code path to read, one set of options
// to compare with the plan, and one failure shape. A clip's color
// conversion runs on the node's GPU in scale_vaapi, because the
// software conversion costs about four times the CPU at 1080p and
// a stick-class node cannot run it at 4K at all.

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

// The ffmpeg the display-capture image carries, started once per
// request. It is a variable so a drill can put a program of its own
// in its place.
var ffmpegProgram = "/usr/bin/ffmpeg"

// threadQueueSize bounds the frames ffmpeg queues from the pipe, so
// a slow encoder cannot grow the process. clipWidthLimit is the width
// a clip on a screen wider than 1080p is scaled to unless the caller
// asks for more, because the container's memory limit is sized for a
// 1080p encode.
const (
	threadQueueSize = 8
	clipWidthLimit  = 1920
)

// How much of ffmpeg's stderr an error carries. The tail is the part
// that says what went wrong: ffmpeg prints its banner and its graph
// first, and the failure last.
const stderrTailSize = 4 << 10

// One encode: the frame the pipe carries, the form it is asked for,
// the caller's knobs, and the device it runs on.
type encodePlan struct {
	mediaType   string
	width       int
	height      int
	pixelFormat string
	framerate   int
	quality     int
	// The caller asked for one axis or neither: width= and height=
	// together are a 400, and the axis that is not given follows the
	// aspect.
	scaleWidth  int
	scaleHeight int
	device      string
	// The fallback graph converts on the CPU and uploads nv12. It
	// is used only where the node's driver refuses to upload the
	// compositor's own pixel format.
	software bool
}

// The commands below are the plan's own, and a reader compares them
// with the plan line for line.
func (p encodePlan) args() []string {
	args := []string{
		"-hide_banner", "-nostdin",
		"-f", "rawvideo",
		"-pixel_format", p.pixelFormat,
		"-video_size", fmt.Sprintf("%dx%d", p.width, p.height),
	}
	if p.framerate > 0 {
		args = append(args, "-framerate", strconv.Itoa(p.framerate),
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
		return append(args, "-c:v", "h264_vaapi",
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

// The device is the render node the pod's render request delivers.
// -init_hw_device opens it under the name gpu, and -filter_hw_device
// hands that device to the filter graph, where hwupload needs it.
func (p encodePlan) hardware() []string {
	return []string{
		"-init_hw_device", "vaapi=gpu:" + p.device,
		"-filter_hw_device", "gpu",
		"-vf", p.graph(),
	}
}

// The graph uploads first, so the conversion to nv12 runs on the
// GPU in scale_vaapi. h264_vaapi takes only vaapi frames (ffmpeg
// 8.0.1), so hwupload is in every clip's graph. The fallback
// converts on the CPU first and uploads nv12, for a node whose
// driver refuses a bgr0 upload. Both graphs scale to the same size,
// because the 1080p rule is what the container's memory limit is
// sized against, and the fallback runs on the node that can least
// afford 4K.
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

// The size the filter scales the region to, in the option form both
// scale filters take, and the empty string for no scale at all. The
// axis the caller named is stated and the other is -2, which is the
// aspect rounded to an even number of pixels, because nv12 takes no
// odd dimension. height= decides the size on its own when it is
// given, so the 1080p default applies only where neither knob is.
func (p encodePlan) encodeSize() string {
	if height := p.encodeHeight(); height > 0 {
		return fmt.Sprintf("h=%d:w=-2", height)
	}
	if width := p.encodeWidth(); width > 0 {
		return fmt.Sprintf("w=%d:h=-2", width)
	}
	return ""
}

// height= scales down only, so a height that is not smaller than the
// region is no scale at all.
func (p encodePlan) encodeHeight() int {
	if p.scaleHeight <= 0 || p.scaleHeight >= p.height {
		return 0
	}
	return p.scaleHeight
}

// A still runs no VA-API graph, one frame being cheaper to encode on
// the CPU than a device is to open, so its width= or height= is a
// software scale. A still takes no 1080p default: the caller asked
// for one frame, and one frame of a 4K screen costs 33 MB either
// way.
func (p encodePlan) softwareScale() []string {
	switch {
	case p.encodeHeight() > 0:
		return []string{"-vf", fmt.Sprintf("scale=h=%d:w=-2", p.encodeHeight())}
	case p.scaleWidth > 0 && p.scaleWidth < p.width:
		return []string{"-vf", fmt.Sprintf("scale=w=%d:h=-2", p.scaleWidth)}
	}
	return nil
}

// A clip wider than 1080p is scaled to 1080p unless the caller asked
// for another size with width= or height=. A scale that is not
// smaller than the frame is no scale at all.
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

// This API's quality runs 1 to 100, high being better. The software
// JPEG encoder's -q:v runs 2 to 31, low being better, so a still's
// quality maps onto that scale. mjpeg_vaapi takes the 1 to 100 scale
// as -global_quality and needs no map.
func jpegScale(quality int) int {
	if quality <= 0 {
		quality = 85
	}
	value := 31 - (quality-1)*29/99
	return min(max(value, 2), 31)
}

// The render node the pod's claim delivers. The name is read from
// the directory rather than stated, because the kernel renumbers
// render nodes across a reboot.
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
		return "", fmt.Errorf("no render node in %s; does this container claim a render device?", driRoot)
	}
	return driRoot + "/" + nodes[0], nil
}

// An encode is one process, with a pipe that carries the frames in
// and a pipe that carries the body out.
type encoder struct {
	cmd    *exec.Cmd
	frames io.WriteCloser
	body   io.ReadCloser
	stderr *stderrTail
}

// ffmpeg's own stderr reaches this process's log, because the graph
// it chose is what a drill reads to tell the GPU conversion from the
// fallback. The tail is kept beside it so a failure carries ffmpeg's
// own words.
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
		return nil, fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}
	return &encoder{cmd: cmd, frames: frames, body: body, stderr: tail}, nil
}

// An encode that failed carries ffmpeg's last words, which is what
// the log line and a problem document's detail report.
func (e *encoder) wait() error {
	err := e.cmd.Wait()
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w: %s", ffmpegProgram, err, e.stderr.text())
}

func (e *encoder) end() {
	_ = e.frames.Close()
	_ = e.body.Close()
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	_ = e.cmd.Wait()
}

// The last bytes of ffmpeg's stderr, kept in a bounded tail because
// a stream may run for hours and its stderr with it.
type stderrTail struct {
	mu   sync.Mutex
	held []byte
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.held = append(t.held, p...)
	if len(t.held) > stderrTailSize {
		t.held = t.held[len(t.held)-stderrTailSize:]
	}
	return len(p), nil
}

func (t *stderrTail) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.held))
}

// The sidecar's log carries its own lines and ffmpeg's together, so
// each line from the encoder carries a prefix that names it.
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
