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

// The opcodes are spelled again here instead of read from the
// client, so a client that numbered one wrong fails against the
// protocol and not against itself.
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

// The global names the fake hands out: wl_shm, weston_capture_v1,
// then one per output counting up from three.
const (
	shmGlobal     uint32 = 1
	captureGlobal uint32 = 2
	firstOutput   uint32 = 3
)

// The bound on a drill that hangs, so a client that waits for an
// event the fake never sends fails in seconds and not at the test
// runner's own limit.
const captureDrillTimeout = 5 * time.Second

// The frame the fake writes through the client's own descriptor: a
// byte pattern a test can tell from any other seed's.
func capturePattern(seed byte, size int) []byte {
	pattern := make([]byte, size)
	for index := range pattern {
		pattern[index] = seed + byte(index)
	}
	return pattern
}

// What one output of the fake states about itself on wl_output: its
// connector name, its scale, and its current mode.
type captureWestonOutput struct {
	connector string
	scale     uint32
	width     int
	height    int
	refresh   uint32
}

// What the fake answers one capture request with: complete with a
// seeded frame, retry with a new size, or failed with a message.
type captureAnswer struct {
	event   uint16
	message string
	width   int
	height  int
	seed    byte
}

// The fixture is the server half of weston 14.0.2's capture protocol
// on a Unix socket: the registry, wl_shm, weston_capture_v1, and the
// outputs, with scripted answers to each capture request.
type captureWeston struct {
	t       *testing.T
	path    string
	outputs []captureWestonOutput
	format  uint32
	answers []captureAnswer
}

func startCaptureWeston(t *testing.T, format uint32, outputs []captureWestonOutput, answers ...captureAnswer) *captureWeston {
	t.Helper()
	weston := &captureWeston{
		t:       t,
		path:    filepath.Join(t.TempDir(), "wayland-capture"),
		outputs: outputs,
		format:  format,
		answers: answers,
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

// One connection to the fake: the object ids the client chose, the
// buffer it shared, and how far through the scripted answers it is.
type captureWestonSession struct {
	weston *captureWeston
	socket *net.UnixConn
	wire   *waylandClient

	registry uint32
	shm      uint32
	capture  uint32
	pool     uint32
	// A source is keyed to the output its create request named, so a
	// capture on it answers that output's size and not the first
	// one's.
	outputs map[uint32]int
	sources map[uint32]int

	memory int
	pixels []byte
	answer int
	// The fake holds the client to the protocol's own rule: a capture
	// that arrives before the last one retired is the sequence error,
	// which on a real compositor kills the connection.
	awaiting bool
	// A descriptor belongs to the message whose argument list
	// declares one, not to whichever message the same read happened
	// to carry first, which is how libwayland keeps them too.
	files []int
}

// ReadMsgUnix and not Read, because a plain Read drops the descriptor
// that arrives as ancillary data beside create_pool.
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
		s.weston.t.Error(err)
		return nil
	}
	var files []int
	for index := range messages {
		rights, err := unix.ParseUnixRights(&messages[index])
		if err != nil {
			s.weston.t.Error(err)
			continue
		}
		files = append(files, rights...)
	}
	return files
}

