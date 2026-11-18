package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The fake spells the opcodes out again instead of importing the
// client's constants, so a wrong number in the client fails the test.
const (
	wlShmCreatePool       uint16 = 0
	wlShmPoolCreateBuffer uint16 = 0
	westonCaptureCreate   uint16 = 1
	westonCaptureCapture  uint16 = 1
	westonCaptureFormat   uint16 = 0
	westonCaptureSize     uint16 = 1
	westonCaptureComplete uint16 = 2
	westonCaptureRetry    uint16 = 3
	westonCaptureFailed   uint16 = 4
)

// The registry names of the fake's globals. The outputs take the names
// from firstOutput on, in order.
const (
	shmGlobal     uint32 = 1
	captureGlobal uint32 = 2
	firstOutput   uint32 = 3
)

// A client that waits for an event the fake never sends fails after
// this timeout instead of at the test runner's limit.
const captureTestTimeout = 5 * time.Second

// capturePattern returns the bytes of a frame whose content depends on
// seed, so a test can tell one scripted frame from another.
func capturePattern(seed byte, size int) []byte {
	pattern := make([]byte, size)
	for index := range pattern {
		pattern[index] = seed + byte(index)
	}
	return pattern
}

type captureWestonOutput struct {
	connector string
	scale     uint32
	width     int
	height    int
	refresh   uint32
}

// captureReply scripts the fake's reply to one capture request:
// complete with a frame filled from seed, retry with a new size, or
// failed with message. A zero width retries with the old size.
type captureReply struct {
	event   uint16
	message string
	width   int
	height  int
	seed    byte
}

// captureWeston is a fake weston on a Unix socket that serves the
// registry, wl_shm, weston_capture_v1, and the outputs. It replies to
// each capture request from its script, and with complete after the
// script runs out.
type captureWeston struct {
	t       *testing.T
	path    string
	outputs []captureWestonOutput
	format  uint32
	replies []captureReply
}

func startCaptureWeston(t *testing.T, format uint32, outputs []captureWestonOutput, replies ...captureReply) *captureWeston {
	t.Helper()
	weston := &captureWeston{
		t:       t,
		path:    filepath.Join(t.TempDir(), "wayland-capture"),
		outputs: outputs,
		format:  format,
		replies: replies,
	}
	listener := listenOnSocket(t, weston.path)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go weston.serve(connection.(*net.UnixConn))
		}
	}()
	return weston
}

// captureWestonSession is the fake's state for one client connection.
type captureWestonSession struct {
	weston *captureWeston
	socket *net.UnixConn
	wire   *waylandClient

	registry uint32
	shm      uint32
	capture  uint32
	pool     uint32
	// Both map an object id to an index in weston.outputs, so a capture
	// replies with the size of the source's output.
	outputs map[uint32]int
	sources map[uint32]int

	memory    int
	pixels    []byte
	nextReply int
	// awaiting is true while reply runs. A real compositor closes the
	// connection on a capture request while another is pending.
	awaiting bool
	// Descriptors received as ancillary data queue here until a
	// message with a file argument takes one, as in libwayland.
	files []int
}

// serve uses ReadMsgUnix because a plain Read drops the descriptor
// that arrives as ancillary data with create_pool.
func (w *captureWeston) serve(socket *net.UnixConn) {
	session := &captureWestonSession{
		weston:  w,
		socket:  socket,
		wire:    newWaylandClient(socket),
		outputs: map[uint32]int{},
		sources: map[uint32]int{},
		memory:  -1,
	}
	defer session.release()

	var stream []byte
	data := make([]byte, maxWaylandMessage)
	control := make([]byte, unix.CmsgSpace(4))
	for {
		read, controlRead, _, _, err := socket.ReadMsgUnix(data, control)
		if err != nil {
			return
		}
		session.files = append(session.files, session.descriptors(control[:controlRead])...)
		stream = append(stream, data[:read]...)
		for len(stream) >= 8 {
			word := binary.LittleEndian.Uint32(stream[4:])
			size := int(word >> 16)
			if size < 8 || len(stream) < size {
				break
			}
			fields := waylandFields{body: bytes.Clone(stream[8:size])}
			session.handle(binary.LittleEndian.Uint32(stream[:4]), uint16(word), &fields)
			stream = stream[size:]
		}
	}
}

func (s *captureWestonSession) descriptors(control []byte) []int {
	if len(control) == 0 {
		return nil
	}
	messages, err := unix.ParseSocketControlMessage(control)
	if err != nil {
		s.weston.t.Errorf("parsing the control messages of the request: %v", err)
		return nil
	}
	var files []int
	for index := range messages {
		rights, err := unix.ParseUnixRights(&messages[index])
		if err != nil {
			s.weston.t.Errorf("reading the file descriptors of the request: %v", err)
			continue
		}
		files = append(files, rights...)
	}
	return files
}

