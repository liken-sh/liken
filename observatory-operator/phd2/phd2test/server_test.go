package phd2test

import (
	"bufio"
	"encoding/json"
	"slices"
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
		got := exchange(t, New(), `{"method":"get_star_image","id":7}`)
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

// Each guiding method refuses what event_server.cpp and phdcontrol.cpp
// refuse, with their messages.
func TestTheGuidingMethodsRefuseAsPHD2Does(t *testing.T) {
	const ok = `{"pixels":1.5,"time":5,"timeout":60}`
	cases := []struct {
		name, line string
		set        func(*Server)
		want       string
	}{
		{"an exposure that is not a whole number", `{"method":"set_exposure","params":[1.5],"id":1}`, nil, "expected exposure param"},
		{"a loop with no camera", `{"method":"loop","id":1}`, nil, "could not start looping"},
		{"a star while not looping", `{"method":"find_star","id":1}`, nil, "could not find star"},
		{"no star in the frame", `{"method":"find_star","id":1}`,
			func(s *Server) {
				s.AppState, s.Refusals = "Looping", map[string]string{"find_star": "could not find star"}
			}, "could not find star"},
		{"a guide with no settle", `{"method":"guide","params":{"recalibrate":false},"id":1}`, nil, "expected settle object param"},
		{"a settle with no timeout", `{"method":"guide","params":{"settle":{"pixels":1.5,"time":5}},"id":1}`, nil, "invalid settle params"},
		{"a guide with no equipment", `{"method":"guide","params":{"settle":` + ok + `},"id":1}`, nil, "all equipment must be connected first"},
		{"a guide during a settle", `{"method":"guide","params":{"settle":` + ok + `},"id":1}`,
			func(s *Server) { s.Equipment, s.settling = true, "dither" }, "Cannot initiate guide while dither is in progress"},
		{"a dither with no amount", `{"method":"dither","params":{"settle":` + ok + `},"id":1}`, nil, "expected dither amount param"},
		{"a dither with no settle", `{"method":"dither","params":{"amount":5},"id":1}`, nil, "expected settle object param"},
		{"a dither with a bad settle", `{"method":"dither","params":{"amount":5,"settle":{}},"id":1}`, nil, "invalid settle params"},
		{"a dither during a settle", `{"method":"dither","params":{"amount":5,"settle":` + ok + `},"id":1}`,
			func(s *Server) { s.AppState, s.settling = "Guiding", "guide" }, "Cannot initiate dither while guide is in progress"},
		{"a dither while not guiding", `{"method":"dither","params":{"amount":5,"settle":` + ok + `},"id":1}`, nil, "Dither error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := New()
				if c.set != nil {
					s.Set(c.set)
				}
				got := exchange(t, s, c.line)
				failure, _ := got[2]["error"].(map[string]any)
				if failure["message"] != c.want {
					t.Errorf("answer = %v, want %q", got[2], c.want)
				}
			})
		})
	}
}

// readAll sends one line to a new connection and answers every message
// that follows the catch-up events until the Server goes quiet.
func readAll(t *testing.T, s *Server, line string) []map[string]any {
	conn, _ := s.DialContext(t.Context(), "tcp", "phd2:4400")
	lines := bufio.NewScanner(conn)
	lines.Scan()
	lines.Scan()
	_, _ = conn.Write([]byte(line + "\r\n"))
	var out []map[string]any
	read := make(chan struct{})
	go func() {
		defer close(read)
		for lines.Scan() {
			var m map[string]any
			_ = json.Unmarshal(lines.Bytes(), &m)
			out = append(out, m)
		}
	}()
	synctest.Wait()
	conn.Close()
	<-read
	return out
}

// names answers the event of each message, or "answer" for an answer.
func names(messages []map[string]any) []string {
	var out []string
	for _, m := range messages {
		name, _ := m["Event"].(string)
		if name == "" {
			name = "answer"
		}
		out = append(out, name)
	}
	return out
}

