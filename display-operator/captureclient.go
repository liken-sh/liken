package main

// This file is a Wayland client that captures frames from weston
// through weston_capture_v1, the protocol in weston's
// weston-output-capture.xml. It connects to the capture socket, not to
// the socket of the output watch in wayland.go, and each HTTP request
// opens its own connection. It reuses the wire code in wayland.go.
//
// weston serves the protocol at version 2, and this client binds
// version 1. At version 2 a source can send several format events and
// then formats_done. At version 1 it sends one format event and one
// size event, so the client reads no formats_done.

import (
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

const (
	shmInterface     = "wl_shm"
	shmVersion       = 1
	captureInterface = "weston_capture_v1"
	captureVersion   = 1
)

// Each request opcode is the request's position in its interface in
// the protocol XML. The wire carries no names, so a wrong number sends
// a different request.
const (
	shmCreatePool        uint16 = 0
	shmPoolCreateBuffer  uint16 = 0
	shmPoolDestroy       uint16 = 1
	bufferDestroy        uint16 = 0
	captureV1Destroy     uint16 = 0
	captureV1Create      uint16 = 1
	captureSourceDestroy uint16 = 0
	captureSourceCapture uint16 = 1
)

// The capture source's events. format and size give the buffer that
// the compositor needs, and the source sends them after create and
// again before each retry.
const (
	captureFormatEvent   uint16 = 0
	captureSizeEvent     uint16 = 1
	captureCompleteEvent uint16 = 2
	captureRetryEvent    uint16 = 3
	captureFailedEvent   uint16 = 4
)

// The wl_output scale event, which the watch in wayland.go does not
// read. The info route reports the scale, so a caller that works in
// logical pixels can convert to frame pixels.
const outputScaleEvent uint16 = 3

// The client captures from the framebuffer source because weston
// offers it on every output. The writeback source is often not
// available, and the blending source exists only when color management
// is active on the output.
//
// weston disables hardware planes while a framebuffer capture is
// pending, so the DRM backend composites every view into the primary
// plane, including a video that was on an overlay plane. No view then covers
// the output from a plane of its own, and weston writes the CRTC's
// BACKGROUND_COLOR. i915 refuses every atomic commit that writes 0
// there. weston 16.0.1 and later write opaque black, and the weston
// image builds from such a release.
const captureSourceFramebuffer uint32 = 1

// The DRM fourcc codes that weston's GL renderer reports in the format
// event. Each is the four characters of its name as a little-endian
// number.
const (
	formatAR24 uint32 = 0x34325241
	formatXR24 uint32 = 0x34325258
	formatXB24 uint32 = 0x34324258
	formatAB24 uint32 = 0x34324241
)

// wl_shm gives ARGB8888 the value 0 and XRGB8888 the value 1. Every
// other format in its enum is the DRM fourcc.
const (
	shmFormatARGB8888 uint32 = 0
	shmFormatXRGB8888 uint32 = 1
)

// Each format in capturePixelFormats has four bytes per pixel. The
// stride must be exactly width times four, because weston's PBO read
// path assumes a packed buffer.
const capturePixelBytes = 4

// The most capture requests that one call to frame sends. An output
// whose size or format changes on every capture would otherwise retry
// forever.
const captureRetryLimit = 4

const captureBufferName = "liken-capture"

// The ffmpeg -pixel_format name for each fourcc. The GL renderer's
// read format varies, so the name comes from the format event and is
// not fixed to bgr0.
var capturePixelFormats = map[uint32]string{
	formatXR24: "bgr0",
	formatAR24: "bgra",
	formatXB24: "rgb0",
	formatAB24: "rgba",
}

// The error gives the fourcc as characters, as drm_fourcc.h writes
// it, and as hex, as the wl_shm enum writes it.
func capturePixelFormat(format uint32) (string, error) {
	name, mapped := capturePixelFormats[format]
	if !mapped {
		return "", fmt.Errorf("no ffmpeg pixel format matches the compositor's capture format %s (0x%08x)", fourccName(format), format)
	}
	return name, nil
}

// fourccName returns the four characters of a fourcc, which are its
// bytes from least significant to most.
func fourccName(format uint32) string {
	return string([]byte{byte(format), byte(format >> 8), byte(format >> 16), byte(format >> 24)})
}

func shmFormat(format uint32) uint32 {
	switch format {
	case formatAR24:
		return shmFormatARGB8888
	case formatXR24:
		return shmFormatXRGB8888
	}
	return format
}

// captureScreen is what the HTTP layer reads about one output: the
// frame's size and format, which ffmpeg needs, and the scale and
// refresh rate, which the info route reports.
type captureScreen struct {
	Connector   string
	Width       int
	Height      int
	Scale       int
	Refresh     int
	Format      uint32
	PixelFormat string
}

// captureFailure is a failed event from the compositor, with weston's
// message. compositorFault in capture.go returns a 500 for it, because
// a retry gets the same refusal, and a 503 for every other error.
type captureFailure struct {
	Message string
}

func (f *captureFailure) Error() string {
	return "the compositor refused the capture: " + f.Message
}

// captureSizeChange ends a clip when the output's size or format
// changes. It has both sizes for the error that the clip returns.
type captureSizeChange struct {
	OldWidth  int
	OldHeight int
	Width     int
	Height    int
}

func (c *captureSizeChange) Error() string {
	return fmt.Sprintf("the capture changed size from %dx%d to %dx%d", c.OldWidth, c.OldHeight, c.Width, c.Height)
}

// captureRetryBehavior sets what the session does when the compositor
// sends a retry with a new size or format. A still allocates a new
// buffer and captures again. A clip ends, because ffmpeg reads
// -video_size and -pixel_format once at start and would read every
// later frame with the old values.
type captureRetryBehavior int

const (
	captureReallocates captureRetryBehavior = iota
	captureEndsOnChange
)

type captureOutput struct {
	connector string
	scale     int
	refresh   int
}

// captureSession is one connection to the capture socket, bound to one
// output with one capture source. The compositor writes each frame
// into a memfd that the session maps, and the session allocates a new
// one after each retry event.
type captureSession struct {
	socket   *net.UnixConn
	wire     *waylandClient
	timeout  time.Duration
	behavior captureRetryBehavior

	registry uint32
	shm      uint32
	capture  uint32
	output   uint32
	source   uint32
	pool     uint32
	buffer   uint32

	memory int
	pixels []byte
	info   captureScreen
}

// openCapture connects to the capture socket, binds the output with
// the given connector name, and allocates a buffer for its frames.
func openCapture(socketPath, connector string, timeout time.Duration) (*captureSession, error) {
	socket, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, fmt.Errorf("connecting to the capture socket: %w", err)
	}
	unixSocket, isUnix := socket.(*net.UnixConn)
	if !isUnix {
		_ = socket.Close()
		return nil, fmt.Errorf("the capture socket %s is not a Unix socket", socketPath)
	}
	session := &captureSession{
		socket:  unixSocket,
		wire:    newWaylandClient(unixSocket),
		timeout: timeout,
		memory:  -1,
	}
	if err := session.bind(connector); err != nil {
		_ = session.close()
		return nil, err
	}
	return session, nil
}