func (s *captureWestonSession) send(object uint32, opcode uint16, words waylandWords) {
	if err := s.wire.request(object, opcode, words); err != nil {
		s.weston.t.Errorf("sending the event to the client: %v", err)
	}
}

func (s *captureWestonSession) handle(object uint32, opcode uint16, fields *waylandFields) {
	switch {
	case object == displayObject && opcode == wlDisplayGetRegistry:
		s.registry = fields.uint()
		s.announce()
	case object == displayObject && opcode == wlDisplaySync:
		callback := fields.uint()
		var done waylandWords
		done.putUint(0)
		s.send(callback, wlCallbackDone, done)
	case object == s.registry && opcode == wlRegistryBind:
		global := fields.uint()
		name := fields.text()
		_ = fields.uint()
		s.bind(global, name, fields.uint())
	case object == s.capture && opcode == westonCaptureCreate:
		output := fields.uint()
		source := fields.uint()
		id := fields.uint()
		if source != captureSourceFramebuffer {
			s.weston.t.Errorf("the client requested capture source %d, want the framebuffer source %d", source, captureSourceFramebuffer)
		}
		s.sources[id] = s.outputs[output]
		s.describe(id)
	case object == s.shm && opcode == wlShmCreatePool:
		s.pool = fields.uint()
		s.mapPool(s.takeFile(), int(fields.uint()))
	case object == s.pool && opcode == wlShmPoolCreateBuffer:
		s.checkBuffer(fields)
	case opcode == westonCaptureCapture && s.isSource(object):
		s.reply(object)
	}
}

// checkBuffer fails the test on a buffer that a real compositor
// refuses: a stride other than width times four, or a wl_shm format
// other than the enum value for the fourcc.
func (s *captureWestonSession) checkBuffer(fields *waylandFields) {
	_, _ = fields.uint(), fields.uint()
	width := int(fields.uint())
	_ = fields.uint()
	stride := int(fields.uint())
	format := fields.uint()
	if stride != width*4 {
		s.weston.t.Errorf("the buffer has a stride of %d on a width of %d, want %d", stride, width, width*4)
	}
	want := s.weston.format
	switch s.weston.format {
	case formatAR24:
		want = 0
	case formatXR24:
		want = 1
	}
	if format != want {
		s.weston.t.Errorf("the buffer has wl_shm format %d for fourcc 0x%08x, want %d", format, s.weston.format, want)
	}
}

func (s *captureWestonSession) isSource(object uint32) bool {
	_, ours := s.sources[object]
	return ours
}

func (s *captureWestonSession) announce() {
	s.global(shmGlobal, shmInterface, 1)
	s.global(captureGlobal, captureInterface, 1)
	for index := range s.weston.outputs {
		s.global(firstOutput+uint32(index), outputInterface, 4)
	}
}

func (s *captureWestonSession) global(global uint32, name string, version uint32) {
	var words waylandWords
	words.putUint(global)
	words.putText(name)
	words.putUint(version)
	s.send(s.registry, wlRegistryGlobal, words)
}

func (s *captureWestonSession) bind(global uint32, name string, id uint32) {
	switch name {
	case shmInterface:
		s.shm = id
	case captureInterface:
		s.capture = id
	case outputInterface:
		index := int(global - firstOutput)
		s.outputs[id] = index
		s.sendOutput(id, s.weston.outputs[index])
	}
}

// sendOutput sends the events of a newly bound wl_output in weston's
// order: geometry, scale, mode, name, and done.
func (s *captureWestonSession) sendOutput(id uint32, output captureWestonOutput) {
	var geometry waylandWords
	for _, value := range []uint32{0, 0, 600, 340, 0} {
		geometry.putUint(value)
	}
	geometry.putText("LGD")
	geometry.putText("LG ULTRAWIDE")
	geometry.putUint(0)
	s.send(id, wlOutputGeometry, geometry)

	var scale waylandWords
	scale.putUint(output.scale)
	s.send(id, wlOutputScale, scale)

	var mode waylandWords
	for _, value := range []uint32{1, uint32(output.width), uint32(output.height), output.refresh} {
		mode.putUint(value)
	}
	s.send(id, wlOutputMode, mode)

	var name waylandWords
	name.putText(output.connector)
	s.send(id, wlOutputName, name)
	s.send(id, wlOutputDone, waylandWords{})
}

