package main

// This file is the client that takes frames off the capture socket.
// It is a connection of its own, beside the standing watch in
// wayland.go, because it opens on a different socket, the one the
// layout module admits captures from, and lives for one request
// rather than for the compositor's life. It shares the wire code in
// wayland.go and adds the requests the watch never sends: wl_shm's
// pool and buffer, and weston_capture_v1's create and capture.
//
// The protocol is weston-output-capture.xml from libweston-14-dev
// 14.0.2-5, read 2026-09-16, at version 1 of both of its interfaces.

import (
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// The two interfaces this client binds beside wl_output: wl_shm for
// the buffer the compositor writes into, and weston_capture_v1 for
// the source that fills it. Both are bound at version 1.
const (
	shmInterface     = "wl_shm"
	shmVersion       = 1
	captureInterface = "weston_capture_v1"
	captureVersion   = 1
)

// The request opcodes. An opcode is the position of the request in
// its interface's protocol definition, and the wire carries no
// names, so these numbers are the contract.
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

// The capture source's five events. format and size describe the
// buffer the client has to offer, and arrive after create and again
// before a retry. complete, retry, and failed each end one capture,
// and the client waits for one of the three before it sends the next
// capture request.
const (
	captureFormatEvent   uint16 = 0
	captureSizeEvent     uint16 = 1
	captureCompleteEvent uint16 = 2
	captureRetryEvent    uint16 = 3
	captureFailedEvent   uint16 = 4
)

// The wl_output scale event. The watch in wayland.go skips it; this
// client reads it because the info document reports the scale, so a
// client in logical pixels can convert to the frame's own.
const outputScaleEvent uint16 = 3

// The framebuffer source "copies the contents of the final
// framebuffer", "temporarily disables all use of hardware planes",
// and "is always available", so a film on an overlay plane is
// composited into the frame. writeback is "often not available" in
// Weston 14, and blending omits the output's color transform.
const captureSourceFramebuffer uint32 = 1

// The four DRM fourccs weston's GL renderer reports, as the format
// event carries them: the four characters of the name, least
// significant byte first.
const (
	formatAR24 uint32 = 0x34325241
	formatXR24 uint32 = 0x34325258
	formatXB24 uint32 = 0x34324258
	formatAB24 uint32 = 0x34324241
)

// wl_shm numbers two formats itself, ARGB8888 as 0 and XRGB8888 as 1,
// and every other format in its enum is the DRM fourcc unchanged.
const (
	shmFormatARGB8888 uint32 = 0
	shmFormatXRGB8888 uint32 = 1
)

// Every format the compositor reports is four bytes a pixel, and the
// stride is exactly width times four: weston's PBO read path assumes
// a packed buffer.
const capturePixelBytes = 4

// A retry means the size or format changed and the client
// reallocated. An output that changes on every capture would loop
// forever, so one frame allows this many retries before it fails.
const captureRetryLimit = 4

// The memfd's name. It appears in /proc and nowhere else.
const captureBufferName = "liken-capture"

// The ffmpeg -pixel_format name for each fourcc weston reports. The
// GL renderer's read format is not fixed, so the sidecar derives the
// name from the format event rather than stating bgr0.
var capturePixelFormats = map[uint32]string{
	formatXR24: "bgr0",
	formatAR24: "bgra",
	formatXB24: "rgb0",
	formatAB24: "rgba",
}

// The error names the fourcc as characters and as hex, because a
// person reads the characters and drm_fourcc.h lists the hex.
func capturePixelFormat(format uint32) (string, error) {
	name, mapped := capturePixelFormats[format]
	if !mapped {
		return "", fmt.Errorf("the compositor captures in %s (0x%08x), which no ffmpeg pixel format carries", fourccName(format), format)
	}
	return name, nil
}

// A fourcc spells itself: its four bytes are the four characters of
// its name, least significant byte first.
func fourccName(format uint32) string {
	return string([]byte{byte(format), byte(format >> 8), byte(format >> 16), byte(format >> 24)})
}

// The fourcc to wl_shm enum map: the two formats wl_shm renumbers,
// and every other one unchanged.
func shmFormat(format uint32) uint32 {
	switch format {
	case formatAR24:
		return shmFormatARGB8888
	case formatXR24:
		return shmFormatXRGB8888
	}
	return format
}

// What the HTTP layer states about a screen and hands to ffmpeg: the
// size and format of the frame, and the scale and refresh the info
// document reports.
type captureScreen struct {
	Connector   string
	Width       int
	Height      int
	Scale       int
	Refresh     int
	Format      uint32
	PixelFormat string
}

// A failed event is its own error type, because the HTTP layer turns
// it into a 500 capture-denied with weston's own word as the detail,
// and every other error into a 503.
type captureFailure struct {
	Message string
}

func (f *captureFailure) Error() string {
	return "the compositor refused the capture: " + f.Message
}

// A size change carries both sizes, because the clip that ends on it
// logs the old and the new.
type captureSizeChange struct {
	OldWidth  int
	OldHeight int
	Width     int
	Height    int
}

func (c *captureSizeChange) Error() string {
	return fmt.Sprintf("the capture changed size from %dx%d to %dx%d", c.OldWidth, c.OldHeight, c.Width, c.Height)
}

// The two answers to a retry. A still reallocates the buffer at the
// new size and captures again. A clip ends on the change, because
// ffmpeg's -video_size and -pixel_format are stated once when it
// starts and cannot follow one.
type captureRetryBehavior int

const (
	captureReallocates captureRetryBehavior = iota
	captureEndsTheFrame
)

// What one output states that this client reads: its connector name,
// its scale, and the refresh of its current mode.
type captureOutput struct {
	connector string
	scale     int
	refresh   int
}

// One session holds one connection, one output, one capture source
// on it, and one buffer the compositor writes every frame into. The
// buffer is a memfd, 33 MB at 4K, mapped for the session's life and
// unmapped when the request ends.
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

// openCapture dials the capture socket, binds the output the
// connector names and a capture source on it, and reads the format
// and size events that describe the buffer to allocate.
func openCapture(socketPath, connector string, timeout time.Duration) (*captureSession, error) {
	socket, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, fmt.Errorf("the capture socket %s: %w", socketPath, err)
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

// The retry behavior is set after open, because open itself always
// reallocates: no frame has been promised to an encoder yet. The
// caller sets it once the form is chosen, a still or a clip.
func (s *captureSession) onRetry(behavior captureRetryBehavior) {
	s.behavior = behavior
}

// frame takes exactly one frame and answers the mapped pixels of the
// whole frame. The slice is the buffer itself, so it is valid until
// the next call to frame or to close.
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
	return nil, fmt.Errorf("the compositor asked to retry %d captures in a row", captureRetryLimit)
}

func (s *captureSession) close() error {
	return errors.Join(
		s.release(),
		s.destroy(&s.source, captureSourceDestroy),
		s.destroy(&s.capture, captureV1Destroy),
		s.socket.Close(),
	)
}

// Two roundtrips. The first reads the registry and binds every
// output in it; an output's own name, scale, and mode events follow
// its bind, so the second roundtrip is what reads them.
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
		return fmt.Errorf("the compositor serves no output named %s", connector)
	}
	return s.createSource()
}