func (s *captureSession) screen() captureScreen {
	return s.info
}

// onRetry sets what frame does on a retry event. Until a caller sets
// it, the session reallocates.
func (s *captureSession) onRetry(behavior captureRetryBehavior) {
	s.behavior = behavior
}

// frame captures one frame and returns its pixels. The slice is the
// mapped buffer itself, so it is valid only until the next call to
// frame or close.
func (s *captureSession) frame() ([]byte, error) {
	for range captureRetryLimit {
		if err := s.wire.request(s.source, captureSourceCapture, oneObject(s.buffer)); err != nil {
			return nil, err
		}
		taken, err := s.await()
		if err != nil {
			return nil, err
		}
		if taken {
			return s.pixels, nil
		}
	}
	return nil, fmt.Errorf("the compositor replied with retry to %d capture requests in a row", captureRetryLimit)
}

func (s *captureSession) close() error {
	return errors.Join(
		s.release(),
		s.destroy(&s.source, captureSourceDestroy),
		s.destroy(&s.capture, captureV1Destroy),
		s.socket.Close(),
	)
}

// bind needs two roundtrips. The first reads the registry and binds
// every wl_output. An output sends its name, scale, and mode only after
// it is bound, so the second roundtrip reads them.
func (s *captureSession) bind(connector string) error {
	s.registry = s.wire.newID()
	if err := s.wire.request(displayObject, displayGetRegistry, oneObject(s.registry)); err != nil {
		return err
	}
	outputs := map[uint32]*captureOutput{}
	if err := s.roundtrip(func(event waylandEvent) error { return s.global(event, outputs) }); err != nil {
		return err
	}
	if s.shm == 0 {
		return fmt.Errorf("the compositor on the capture socket offers no %s", shmInterface)
	}
	if s.capture == 0 {
		return fmt.Errorf("the compositor on the capture socket offers no %s", captureInterface)
	}
	if err := s.roundtrip(func(event waylandEvent) error { return outputEvent(event, outputs) }); err != nil {
		return err
	}
	for id, output := range outputs {
		if output.connector != connector {
			continue
		}
		s.output = id
		s.info.Connector, s.info.Scale, s.info.Refresh = output.connector, output.scale, output.refresh
	}
	if s.output == 0 {
		return fmt.Errorf("the compositor has no output named %s", connector)
	}
	return s.createSource()
}