func (s *captureWestonSession) describe(source uint32) {
	output := s.weston.outputs[s.sources[source]]
	var format waylandWords
	format.putUint(s.weston.format)
	s.send(source, westonCaptureFormat, format)
	s.sendSize(source, output.width, output.height)
}

func (s *captureWestonSession) sendSize(source uint32, width, height int) {
	var size waylandWords
	size.putUint(uint32(width))
	size.putUint(uint32(height))
	s.send(source, westonCaptureSize, size)
}

// takeFile returns the oldest received descriptor. Descriptors arrive
// in the order they were sent, so it belongs to the create_pool being
// handled.
func (s *captureWestonSession) takeFile() int {
	if len(s.files) == 0 {
		s.weston.t.Error("the client sent create_pool with no descriptor")
		return -1
	}
	file := s.files[0]
	s.files = s.files[1:]
	return file
}

// mapPool maps the client's descriptor, and the fake writes frames
// through that mapping as weston writes into a wl_shm buffer. So a
// test that reads the frame from the client's mapping proves that the
// descriptor reached the fake.
func (s *captureWestonSession) mapPool(file, size int) {
	s.release()
	if file < 0 {
		return
	}
	pixels, err := unix.Mmap(file, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		s.weston.t.Errorf("mapping the capture buffer: %v", err)
		return
	}
	s.memory, s.pixels = file, pixels
}

func (s *captureWestonSession) release() {
	if s.pixels != nil {
		if err := unix.Munmap(s.pixels); err != nil {
			s.weston.t.Errorf("unmapping the capture buffer: %v", err)
		}
		s.pixels = nil
	}
	if s.memory >= 0 {
		if err := unix.Close(s.memory); err != nil {
			s.weston.t.Errorf("closing the capture buffer file: %v", err)
		}
		s.memory = -1
	}
}

func (s *captureWestonSession) reply(source uint32) {
	if s.awaiting {
		s.weston.t.Error("the client sent a second capture before complete, retry or failed, " +
			"which is the sequence protocol error")
	}
	s.awaiting = true
	defer func() { s.awaiting = false }()
	reply := captureReply{event: westonCaptureComplete}
	if s.nextReply < len(s.weston.replies) {
		reply = s.weston.replies[s.nextReply]
	}
	s.nextReply++
	switch reply.event {
	case westonCaptureComplete:
		copy(s.pixels, capturePattern(reply.seed, len(s.pixels)))
		s.send(source, westonCaptureComplete, waylandWords{})
	case westonCaptureRetry:
		if reply.width != 0 {
			s.sendSize(source, reply.width, reply.height)
		}
		s.send(source, westonCaptureRetry, waylandWords{})
	case westonCaptureFailed:
		var words waylandWords
		words.putText(reply.message)
		s.send(source, westonCaptureFailed, words)
	}
}

// defaultCaptureOutputs is one output small enough to compare byte for
// byte.
func defaultCaptureOutputs() []captureWestonOutput {
	return []captureWestonOutput{{connector: "HDMI-A-1", scale: 2, width: 8, height: 4, refresh: 59997}}
}

func TestACaptureTakesTheFrameTheCompositorWrote(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24, defaultCaptureOutputs(),
		captureReply{event: westonCaptureComplete, seed: 7})
	session, err := openCapture(weston.path, "HDMI-A-1", captureTestTimeout)
	if err != nil {
		t.Fatalf("opening the capture session: %v", err)
	}
	t.Cleanup(func() { _ = session.close() })

	frame, err := session.frame()
	if err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	if want := capturePattern(7, 8*4*4); !bytes.Equal(frame, want) {
		t.Errorf("the frame is %v, want %v", frame, want)
	}
	want := captureScreen{
		Connector:   "HDMI-A-1",
		Width:       8,
		Height:      4,
		Scale:       2,
		Refresh:     60,
		Format:      formatXR24,
		PixelFormat: "bgr0",
	}
	if screen := session.screen(); screen != want {
		t.Errorf("the session reports %+v, want %+v", screen, want)
	}
}

func TestACaptureBindsTheOutputWithTheRequestedConnector(t *testing.T) {
	weston := startCaptureWeston(t, formatAR24, []captureWestonOutput{
		{connector: "HDMI-A-1", scale: 2, width: 8, height: 4, refresh: 59997},
		{connector: "DP-1", scale: 1, width: 12, height: 6, refresh: 30000},
	}, captureReply{event: westonCaptureComplete, seed: 3})
	session, err := openCapture(weston.path, "DP-1", captureTestTimeout)
	if err != nil {
		t.Fatalf("opening the capture session: %v", err)
	}
	t.Cleanup(func() { _ = session.close() })

	frame, err := session.frame()
	if err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	if want := capturePattern(3, 12*4*6); !bytes.Equal(frame, want) {
		t.Errorf("the frame is %d bytes starting %v, want DP-1's %d bytes starting %v", len(frame), frame[:min(8, len(frame))], len(want), want[:8])
	}
	want := captureScreen{
		Connector:   "DP-1",
		Width:       12,
		Height:      6,
		Scale:       1,
		Refresh:     30,
		Format:      formatAR24,
		PixelFormat: "bgra",
	}
	if screen := session.screen(); screen != want {
		t.Errorf("the session reports %+v, want %+v", screen, want)
	}
}

