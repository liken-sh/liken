// Package phd2test is a fake PHD2 event server for tests. It answers
// the way PHD2's event server does (src/event_server.cpp in
// OpenPHDGuiding/phd2): each message is one line of JSON that ends in
// CR LF, a new connection receives the catch-up events Version and
// AppState first, and each request with an id receives one JSON-RPC 2.0
// answer with the same id. A client dials it through the Server's
// DialContext, and each dial is one end of a net.Pipe, so a test runs
// in a synctest bubble on the fake clock, which a real socket stops.
//
// The package phd2 tests its client against it, and the operator tests
// its guider steps against one Server for each guider pod. The answers
// and the events around them follow the transcript in phd2/testdata/,
// which a real PHD2 sent.
package phd2test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"syscall"
)

// Server is one fake PHD2. Its fields are its state, and Set changes
// them under the Server's lock.
type Server struct {
	mu sync.Mutex
	// AppState is what get_app_state answers and the catch-up AppState
	// event says.
	AppState   string
	Calibrated bool
	// Equipment is what get_connected answers. set_connected sets it.
	Equipment bool
	// Scale is the pixel scale in arc-seconds per pixel, or nil for a
	// camera whose pixel size PHD2 does not know.
	Scale *float64
	// Refuse is the message of an error answer to set_connected.
	Refuse string
	// Refusals holds the message of an error answer to each method it
	// names, such as find_star with "could not find star".
	Refusals map[string]string
	// Hold names a method that the Server reads and never answers.
	Hold string
	// Exposure is the guide exposure in milliseconds that set_exposure
	// set last.
	Exposure int
	// Star is the position in pixels that find_star selects.
	Star [2]float64
	// HoldSettle keeps each settle of a guide or a dither open after
	// its SettleBegin and one Settling event, until Settle ends it.
	HoldSettle bool
	// SettleFailure is the reason that each SettleDone reports, or
	// empty for a settle that settles.
	SettleFailure string

	// settling names the operation whose settle HoldSettle holds open,
	// guide or dither. PHD2 refuses another guide or dither until it
	// ends.
	settling string
	params   map[string]json.RawMessage
	methods  []string
	conns    []net.Conn
	dials    int
	// exited records a Cut. A PHD2 that exited has no listener, so
	// each later dial is refused.
	exited bool
	// Failed receives what the Server could not read. A test checks it.
	failed []string
}

// New answers a PHD2 that runs, is idle, and has no equipment
// connected, as PHD2 is after it starts. Its star is where the
// recorded session's star was.
func New() *Server {
	return &Server{AppState: "Stopped", Star: [2]float64{1045.09, 44.96}, params: map[string]json.RawMessage{}}
}

// DialContext answers one end of a new pipe, and serves the other. A
// Server that was cut refuses the dial with ECONNREFUSED, as a host with
// no listener on the port does.
func (s *Server) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
	}
	server, client := net.Pipe()
	s.conns = append(s.conns, server)
	s.dials++
	s.mu.Unlock()
	go s.serve(server)
	return client, nil
}

// Event builds an event with the four attributes that PHD2 adds to
// each one.
func Event(name string, attributes map[string]any) map[string]any {
	e := map[string]any{"Event": name, "Timestamp": 1790000000.5, "Host": "guider", "Inst": 1}
	for k, v := range attributes {
		e[k] = v
	}
	return e
}

// GuideStep builds a GuideStep event with its distances in pixels.
func GuideStep(frame int, ra, dec float64) map[string]any {
	return Event("GuideStep", map[string]any{
		"Frame": frame, "Time": float64(frame), "Mount": "INDI Mount [Telescope Simulator]",
		"dx": 0.1, "dy": 0.2, "RADistanceRaw": ra, "DECDistanceRaw": dec,
		"RADistanceGuide": ra, "DECDistanceGuide": dec,
		"StarMass": 23456, "SNR": 41.25, "HFD": 2.31, "AvgDist": 0.4,
	})
}

// send writes one message to one connection. net.Pipe serializes
// concurrent writes, so an event and an answer never interleave.
func send(conn net.Conn, message map[string]any) {
	body, _ := json.Marshal(message)
	_, _ = conn.Write(append(body, '\r', '\n'))
}