// The events around each answer follow the recorded session in
// phd2/testdata/: find_star's selection comes before its answer, and a
// guide's calibration and settle come after it.
func TestEachMethodSendsPHD2sEvents(t *testing.T) {
	const settle = `{"pixels":1.5,"time":5,"timeout":60}`
	cases := []struct {
		name, line string
		set        func(*Server)
		want       []string
	}{
		{"loop", `{"method":"loop","id":1}`, func(s *Server) { s.Equipment = true },
			[]string{"answer", "LoopingExposures"}},
		{"find a star", `{"method":"find_star","id":1}`, func(s *Server) { s.AppState = "Looping" },
			[]string{"LockPositionSet", "StarSelected", "answer"}},
		{"guide with no calibration", `{"method":"guide","params":{"settle":` + settle + `},"id":1}`, func(s *Server) { s.Equipment = true },
			[]string{"answer", "StartCalibration", "CalibrationComplete", "StartGuiding", "SettleBegin", "Settling", "SettleDone"}},
		{"guide with a calibration", `{"method":"guide","params":{"settle":` + settle + `},"id":1}`, func(s *Server) { s.Equipment, s.Calibrated = true, true },
			[]string{"answer", "StartGuiding", "SettleBegin", "Settling", "SettleDone"}},
		{"recalibrate", `{"method":"guide","params":{"settle":` + settle + `,"recalibrate":true},"id":1}`, func(s *Server) { s.Equipment, s.Calibrated = true, true },
			[]string{"answer", "StartCalibration", "CalibrationComplete", "StartGuiding", "SettleBegin", "Settling", "SettleDone"}},
		{"dither", `{"method":"dither","params":{"amount":5,"raOnly":true,"settle":` + settle + `},"id":1}`, func(s *Server) { s.AppState = "Guiding" },
			[]string{"answer", "GuidingDithered", "SettleBegin", "Settling", "SettleDone"}},
		{"hold the settle", `{"method":"dither","params":{"amount":5,"settle":` + settle + `},"id":1}`, func(s *Server) { s.AppState, s.HoldSettle = "Guiding", true },
			[]string{"answer", "GuidingDithered", "SettleBegin", "Settling"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := New()
				s.Set(c.set)
				if got := names(readAll(t, s, c.line)); !slices.Equal(got, c.want) {
					t.Errorf("messages = %v, want %v", got, c.want)
				}
			})
		})
	}
}

func TestFindStarAnswersTheStar(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New()
		s.Set(func(s *Server) { s.AppState, s.Star = "Looping", [2]float64{10, 20} })
		got := exchange(t, s, `{"method":"find_star","id":1}`)
		if position, _ := got[2]["result"].([]any); !slices.Equal(position, []any{10.0, 20.0}) || s.AppState != "Selected" {
			t.Errorf("answer = %v, state = %q", got[2], s.AppState)
		}
	})
}

func TestSetExposureRecordsTheExposure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New()
		exchange(t, s, `{"method":"set_exposure","params":[2000],"id":1}`)
		if s.Exposure != 2000 || string(s.Params("set_exposure")) != "[2000]" {
			t.Errorf("exposure = %d, params = %s", s.Exposure, s.Params("set_exposure"))
		}
	})
}

// A settle that fails reports Status 1 and PHD2's reason, and a held
// settle ends with Settle.
func TestASettleEndsSettledOrFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New()
		s.Set(func(s *Server) { s.AppState, s.SettleFailure = "Guiding", "timed-out waiting for guider to settle" })
		got := readAll(t, s, `{"method":"dither","params":{"amount":5,"settle":{"pixels":1.5,"time":5,"timeout":60}},"id":1}`)
		if done := got[len(got)-1]; done["Status"] != 1.0 || done["Error"] != "timed-out waiting for guider to settle" {
			t.Errorf("SettleDone = %v", done)
		}

		s = New()
		s.Set(func(s *Server) { s.AppState, s.HoldSettle = "Guiding", true })
		conn, _ := s.DialContext(t.Context(), "tcp", "phd2:4400")
		defer conn.Close()
		lines := bufio.NewScanner(conn)
		lines.Scan()
		lines.Scan()
		_, _ = conn.Write([]byte(`{"method":"dither","params":{"amount":5,"settle":{"pixels":1.5,"time":5,"timeout":60}},"id":1}` + "\r\n"))
		// The answer, GuidingDithered, SettleBegin, and Settling.
		for range 4 {
			lines.Scan()
		}
		go s.Settle("")
		lines.Scan()
		var done map[string]any
		_ = json.Unmarshal(lines.Bytes(), &done)
		if _, failed := done["Error"]; done["Event"] != "SettleDone" || done["Status"] != 0.0 || failed {
			t.Errorf("SettleDone = %v", done)
		}
	})
}
