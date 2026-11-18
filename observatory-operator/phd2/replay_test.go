package phd2

// The replay plays a transcript of a real PHD2 session from testdata/
// to the client, so the client is tested against what PHD2 actually
// sends. testdata/README.md describes the recording.
//
// For each phase, the replay first sends the lines before the first
// recorded request. Then, for each request from the client, it sends the
// lines that followed the recorded request with the same method, with
// the client's request id in the response. The client also sends reads
// that the recording client did not, such as the reads after
// ConfigurationChange, and the replay responds to those with the last
// result PHD2 gave for that method. Some phases, such as loop and guide,
// contain requests that the client never sends. stream sends the events
// of such a phase at once, and holds back its responses for the reads
// that the client does send.

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type recorded struct {
	sent bool
	line string
}

func phase(t *testing.T, name string) []recorded {
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading the recorded phase %s: %v", name, err)
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
	// mu guards the fields below, which hold the phase being served.
	mu      sync.Mutex
	current []recorded
	used    map[int]bool
	// streamed is true when stream has already sent the current phase's
	// events, so responses leave them out.
	streamed bool
	// last holds the last result PHD2 gave for each method.
	last map[string]json.RawMessage
}

func methodOf(line string) string {
	var r struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal([]byte(line), &r)
	return r.Method
}

func paramsOf(line string) json.RawMessage {
	var r struct {
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal([]byte(line), &r)
	return r.Params
}

// sameJSON ignores key order.
func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	return reflect.DeepEqual(x, y)
}

func isResponse(line string) bool { return strings.HasPrefix(line, `{"jsonrpc"`) }

func (r *replay) send(line string) {
	if _, err := r.conn.Write([]byte(line + "\r\n")); err != nil {
		r.t.Errorf("sending a recorded line to the client: %v", err)
	}
}

// play starts a phase. It sends the lines before the first recorded
// request, and serve sends the rest as the client requests them.
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

// stream starts a phase by sending all of its events. serve sends its
// responses as the client requests them.
func (r *replay) stream(name string) {
	lines := phase(r.t, name)
	r.mu.Lock()
	r.current, r.used, r.streamed = lines, map[int]bool{}, true
	r.mu.Unlock()
	for _, l := range lines {
		if !l.sent && !isResponse(l.line) {
			r.send(l.line)
		}
	}
}

func (r *replay) serve() {
	lines := bufio.NewScanner(r.conn)
	for lines.Scan() {
		var request struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     int             `json:"id"`
		}
		if err := json.Unmarshal(lines.Bytes(), &request); err != nil {
			r.t.Errorf("the client sent a request that is not JSON: %q", lines.Text())
			return
		}
		for _, line := range r.responses(request.Method, request.Params) {
			if isResponse(line) {
				var response map[string]json.RawMessage
				_ = json.Unmarshal([]byte(line), &response)
				response["id"], _ = json.Marshal(request.ID)
				body, _ := json.Marshal(response)
				line = string(body)
			}
			r.send(line)
		}
	}
}

// responses returns the lines to send for a request: the lines that
// follow the next unused recorded request with the same method in the
// current phase, or else the last result for that method. It fails the
// test when the params differ from the recorded ones.
func (r *replay) responses(method string, params json.RawMessage) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, l := range r.current {
		if !l.sent || r.used[i] || methodOf(l.line) != method {
			continue
		}
		r.used[i] = true
		if want := paramsOf(l.line); !sameJSON(params, want) {
			r.t.Errorf("%s params are %s, want %s as in the recording", method, params, want)
		}
		var out []string
		for _, next := range r.current[i+1:] {
			if next.sent {
				break
			}
			if r.streamed && !isResponse(next.line) {
				continue
			}
			out = append(out, next.line)
			if isResponse(next.line) {
				var response map[string]json.RawMessage
				_ = json.Unmarshal([]byte(next.line), &response)
				r.last[method] = response["result"]
			}
		}
		return out
	}
	result, ok := r.last[method]
	if !ok {
		r.t.Errorf("the recording has no response to %s", method)
		return nil
	}
	return []string{`{"jsonrpc":"2.0","result":` + string(result) + `,"id":0}`}
}

// startReplay connects a client to the replay, which plays the connect
// and baseline phases and then serves requests.
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
			t.Fatalf("the state before any request is %+v, want an open client at version 2.6.14, Stopped, with equipment off and no pixel scale", s)
		}

		r.play("03-set-connected.txt")
		if err := c.SetConnected(t.Context(), true); err != nil {
			t.Fatalf("calling set_connected: %v", err)
		}
		if s := settled(c); !equipmentOn(s) || s.PixelScale == nil || *s.PixelScale != 1.23759 {
			t.Errorf("the state after set_connected is %+v, want equipment on and a pixel scale of 1.23759", s)
		}

		r.play("04-loop.txt")
		if err := c.Loop(t.Context()); err != nil {
			t.Fatalf("calling loop: %v", err)
		}
		if s := settled(c); s.AppState != "Looping" {
			t.Errorf("after loop, AppState is %q, want Looping", s.AppState)
		}
		r.play("05-find-star.txt")
		x, y, err := c.FindStar(t.Context())
		if err != nil || x != 1045.09 || y != 44.96 {
			t.Errorf("find_star returned %v, %v, %v, want 1045.09, 44.96, and no error", x, y, err)
		}
		if s := settled(c); s.AppState != "Selected" {
			t.Errorf("after find_star, AppState is %q, want Selected", s.AppState)
		}
		r.play("06-calibrate.txt")
		if err := c.Guide(t.Context(), Settle{Pixels: 1.5, Time: 5 * time.Second, Timeout: time.Minute}, true); err != nil {
			t.Fatalf("calling guide: %v", err)
		}
		s = settled(c)
		if s.AppState != "Guiding" || s.Calibrated == nil || !*s.Calibrated {
			t.Errorf("after calibration, the state is %+v, want Guiding and calibrated", s)
		}
		if s.Settling == nil || !s.Settling.Began || s.Settling.Done {
			t.Errorf("after SettleBegin, the settling state is %+v, want began and not done", s.Settling)
		}
		r.stream("07-guide.txt")
		if err := c.WaitSettled(t.Context()); err != nil {
			t.Errorf("waiting for the settle returned %v, want no error", err)
		}
		s = settled(c)
		if s.AppState != "Guiding" || s.Step == nil || s.RMS.Steps != 33 || s.Step.HFD == 0 || s.Step.SNR == 0 {
			t.Errorf("while guiding, the state is %+v and the step is %+v, want 33 RMS steps and a step with HFD and SNR", s, s.Step)
		}
		want := Settling{Began: true, Distance: 0.67, Time: 3800 * time.Millisecond, SettleTime: 5 * time.Second, StarLocked: true, Done: true, TotalFrames: 7}
		if *s.Settling != want {
			t.Errorf("the settling state is %+v, want %+v", *s.Settling, want)
		}
		t.Logf("RMS of the recorded guiding: %+v pixels, %.2f arcsec total", s.RMS, s.RMS.Total*(*s.PixelScale))

		r.play("08-stop.txt")
		if err := c.StopCapture(t.Context()); err != nil {
			t.Fatalf("calling stop_capture: %v", err)
		}
		if s := settled(c); s.AppState != "Stopped" {
			t.Errorf("after stop_capture, AppState is %q, want Stopped", s.AppState)
		}
	})
}

func equipmentOn(s State) bool { return s.Equipment != nil && *s.Equipment }