// Broadcast sends one event to every open connection.
func (s *Server) Broadcast(e map[string]any) {
	s.mu.Lock()
	conns := append([]net.Conn(nil), s.conns...)
	s.mu.Unlock()
	for _, c := range conns {
		send(c, e)
	}
}

// Cut closes every connection and refuses each later dial, as a PHD2
// that exits does.
func (s *Server) Cut() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.exited = true
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

func (s *Server) serve(conn net.Conn) {
	defer conn.Close()
	s.mu.Lock()
	state := s.AppState
	s.mu.Unlock()
	send(conn, Event("Version", map[string]any{"PHDVersion": "2.6.14", "PHDSubver": "", "OverlapSupport": true, "MsgVersion": 1}))
	send(conn, Event("AppState", map[string]any{"State": state}))
	lines := bufio.NewScanner(conn)
	for lines.Scan() {
		var request struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     *int            `json:"id"`
		}
		if err := json.Unmarshal(lines.Bytes(), &request); err != nil {
			s.mu.Lock()
			s.failed = append(s.failed, lines.Text())
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		s.methods = append(s.methods, request.Method)
		held := request.Method == s.Hold
		s.mu.Unlock()
		if held {
			continue
		}
		answer := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		a := s.answer(request.Method, request.Params)
		for _, e := range a.before {
			send(conn, e)
		}
		if a.failure != "" {
			answer["error"] = map[string]any{"code": 1, "message": a.failure}
		} else {
			answer["result"] = a.result
		}
		if request.ID != nil {
			send(conn, answer)
		}
		for _, e := range a.after {
			send(conn, e)
		}
	}
}

// reply is the Server's answer to one method: the events it sends
// before the answer, the result or the failure, and the events after.
type reply struct {
	before  []map[string]any
	result  any
	failure string
	after   []map[string]any
}

func result(v any, after ...map[string]any) reply    { return reply{result: v, after: after} }
func failure(message string) reply                   { return reply{failure: message} }
func (r reply) first(before ...map[string]any) reply { r.before = before; return r }

