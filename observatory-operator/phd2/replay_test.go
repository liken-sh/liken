package phd2

// The replay serves the transcript in testdata/, which a real PHD2
// sent, to the client, so the client is held to what PHD2 sends and not
// only to the fake in phd2test. testdata/README.md says how the session
// was recorded.
//
// The transcript is one connection, split into phases. The replay
// sends the lines of a phase that come before its first request, then
// answers each request of the client with the lines that followed the
// recorded request of the same method, with the client's id in the
// answer. The client sends reads that the recording client did not,
// such as the reads after ConfigurationChange, and the replay answers
// those with the last answer PHD2 gave to that method. A phase whose
// requests the client never sends, such as loop or guide, is streamed:
// its events go out, and its answers stay back, apart from the answer
// to a read that the client sends too.

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

type recorded struct {
	sent bool
	line string
}

func phase(t *testing.T, name string) []recorded {
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var out []recorded
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		mark, body, _ := strings.Cut(line, " ")
		out = append(out, recorded{sent: mark == ">", line: body})
	}
	return out
}

type replay struct {
	t    *testing.T
	conn net.Conn
	// mu guards answers and the phase being served.
	mu      sync.Mutex
	current []recorded
	used    map[int]bool
	// streamed is true while the current phase's events went out
	// already, so an answer from it goes out alone.
	streamed bool
	// last holds PHD2's last answer to each method, by method.
	last map[string]json.RawMessage
}

func methodOf(line string) string {
	var r struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal([]byte(line), &r)
	return r.Method
}

func isAnswer(line string) bool { return strings.HasPrefix(line, `{"jsonrpc"`) }

func (r *replay) send(line string) {
	if _, err := r.conn.Write([]byte(line + "\r\n")); err != nil {
		r.t.Error(err)
	}
}

// play serves a phase: its lines before the first request at once, and
// the rest as the client asks.
func (r *replay) play(name string) {
	lines := phase(r.t, name)
	r.mu.Lock()
	r.current, r.used, r.streamed = lines, map[int]bool{}, false
	r.mu.Unlock()
	for _, l := range lines {
		if l.sent {
			return
		}
		r.send(l.line)
	}
}

// stream sends a phase's events and holds its answers back. A read
// that the client sends meanwhile gets the answer that PHD2 gave in
// the phase.
func (r *replay) stream(name string) {
	lines := phase(r.t, name)
	r.mu.Lock()
	r.current, r.used, r.streamed = lines, map[int]bool{}, true
	r.mu.Unlock()
	for _, l := range lines {
		if !l.sent && !isAnswer(l.line) {
			r.send(l.line)
		}
	}
}

// serve reads the client's requests and answers each one.
func (r *replay) serve() {
	lines := bufio.NewScanner(r.conn)
	for lines.Scan() {
		var request struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		if err := json.Unmarshal(lines.Bytes(), &request); err != nil {
			r.t.Errorf("a request that is not JSON: %q", lines.Text())
			return
		}
		for _, line := range r.answers(request.Method) {
			if isAnswer(line) {
				var answer map[string]json.RawMessage
				_ = json.Unmarshal([]byte(line), &answer)
				answer["id"], _ = json.Marshal(request.ID)
				body, _ := json.Marshal(answer)
				line = string(body)
			}
			r.send(line)
		}
	}
}

// answers answers the lines that follow the recorded request of a
// method in the current phase, or the last answer to the method.
func (r *replay) answers(method string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, l := range r.current {
		if !l.sent || r.used[i] || methodOf(l.line) != method {
			continue
		}
		r.used[i] = true
		var out []string
		for _, next := range r.current[i+1:] {
			if next.sent {
				break
			}
			if r.streamed && !isAnswer(next.line) {
				continue
			}
			out = append(out, next.line)
			if isAnswer(next.line) {
				var answer map[string]json.RawMessage
				_ = json.Unmarshal([]byte(next.line), &answer)
				r.last[method] = answer["result"]
			}
		}
		return out
	}
	result, ok := r.last[method]
	if !ok {
		r.t.Errorf("the transcript has no answer to %s", method)
		return nil
	}
	return []string{`{"jsonrpc":"2.0","result":` + string(result) + `,"id":0}`}
}

// startReplay runs a client against the replay, and records each
// baseline answer of the first phases as the last answer.
func startReplay(t *testing.T) (*Client, *replay) {
	r := &replay{t: t, last: map[string]json.RawMessage{}}
	dial := dialerFunc(func() net.Conn {
		server, client := net.Pipe()
		r.conn = server
		go func() {
			r.play("01-connect.txt")
			r.play("02-baseline.txt")
			r.serve()
		}()
		return client
	})
	return running(t, dial), r
}

type dialerFunc func() net.Conn

func (f dialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(), nil
}

func TestTheClientFollowsARealPHD2(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, r := startReplay(t)
		s := settled(c)
		if !s.Open || s.Version != "2.6.14" || s.AppState != "Stopped" || s.Equipment == nil || *s.Equipment || s.PixelScale != nil {
			t.Fatalf("baseline = %+v", s)
		}

		r.play("03-set-connected.txt")
		if err := c.SetConnected(t.Context(), true); err != nil {
			t.Fatal(err)
		}
		if s := settled(c); !equipmentOn(s) || s.PixelScale == nil || *s.PixelScale != 1.23759 {
			t.Errorf("after set_connected = %+v", s)
		}

		r.stream("04-loop.txt")
		if s := settled(c); s.AppState != "Looping" {
			t.Errorf("looping = %q", s.AppState)
		}
		r.stream("05-find-star.txt")
		if s := settled(c); s.AppState != "Selected" {
			t.Errorf("with a star = %q, and PHD2 answered Selected", s.AppState)
		}
		r.stream("06-calibrate.txt")
		if s := settled(c); s.AppState != "Guiding" || s.Calibrated == nil || !*s.Calibrated {
			t.Errorf("after calibration = %+v", s)
		}
		r.stream("07-guide.txt")
		s = settled(c)
		if s.AppState != "Guiding" || s.Step == nil || s.RMS.Steps != 33 || s.Step.HFD == 0 || s.Step.SNR == 0 {
			t.Errorf("guiding = %+v, step %+v", s, s.Step)
		}
		t.Logf("RMS of the recorded guiding: %+v pixels, %.2f arcsec total", s.RMS, s.RMS.Total*(*s.PixelScale))

		r.play("08-stop.txt")
		if err := c.StopCapture(t.Context()); err != nil {
			t.Fatal(err)
		}
		if s := settled(c); s.AppState != "Stopped" {
			t.Errorf("after stop_capture = %q", s.AppState)
		}
	})
}

func equipmentOn(s State) bool { return s.Equipment != nil && *s.Equipment }