// roundtrip sends a wl_display sync and passes each event to handle
// until the callback's done event arrives. By then the compositor has
// handled every earlier request and sent its events.
func (s *captureSession) roundtrip(handle func(waylandEvent) error) error {
	callback := s.wire.newID()
	if err := s.wire.request(displayObject, displaySync, oneObject(callback)); err != nil {
		return err
	}
	for {
		event, err := s.event()
		if err != nil {
			return err
		}
		if event.object == callback && event.opcode == callbackDoneEvent {
			return nil
		}
		if err := handle(event); err != nil {
			return err
		}
	}
}

// event reads one event with the session's timeout, so a compositor
// that stops responding fails the request instead of blocking it. The
// compositor closes the connection after a wl_display error event, so
// event returns that event as an error.
func (s *captureSession) event() (waylandEvent, error) {
	if err := s.socket.SetReadDeadline(time.Now().Add(s.timeout)); err != nil {
		return waylandEvent{}, err
	}
	event, err := s.wire.event()
	if err != nil {
		return waylandEvent{}, err
	}
	if event.object == displayObject && event.opcode == displayErrorEvent {
		object := event.fields.uint()
		code := event.fields.uint()
		return waylandEvent{}, fmt.Errorf("the compositor sent a protocol error for object %d, code %d: %s", object, code, event.fields.text())
	}
	return event, nil
}

func (s *captureSession) global(event waylandEvent, outputs map[uint32]*captureOutput) error {
	if event.object != s.registry || event.opcode != registryGlobalEvent {
		return nil
	}
	global := event.fields.uint()
	name := event.fields.text()
	version := event.fields.uint()
	if err := event.fields.err; err != nil {
		return err
	}
	switch name {
	case shmInterface:
		id, err := s.bindGlobal(global, name, min(version, shmVersion))
		s.shm = id
		return err
	case captureInterface:
		id, err := s.bindGlobal(global, name, min(version, captureVersion))
		s.capture = id
		return err
	case outputInterface:
		id, err := s.bindGlobal(global, name, min(version, outputVersion))
		outputs[id] = &captureOutput{scale: 1}
		return err
	}
	return nil
}

func (s *captureSession) bindGlobal(global uint32, name string, version uint32) (uint32, error) {
	id := s.wire.newID()
	var words waylandWords
	words.putUint(global)
	words.putText(name)
	words.putUint(version)
	words.putUint(id)
	return id, s.wire.request(s.registry, registryBind, words)
}

// outputEvent records a bound output's name, scale, and refresh rate.
// It rounds the mode's millihertz with westonRefresh, as the Display's
// status does, so both report the same number.
func outputEvent(event waylandEvent, outputs map[uint32]*captureOutput) error {
	output, ours := outputs[event.object]
	if !ours {
		return nil
	}
	switch event.opcode {
	case outputNameEvent:
		output.connector = event.fields.text()
	case outputScaleEvent:
		output.scale = int(event.fields.uint())
	case outputModeEvent:
		flags := event.fields.uint()
		event.fields.skip(2)
		refresh := event.fields.uint()
		if flags&outputModeCurrent != 0 {
			output.refresh = int(westonRefresh(refresh))
		}
	}
	return event.fields.err
}

func (s *captureSession) createSource() error {
	s.source = s.wire.newID()
	var words waylandWords
	words.putUint(s.output)
	words.putUint(captureSourceFramebuffer)
	words.putUint(s.source)
	if err := s.wire.request(s.capture, captureV1Create, words); err != nil {
		return err
	}
	if err := s.describe(); err != nil {
		return err
	}
	return s.allocate()
}

func (s *captureSession) describe() error {
	var format, size bool
	for !format || !size {
		event, err := s.event()
		if err != nil {
			return err
		}
		if event.object != s.source {
			continue
		}
		switch event.opcode {
		case captureFormatEvent:
			s.info.Format, format = event.fields.uint(), true
		case captureSizeEvent:
			s.info.Width = int(int32(event.fields.uint()))
			s.info.Height = int(int32(event.fields.uint()))
			size = true
		case captureFailedEvent:
			return &captureFailure{Message: event.fields.text()}
		}
		if err := event.fields.err; err != nil {
			return err
		}
	}
	return nil
}

