package phd2test

import (
	"bufio"
	"encoding/json"
	"testing"
	"testing/synctest"
)

// exchange sends one line to a new connection of the Server, and
// answers the lines it reads back: the two catch-up events, then the
// answer.
func exchange(t *testing.T, s *Server, line string) []map[string]any {
	conn, err := s.DialContext(t.Context(), "tcp", "phd2:4400")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	lines := bufio.NewScanner(conn)
	var out []map[string]any
	read := func() {
		if !lines.Scan() {
			t.Fatal("the connection ended")
		}
		var m map[string]any
		if err := json.Unmarshal(lines.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	read()
	read()
	if _, err := conn.Write([]byte(line + "\r\n")); err != nil {
		t.Fatal(err)
	}
	// An event can come before the answer, as ConfigurationChange does
	// before the answer to set_connected.
	for {
		read()
		if _, answer := out[len(out)-1]["jsonrpc"]; answer {
			out = append(out[:2], out[len(out)-1])
			return out
		}
	}
}

func TestAnUnknownMethodIsNotFound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got := exchange(t, New(), `{"method":"loop","id":7}`)
		if got[0]["Event"] != "Version" || got[1]["Event"] != "AppState" || got[1]["State"] != "Stopped" {
			t.Errorf("catch-up = %v", got[:2])
		}
		failure, _ := got[2]["error"].(map[string]any)
		if got[2]["id"] != 7.0 || failure["message"] != "method not found" {
			t.Errorf("answer = %v", got[2])
		}
	})
}

func TestSetConnectedNeedsOneBoolean(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New()
		got := exchange(t, s, `{"method":"set_connected","params":["yes"],"id":1}`)
		failure, _ := got[2]["error"].(map[string]any)
		if failure["message"] != "expected connected boolean param" || s.Equipment {
			t.Errorf("answer = %v, equipment = %v", got[2], s.Equipment)
		}
	})
}

func TestALineThatIsNotJSONIsRecorded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New()
		conn, _ := s.DialContext(t.Context(), "tcp", "phd2:4400")
		lines := bufio.NewScanner(conn)
		lines.Scan()
		lines.Scan()
		_, _ = conn.Write([]byte("not json\r\n"))
		for lines.Scan() {
		}
		conn.Close()
		if got := s.Unread(); len(got) != 1 || got[0] != "not json" {
			t.Errorf("unread = %q", got)
		}
		if s.Dials() != 1 {
			t.Errorf("dials = %d", s.Dials())
		}
	})
}

func TestEachReadAnswersTheServersState(t *testing.T) {
	cases := []struct {
		method string
		set    func(*Server)
		want   any
	}{
		{"get_app_state", func(s *Server) { s.AppState = "Guiding" }, "Guiding"},
		{"get_calibrated", func(s *Server) { s.Calibrated = true }, true},
		{"get_connected", func(s *Server) { s.Equipment = true }, true},
		{"get_pixel_scale", func(s *Server) { s.Scale, s.Equipment = Pointer(2.5), true }, 2.5},
		{"get_pixel_scale", func(s *Server) { s.Scale = Pointer(2.5) }, nil},
		{"get_pixel_scale", func(s *Server) { s.Equipment = true }, nil},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := New()
				s.Set(c.set)
				got := exchange(t, s, `{"method":"`+c.method+`","id":1}`)
				if got[2]["result"] != c.want {
					t.Errorf("answer = %v, want %v", got[2], c.want)
				}
				if s.Received() != c.method {
					t.Errorf("received %q", s.Received())
				}
			})
		})
	}
}

func TestSetConnectedSetsTheEquipmentOrRefuses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New()
		got := exchange(t, s, `{"method":"set_connected","params":[true],"id":1}`)
		if got[2]["result"] != 0.0 || !s.Equipment {
			t.Errorf("answer = %v, equipment = %v", got[2], s.Equipment)
		}
		s.Set(func(s *Server) { s.Refuse = "equipment failed to connect: mount" })
		got = exchange(t, s, `{"method":"set_connected","params":[false],"id":2}`)
		failure, _ := got[2]["error"].(map[string]any)
		if failure["message"] != "equipment failed to connect: mount" || !s.Equipment {
			t.Errorf("answer = %v, equipment = %v", got[2], s.Equipment)
		}
	})
}

// stop_capture answers, then reports the end of the loop as PHD2 does.
func TestStopCaptureEndsTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New()
		s.Set(func(s *Server) { s.AppState = "Looping" })
		conn, _ := s.DialContext(t.Context(), "tcp", "phd2:4400")
		defer conn.Close()
		lines := bufio.NewScanner(conn)
		lines.Scan()
		lines.Scan()
		_, _ = conn.Write([]byte(`{"method":"stop_capture","id":3}` + "\r\n"))
		lines.Scan()
		lines.Scan()
		var e map[string]any
		_ = json.Unmarshal(lines.Bytes(), &e)
		if e["Event"] != "LoopingExposuresStopped" || s.AppState != "Stopped" {
			t.Errorf("event = %v, state = %q", e, s.AppState)
		}
	})
}

// A held method gets no answer, and a broadcast reaches each open
// connection until a cut ends them.
func TestAHeldMethodWaitsAndABroadcastReachesTheClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New()
		s.Set(func(s *Server) { s.Hold = "stop_capture" })
		conn, _ := s.DialContext(t.Context(), "tcp", "phd2:4400")
		lines := bufio.NewScanner(conn)
		lines.Scan()
		lines.Scan()
		_, _ = conn.Write([]byte(`{"method":"stop_capture","id":3}` + "\r\n"))
		synctest.Wait()
		go s.Broadcast(GuideStep(7, 0.5, -0.5))
		lines.Scan()
		var e map[string]any
		_ = json.Unmarshal(lines.Bytes(), &e)
		if e["Event"] != "GuideStep" || e["Frame"] != 7.0 || e["RADistanceRaw"] != 0.5 {
			t.Errorf("event = %v", e)
		}
		s.Cut()
		if lines.Scan() {
			t.Errorf("read %q after the cut", lines.Text())
		}
		s.Clear()
		if s.Received() != "" {
			t.Errorf("received %q after clear", s.Received())
		}
	})
}

// A PHD2 that exited refuses a new connection, the way a socket with no
// listener refuses one, so a dial that races the end of a pod never
// reaches the PHD2 that is gone.
func TestACutServerRefusesADial(t *testing.T) {
	s := New()
	s.Cut()
	if conn, err := s.DialContext(t.Context(), "tcp", "phd2:4400"); err == nil {
		conn.Close()
		t.Fatal("the dial reached a PHD2 that exited")
	}
}