// A roundtrip is a sync whose done callback marks the end of what
// the compositor had to say in answer to every request before it.
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

// Every read carries the session's deadline, so a compositor that
// stops answering ends the request rather than holding it. A
// wl_display error event is a protocol error, and the compositor
// closes the connection after it, so it ends the session here with
// the compositor's own message.
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
		return waylandEvent{}, fmt.Errorf("the compositor refused object %d with code %d: %s", object, code, event.fields.text())
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

// The mode event states the refresh in millihertz. The info document
// states it in whole hertz, rounded the way the kernel rounds its own
// vrefresh, so the number a caller reads here is the number the
// Display's status reports.
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

// create makes the source. The source states its format and size
// before any capture, and those two events are what the buffer is
// allocated from.
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

// One allocation makes three things: a memfd of the frame's size,
// mapped into this process, a wl_shm pool over that descriptor, and
// a wl_buffer of the whole pool in the compositor's own format.
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
		return fmt.Errorf("the compositor states a capture size of %dx%d", s.info.Width, s.info.Height)
	}
	file, err := unix.MemfdCreate(captureBufferName, unix.MFD_CLOEXEC)
	if err != nil {
		return fmt.Errorf("a memfd for the capture buffer: %w", err)
	}
	s.memory = file
	if err := unix.Ftruncate(file, int64(size)); err != nil {
		return fmt.Errorf("sizing the capture buffer to %d bytes: %w", size, err)
	}
	pixels, err := unix.Mmap(file, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mapping the %d bytes of the capture buffer: %w", size, err)
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

// A Wayland fd argument takes no word in the message body. The
// descriptor travels beside the bytes as SCM_RIGHTS ancillary data,
// and the compositor reads it from the same sendmsg.
func (s *captureSession) sendWithFile(object uint32, opcode uint16, words waylandWords, file int) error {
	_, _, err := s.socket.WriteMsgUnix(waylandMessage(object, opcode, words), unix.UnixRights(file), nil)
	return err
}

// await reads until complete, retry, or failed. A second capture
// request while one is in flight is the sequence protocol error,
// which kills the connection, so the client sends the next capture
// only after one of the three has arrived.
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

// A retry means the compositor could not use the buffer it was
// given, and the format and size events before it say what it needs
// now. A retry with nothing changed reallocates and captures again.
// A retry that changed the size or the format is the one case the
// caller decides: a still reallocates, and a clip ends. A clip ends
// on a format change as well as on a size change, because
// -pixel_format is stated once when ffmpeg starts, and every frame
// after the change would be read under the old one.
func (s *captureSession) retry(width, height int, format uint32) error {
	changed := s.info.Width != width || s.info.Height != height || s.info.Format != format
	if s.behavior == captureEndsTheFrame && changed {
		return &captureSizeChange{OldWidth: width, OldHeight: height, Width: s.info.Width, Height: s.info.Height}
	}
	return s.allocate()
}

// release gives back the buffer, the pool, the mapping, and the
// memfd, on a reallocation and on close alike, so a session never
// holds two frames of memory.
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

// An object this session never made has id zero and is nothing to
// destroy, so close is safe at every stage of a failed open.
func (s *captureSession) destroy(object *uint32, opcode uint16) error {
	if *object == 0 {
		return nil
	}
	failure := s.wire.request(*object, opcode, waylandWords{})
	*object = 0
	return failure
}

// Most requests in this file carry one object id and nothing else,
// so that argument list has a spelling of its own.
func oneObject(object uint32) waylandWords {
	var words waylandWords
	words.putUint(object)
	return words
}