// allocate releases the previous buffer, then shares a new memfd of
// the frame's size with the compositor as one wl_buffer in the
// compositor's format.
func (s *captureSession) allocate() error {
	pixelFormat, err := capturePixelFormat(s.info.Format)
	if err != nil {
		return err
	}
	s.info.PixelFormat = pixelFormat
	if err := s.release(); err != nil {
		return err
	}
	stride := s.info.Width * capturePixelBytes
	size := stride * s.info.Height
	if size <= 0 {
		return fmt.Errorf("the compositor reports a capture size of %dx%d, which has no pixels", s.info.Width, s.info.Height)
	}
	file, err := unix.MemfdCreate(captureBufferName, unix.MFD_CLOEXEC)
	if err != nil {
		return fmt.Errorf("creating a memfd for the capture buffer: %w", err)
	}
	s.memory = file
	if err := unix.Ftruncate(file, int64(size)); err != nil {
		return fmt.Errorf("sizing the capture buffer to %d bytes: %w", size, err)
	}
	pixels, err := unix.Mmap(file, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mapping the capture buffer of %d bytes: %w", size, err)
	}
	s.pixels = pixels

	s.pool = s.wire.newID()
	var words waylandWords
	words.putUint(s.pool)
	words.putUint(uint32(size))
	if err := s.sendWithFile(s.shm, shmCreatePool, words, file); err != nil {
		return err
	}
	s.buffer = s.wire.newID()
	words = waylandWords{}
	for _, value := range []uint32{s.buffer, 0, uint32(s.info.Width), uint32(s.info.Height), uint32(stride), shmFormat(s.info.Format)} {
		words.putUint(value)
	}
	return s.wire.request(s.pool, shmPoolCreateBuffer, words)
}

// sendWithFile sends a request that has a file descriptor argument.
// Wayland does not put the descriptor in the message body. It goes
// with the message in the same sendmsg as SCM_RIGHTS ancillary data.
func (s *captureSession) sendWithFile(object uint32, opcode uint16, words waylandWords, file int) error {
	_, _, err := s.socket.WriteMsgUnix(waylandMessage(object, opcode, words), unix.UnixRights(file), nil)
	return err
}

// await reads events until complete, retry, or failed, and returns
// true when the frame is in the buffer. A second capture request while
// one is pending is a protocol error that closes the connection, so
// frame calls await before it sends the next request.
func (s *captureSession) await() (bool, error) {
	width, height, format := s.info.Width, s.info.Height, s.info.Format
	for {
		event, err := s.event()
		if err != nil {
			return false, err
		}
		if event.object != s.source {
			continue
		}
		switch event.opcode {
		case captureCompleteEvent:
			return true, nil
		case captureFailedEvent:
			return false, &captureFailure{Message: event.fields.text()}
		case captureFormatEvent:
			s.info.Format = event.fields.uint()
		case captureSizeEvent:
			s.info.Width = int(int32(event.fields.uint()))
			s.info.Height = int(int32(event.fields.uint()))
		case captureRetryEvent:
			return false, s.retry(width, height, format)
		}
		if err := event.fields.err; err != nil {
			return false, err
		}
	}
}

// retry handles a retry event, which means the compositor could not
// use the buffer. The format and size events before it give the buffer
// that it needs now. retry allocates that buffer, except for a clip
// whose size or format changed, which ends with captureSizeChange.
func (s *captureSession) retry(width, height int, format uint32) error {
	changed := s.info.Width != width || s.info.Height != height || s.info.Format != format
	if s.behavior == captureEndsOnChange && changed {
		return &captureSizeChange{OldWidth: width, OldHeight: height, Width: s.info.Width, Height: s.info.Height}
	}
	return s.allocate()
}

// allocate and close both call release, so a session never holds two
// frames of memory.
func (s *captureSession) release() error {
	failures := []error{s.destroy(&s.buffer, bufferDestroy), s.destroy(&s.pool, shmPoolDestroy)}
	if s.pixels != nil {
		failures = append(failures, unix.Munmap(s.pixels))
		s.pixels = nil
	}
	if s.memory >= 0 {
		failures = append(failures, unix.Close(s.memory))
		s.memory = -1
	}
	return errors.Join(failures...)
}

// destroy skips an object that was never created, which has id zero,
// so close works after an open that failed at any step.
func (s *captureSession) destroy(object *uint32, opcode uint16) error {
	if *object == 0 {
		return nil
	}
	failure := s.wire.request(*object, opcode, waylandWords{})
	*object = 0
	return failure
}

func oneObject(object uint32) waylandWords {
	var words waylandWords
	words.putUint(object)
	return words
}