// answer runs one method the way event_server.cpp does, with the
// error messages of event_server.cpp and phdcontrol.cpp.
func (s *Server) answer(method string, params json.RawMessage) reply {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params[method] = params
	if message, refused := s.Refusals[method]; refused {
		return failure(message)
	}
	switch method {
	case "get_app_state":
		return result(s.AppState)
	case "get_calibrated":
		return result(s.Calibrated)
	case "get_connected":
		return result(s.Equipment)
	case "get_pixel_scale":
		// PHD2 knows the camera's pixel size only once the camera is
		// connected.
		if s.Scale == nil || !s.Equipment {
			return result(nil)
		}
		return result(*s.Scale)
	case "set_connected":
		// Connecting the gear writes the profile, and PHD2 reports
		// that before it answers.
		changed := Event("ConfigurationChange", nil)
		var on []bool
		if err := json.Unmarshal(params, &on); err != nil || len(on) != 1 {
			return failure("expected connected boolean param").first(changed)
		}
		if s.Refuse != "" {
			return failure(s.Refuse).first(changed)
		}
		s.Equipment = on[0]
		return result(0).first(changed)
	case "stop_capture":
		// PHD2 reports the end of guiding and of the loop as events
		// after it answers, and sends no AppState.
		var after []map[string]any
		if s.AppState != "Stopped" && s.AppState != "Selected" && s.AppState != "Looping" {
			after = append(after, Event("GuidingStopped", nil))
		}
		s.AppState = "Stopped"
		return result(0, append(after, Event("LoopingExposuresStopped", nil))...)
	case "set_exposure":
		var exposure []int
		if err := json.Unmarshal(params, &exposure); err != nil || len(exposure) != 1 {
			return failure("expected exposure param")
		}
		s.Exposure = exposure[0]
		return result(0)
	case "loop":
		// PHD2 cannot loop with no camera.
		if !s.Equipment {
			return failure("could not start looping")
		}
		if s.AppState != "Selected" {
			s.AppState = "Looping"
		}
		return result(0, Event("LoopingExposures", map[string]any{"Frame": 1}))
	case "find_star":
		if s.AppState != "Looping" && s.AppState != "Selected" {
			return failure("could not find star")
		}
		s.AppState = "Selected"
		at := map[string]any{"X": s.Star[0], "Y": s.Star[1]}
		return result([]float64{s.Star[0], s.Star[1]}).first(Event("LockPositionSet", at), Event("StarSelected", at))
	case "guide":
		var p struct {
			Settle      *settle `json:"settle"`
			Recalibrate bool    `json:"recalibrate"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.Settle == nil {
			return failure("expected settle object param")
		}
		if !p.Settle.valid() {
			return failure("invalid settle params")
		}
		if !s.Equipment {
			return failure("all equipment must be connected first")
		}
		if s.settling != "" {
			return failure("Cannot initiate guide while " + s.settling + " is in progress")
		}
		var after []map[string]any
		if p.Recalibrate || !s.Calibrated {
			mount := map[string]any{"Mount": "INDI Mount [Telescope Simulator]"}
			after = append(after, Event("StartCalibration", mount), Event("CalibrationComplete", mount))
			s.Calibrated = true
		}
		s.AppState = "Guiding"
		after = append(after, Event("StartGuiding", nil))
		return result(0, append(after, s.settleLocked("guide", *p.Settle)...)...)
	case "dither":
		var p struct {
			Amount *float64 `json:"amount"`
			RAOnly bool     `json:"raOnly"`
			Settle *settle  `json:"settle"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.Amount == nil {
			return failure("expected dither amount param")
		}
		if p.Settle == nil {
			return failure("expected settle object param")
		}
		if !p.Settle.valid() {
			return failure("invalid settle params")
		}
		if s.settling != "" {
			return failure("Cannot initiate dither while " + s.settling + " is in progress")
		}
		// PHD2's frame refuses a dither while it does not guide.
		if s.AppState != "Guiding" {
			return failure("Dither error")
		}
		dy := *p.Amount / 2
		if p.RAOnly {
			dy = 0
		}
		dithered := Event("GuidingDithered", map[string]any{"dx": *p.Amount / 2, "dy": dy})
		return result(0, append([]map[string]any{dithered}, s.settleLocked("dither", *p.Settle)...)...)
	}
	return failure("method not found")
}

// settle is the settle object of a guide or a dither.
type settle struct {
	Pixels  *float64 `json:"pixels"`
	Time    *float64 `json:"time"`
	Timeout *float64 `json:"timeout"`
}

// valid is parse_settle's check: pixels, time, and timeout are all
// present.
func (p settle) valid() bool { return p.Pixels != nil && p.Time != nil && p.Timeout != nil }

// settleLocked begins a settle, and answers its events: SettleBegin,
// one Settling, and SettleDone, which reports SettleFailure when it is
// set. With HoldSettle, the settle stays open without its SettleDone
// until Settle ends it. The caller holds mu.
func (s *Server) settleLocked(operation string, p settle) []map[string]any {
	events := []map[string]any{
		Event("SettleBegin", nil),
		Event("Settling", map[string]any{"Distance": 0.67, "Time": 0.0, "SettleTime": *p.Time, "StarLocked": true}),
	}
	if s.HoldSettle {
		s.settling = operation
		return events
	}
	return append(events, settleDone(s.SettleFailure))
}

// settleDone builds a SettleDone event. PHD2 adds Error only to a
// settle that failed.
func settleDone(failure string) map[string]any {
	done := map[string]any{"Status": 0, "TotalFrames": 7, "DroppedFrames": 0}
	if failure != "" {
		done["Status"], done["Error"] = 1, failure
	}
	return Event("SettleDone", done)
}

// Settle ends the settle that HoldSettle holds open, and sends its
// SettleDone to every open connection: settled when failure is empty,
// and failed with failure as the reason otherwise.
func (s *Server) Settle(failure string) {
	s.mu.Lock()
	s.settling = ""
	s.mu.Unlock()
	s.Broadcast(settleDone(failure))
}

// Params answers the params of the last request of a method, as the
// client sent them, or nil when no request of the method arrived.
func (s *Server) Params(method string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.params[method]
}

// Set changes the Server's state under its lock.
func (s *Server) Set(change func(*Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(s)
}

// Received answers the methods the Server has read, in order, joined
// by spaces.
func (s *Server) Received() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.methods, " ")
}

// Clear forgets the methods read so far.
func (s *Server) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods = nil
}

// Dials counts the connections the Server has accepted.
func (s *Server) Dials() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials
}

// Unread answers each line the Server could not read as a request.
func (s *Server) Unread() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.failed...)
}

// Pointer answers a pointer to a number, for Scale.
func Pointer(v float64) *float64 { return &v }
