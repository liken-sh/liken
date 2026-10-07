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
	// Hold names a method that the Server reads and never answers.
	Hold string

	methods []string
	conns   []net.Conn
	dials   int
	// exited records a Cut. A PHD2 that exited has no listener, so
	// each later dial is refused.
	exited bool
	// Failed receives what the Server could not read. A test checks it.
	failed []string
}

// New answers a PHD2 that runs, is idle, and has no equipment
// connected, as PHD2 is after it starts.
func New() *Server { return &Server{AppState: "Stopped"} }

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
		s.mu.Lock()
		before := s.AppState
		s.mu.Unlock()
		if request.Method == "set_connected" {
			// Connecting the gear writes the profile, and PHD2 reports
			// that before it answers.
			send(conn, Event("ConfigurationChange", nil))
		}
		result, failure := s.answer(request.Method, request.Params)
		if failure != "" {
			answer["error"] = map[string]any{"code": 1, "message": failure}
		} else {
			answer["result"] = result
		}
		if request.ID != nil {
			send(conn, answer)
		}
		if request.Method == "stop_capture" {
			// PHD2 reports the end of guiding and of the loop as events
			// after it answers, and sends no AppState.
			if before != "Stopped" && before != "Selected" && before != "Looping" {
				send(conn, Event("GuidingStopped", nil))
			}
			send(conn, Event("LoopingExposuresStopped", nil))
		}
	}
}

// answer runs one method the way event_server.cpp does.
func (s *Server) answer(method string, params json.RawMessage) (any, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case "get_app_state":
		return s.AppState, ""
	case "get_calibrated":
		return s.Calibrated, ""
	case "get_connected":
		return s.Equipment, ""
	case "get_pixel_scale":
		// PHD2 knows the camera's pixel size only once the camera is
		// connected.
		if s.Scale == nil || !s.Equipment {
			return nil, ""
		}
		return *s.Scale, ""
	case "set_connected":
		var on []bool
		if err := json.Unmarshal(params, &on); err != nil || len(on) != 1 {
			return nil, "expected connected boolean param"
		}
		if s.Refuse != "" {
			return nil, s.Refuse
		}
		s.Equipment = on[0]
		return 0, ""
	case "stop_capture":
		s.AppState = "Stopped"
		return 0, ""
	}
	return nil, "method not found"
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