func (s *captureWestonSession) send(object uint32, opcode uint16, words waylandWords) {
	if err := s.wire.request(object, opcode, words); err != nil {
		s.weston.t.Error(err)
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
			s.weston.t.Errorf("the client asked for capture source %d, want the framebuffer source %d", source, captureSourceFramebuffer)
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

// The fake checks the two things the buffer states that a real
// compositor would refuse: a stride other than width times four, and
// a wl_shm format other than the enum value for its fourcc.
func (s *captureWestonSession) checkBuffer(fields *waylandFields) {
	_, _ = fields.uint(), fields.uint()
	width := int(fields.uint())
	_ = fields.uint()
	stride := int(fields.uint())
	format := fields.uint()
	if stride != width*4 {
		s.weston.t.Errorf("the buffer states a stride of %d on a width of %d, want %d", stride, width, width*4)
	}
	want := s.weston.format
	switch s.weston.format {
	case formatAR24:
		want = 0
	case formatXR24:
		want = 1
	}
	if format != want {
		s.weston.t.Errorf("the buffer states wl_shm format %d for fourcc 0x%08x, want %d", format, s.weston.format, want)
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
		s.burst(id, s.weston.outputs[index])
	}
}

// An output's first batch in the order weston sends it: geometry,
// scale, mode, name, and the done event that ends it.
func (s *captureWestonSession) burst(id uint32, output captureWestonOutput) {
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
	s.state(source, output.width, output.height)
}

func (s *captureWestonSession) state(source uint32, width, height int) {
	var size waylandWords
	size.putUint(uint32(width))
	size.putUint(uint32(height))
	s.send(source, westonCaptureSize, size)
}

// The descriptors arrive in the order they were sent, so the next
// one belongs to this create_pool.
func (s *captureWestonSession) takeFile() int {
	if len(s.files) == 0 {
		s.weston.t.Error("the client sent create_pool with no descriptor")
		return -1
	}
	file := s.files[0]
	s.files = s.files[1:]
	return file
}

// The fake maps the descriptor and writes the frame through it, the
// way weston writes into a client's wl_shm buffer, so the bytes the
// client reads from its own mapping are the proof the pool crossed
// the socket.
func (s *captureWestonSession) mapPool(file, size int) {
	s.release()
	if file < 0 {
		return
	}
	pixels, err := unix.Mmap(file, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		s.weston.t.Error(err)
		return
	}
	s.memory, s.pixels = file, pixels
}

func (s *captureWestonSession) release() {
	if s.pixels != nil {
		if err := unix.Munmap(s.pixels); err != nil {
			s.weston.t.Error(err)
		}
		s.pixels = nil
	}
	if s.memory >= 0 {
		if err := unix.Close(s.memory); err != nil {
			s.weston.t.Error(err)
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
	answer := captureAnswer{event: westonCaptureComplete}
	if s.answer < len(s.weston.answers) {
		answer = s.weston.answers[s.answer]
	}
	s.answer++
	switch answer.event {
	case westonCaptureComplete:
		copy(s.pixels, capturePattern(answer.seed, len(s.pixels)))
		s.send(source, westonCaptureComplete, waylandWords{})
	case westonCaptureRetry:
		if answer.width != 0 {
			s.state(source, answer.width, answer.height)
		}
		s.send(source, westonCaptureRetry, waylandWords{})
	case westonCaptureFailed:
		var words waylandWords
		words.putText(answer.message)
		s.send(source, westonCaptureFailed, words)
	}
}

// The one output most drills serve: eight by four pixels at scale 2,
// small enough to compare byte for byte.
func labCaptureOutputs() []captureWestonOutput {
	return []captureWestonOutput{{connector: "HDMI-A-1", scale: 2, width: 8, height: 4, refresh: 59997}}
}

func TestACaptureTakesTheFrameTheCompositorWrote(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24, labCaptureOutputs(),
		captureAnswer{event: westonCaptureComplete, seed: 7})
	session, err := openCapture(weston.path, "HDMI-A-1", captureDrillTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.close() })

	frame, err := session.frame()
	if err != nil {
		t.Fatal(err)
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

func TestACaptureBindsTheOutputTheConnectorNames(t *testing.T) {
	weston := startCaptureWeston(t, formatAR24, []captureWestonOutput{
		{connector: "HDMI-A-1", scale: 2, width: 8, height: 4, refresh: 59997},
		{connector: "DP-1", scale: 1, width: 12, height: 6, refresh: 30000},
	}, captureAnswer{event: westonCaptureComplete, seed: 3})
	session, err := openCapture(weston.path, "DP-1", captureDrillTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.close() })

	frame, err := session.frame()
	if err != nil {
		t.Fatal(err)
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
	weston := startCaptureWeston(t, formatXR24, labCaptureOutputs(),
		captureAnswer{event: westonCaptureRetry, width: 16, height: 8},
		captureAnswer{event: westonCaptureComplete, seed: 5})
	session, err := openCapture(weston.path, "HDMI-A-1", captureDrillTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.close() })

	frame, err := session.frame()
	if err != nil {
		t.Fatal(err)
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
	weston := startCaptureWeston(t, formatXR24, labCaptureOutputs(),
		captureAnswer{event: westonCaptureRetry, width: 16, height: 8},
		captureAnswer{event: westonCaptureComplete, seed: 5})
	session, err := openCapture(weston.path, "HDMI-A-1", captureDrillTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.close() })
	session.onRetry(captureEndsTheFrame)

	_, err = session.frame()
	var changed *captureSizeChange
	if !errors.As(err, &changed) {
		t.Fatalf("the frame answered %v, want a size change", err)
	}
	want := captureSizeChange{OldWidth: 8, OldHeight: 4, Width: 16, Height: 8}
	if *changed != want {
		t.Errorf("the size change is %+v, want %+v", *changed, want)
	}
}

func TestACaptureTheCompositorRefusesCarriesItsWords(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24, labCaptureOutputs(),
		captureAnswer{event: westonCaptureFailed, message: "unauthorized"})
	session, err := openCapture(weston.path, "HDMI-A-1", captureDrillTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.close() })

	_, err = session.frame()
	var failure *captureFailure
	if !errors.As(err, &failure) {
		t.Fatalf("the frame answered %v, want a refusal", err)
	}
	if failure.Message != "unauthorized" {
		t.Errorf("the refusal carries %q, want %q", failure.Message, "unauthorized")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("the error reads %q, which does not carry the compositor's word", err)
	}
}

func TestACaptureInAFormatFfmpegHasNoNameForFailsToOpen(t *testing.T) {
	weston := startCaptureWeston(t, 0x36314752, labCaptureOutputs())
	_, err := openCapture(weston.path, "HDMI-A-1", captureDrillTimeout)
	if err == nil {
		t.Fatal("a capture opened in a format ffmpeg has no name for")
	}
	for _, want := range []string{"RG16", "0x36314752"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error reads %q, which does not name %s", err, want)
		}
	}
}

// A clip takes frame after frame and never has two captures in
// flight, which is the one protocol rule that ends the connection
// when it is broken.
func TestACaptureWaitsForEachAnswerBeforeTheNextRequest(t *testing.T) {
	weston := startCaptureWeston(t, formatXR24, labCaptureOutputs(),
		captureAnswer{event: westonCaptureComplete, seed: 1},
		captureAnswer{event: westonCaptureComplete, seed: 2},
		captureAnswer{event: westonCaptureRetry, width: 8, height: 4},
		captureAnswer{event: westonCaptureComplete, seed: 3})

	session, err := openCapture(weston.path, "HDMI-A-1", captureDrillTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.close() }()

	for take := range 3 {
		frame, err := session.frame()
		if err != nil {
			t.Fatalf("frame %d: %v", take, err)
		}
		if len(frame) != 8*capturePixelBytes*4 {
			t.Fatalf("frame %d holds %d bytes", take, len(frame))
		}
	}
}