func TestACaptureOnAStillReallocatesWhenTheSizeChanges(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24, defaultCaptureOutputs(),
		captureReply{event: westonCaptureRetry, width: 16, height: 8},
		captureReply{event: westonCaptureComplete, seed: 5})
	session, err := openCapture(weston.path, "HDMI-A-1", captureTestTimeout)
	if err != nil {
		t.Fatalf("opening the capture session: %v", err)
	}
	t.Cleanup(func() { _ = session.close() })

	frame, err := session.frame()
	if err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	if want := capturePattern(5, 16*4*8); !bytes.Equal(frame, want) {
		t.Errorf("the frame is %d bytes starting %v, want the new size's %d bytes starting %v", len(frame), frame[:min(8, len(frame))], len(want), want[:8])
	}
	screen := session.screen()
	if screen.Width != 16 || screen.Height != 8 {
		t.Errorf("the session reports %dx%d, want 16x8", screen.Width, screen.Height)
	}
}

func TestACaptureOnAClipEndsWhenTheSizeChanges(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24, defaultCaptureOutputs(),
		captureReply{event: westonCaptureRetry, width: 16, height: 8},
		captureReply{event: westonCaptureComplete, seed: 5})
	session, err := openCapture(weston.path, "HDMI-A-1", captureTestTimeout)
	if err != nil {
		t.Fatalf("opening the capture session: %v", err)
	}
	t.Cleanup(func() { _ = session.close() })
	session.onRetry(captureEndsOnChange)

	_, err = session.frame()
	var changed *captureSizeChange
	if !errors.As(err, &changed) {
		t.Fatalf("reading a frame returned %v, want a size change", err)
	}
	want := captureSizeChange{OldWidth: 8, OldHeight: 4, Width: 16, Height: 8}
	if *changed != want {
		t.Errorf("the size change is %+v, want %+v", *changed, want)
	}
}

func TestACaptureFailsWithTheCompositorsMessageWhenRefused(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24, defaultCaptureOutputs(),
		captureReply{event: westonCaptureFailed, message: "unauthorized"})
	session, err := openCapture(weston.path, "HDMI-A-1", captureTestTimeout)
	if err != nil {
		t.Fatalf("opening the capture session: %v", err)
	}
	t.Cleanup(func() { _ = session.close() })

	_, err = session.frame()
	var failure *captureFailure
	if !errors.As(err, &failure) {
		t.Fatalf("reading a frame returned %v, want a refusal", err)
	}
	if failure.Message != "unauthorized" {
		t.Errorf("the refusal message is %q, want %q", failure.Message, "unauthorized")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("the error is %q, want it to contain the compositor's message", err)
	}
}

func TestACaptureInAFormatFfmpegHasNoNameForFailsToOpen(t *testing.T) {
	weston := startCaptureWeston(t, 0x36314752, defaultCaptureOutputs())
	_, err := openCapture(weston.path, "HDMI-A-1", captureTestTimeout)
	if err == nil {
		t.Fatal("openCapture returned no error for a format that ffmpeg has no name for")
	}
	for _, want := range []string{"RG16", "0x36314752"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to contain %s", err, want)
		}
	}
}

func TestACaptureWaitsForEachReplyBeforeTheNextRequest(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24, defaultCaptureOutputs(),
		captureReply{event: westonCaptureComplete, seed: 1},
		captureReply{event: westonCaptureComplete, seed: 2},
		captureReply{event: westonCaptureRetry, width: 8, height: 4},
		captureReply{event: westonCaptureComplete, seed: 3})

	session, err := openCapture(weston.path, "HDMI-A-1", captureTestTimeout)
	if err != nil {
		t.Fatalf("opening the capture session: %v", err)
	}
	defer func() { _ = session.close() }()

	for take := range 3 {
		frame, err := session.frame()
		if err != nil {
			t.Fatalf("reading frame %d: %v", take, err)
		}
		if len(frame) != 8*capturePixelBytes*4 {
			t.Fatalf("frame %d is %d bytes, want %d", take, len(frame), 8*capturePixelBytes*4)
		}
	}
}
