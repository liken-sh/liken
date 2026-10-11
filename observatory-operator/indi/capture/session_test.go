package capture

import (
	"bufio"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A fakeServer reads each line a capture sends and writes the chunks
// that its answer lists, waiting gap between two chunks, as a server
// that sends a reply in parts does. A line with no answer gets no
// reply.
type fakeServer struct {
	answers map[string][]string
	gap     time.Duration

	mutex    sync.Mutex
	received []string
}

func (s *fakeServer) serve(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		s.mutex.Lock()
		s.received = append(s.received, line)
		s.mutex.Unlock()
		for i, chunk := range s.answers[line] {
			if i > 0 {
				time.Sleep(s.gap)
			}
			if _, err := conn.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}
}

func (s *fakeServer) lines() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return slices.Clone(s.received)
}

// record runs a script against the fake in a synctest bubble, so the
// quiet that ends each phase passes on the fake clock.
func record(t *testing.T, server *fakeServer, script func(device string) Script) (string, []Reply) {
	t.Helper()
	var device string
	var replies []Reply
	synctest.Test(t, func(t *testing.T) {
		client, end := net.Pipe()
		go server.serve(end)
		defer client.Close()
		var err error
		device, replies, err = Run(client, script)
		if err != nil {
			t.Fatal(err)
		}
	})
	return device, replies
}

const definition = `<defSwitchVector device="Bare Dust Cap" name="CAP_PARK" timestamp="2026-10-11T12:00:00"><defSwitch name="PARK">Off</defSwitch></defSwitchVector>`

func connect(device string) string { return switchMessage(device, "CONNECTION", "CONNECT") }

func TestRunSendsTheSetupAndRecordsEachPhase(t *testing.T) {
	setup := `<enableBLOB device="Bare Dust Cap">Also</enableBLOB>`
	server := &fakeServer{answers: map[string][]string{
		Baseline:                 {`<defTextVector device="Bare Dust Cap" name="DRIVER_INFO"/>`},
		connect("Bare Dust Cap"): {definition},
	}}
	script := func(device string) Script {
		return Script{Setup: []string{setup}, Phases: []Phase{{"baseline", Baseline}, {"connect", connect(device)}}}
	}

	device, replies := record(t, server, script)

	if device != "Bare Dust Cap" {
		t.Errorf("device = %q, want Bare Dust Cap", device)
	}
	want := []Reply{
		{"baseline", []byte(`<defTextVector device="Bare Dust Cap" name="DRIVER_INFO"/>`)},
		{"connect", []byte(definition)},
	}
	if !slices.EqualFunc(replies, want, func(a, b Reply) bool { return a.Phase == b.Phase && string(a.Data) == string(b.Data) }) {
		t.Errorf("replies = %q, want %q", replies, want)
	}
	if got := server.lines(); !slices.Equal(got, []string{Baseline, setup, connect("Bare Dust Cap")}) {
		t.Errorf("the server received %q, want the baseline, the setup, then the connect", got)
	}
}

// A pause inside an element does not end a phase, so each reply parses
// on its own.
func TestRunWaitsForTheEndOfAnElement(t *testing.T) {
	half := len(definition) / 2
	server := &fakeServer{gap: 2 * Quiet, answers: map[string][]string{
		Baseline: {definition[:half], definition[half:]},
	}}

	_, replies := record(t, server, func(string) Script { return Script{Phases: []Phase{{"baseline", Baseline}}} })

	if string(replies[0].Data) != definition {
		t.Errorf("baseline = %q, want the whole element", replies[0].Data)
	}
}

func TestRunRefusesABaselineWithNoDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, end := net.Pipe()
		go (&fakeServer{answers: map[string][]string{Baseline: {`<message message="no driver"/>`}}}).serve(end)
		defer client.Close()
		_, _, err := Run(client, func(string) Script { return Script{} })
		if err == nil || !strings.Contains(err.Error(), "names no device") {
			t.Errorf("err = %v, want the baseline named no device", err)
		}
	})
}

func TestRunReportsAClosedConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, end := net.Pipe()
		end.Close()
		if _, _, err := Run(client, func(string) Script { return Script{} }); err == nil {
			t.Error("Run succeeded on a closed connection")
		}
	})
}

func TestSimulatorScripts(t *testing.T) {
	cases := []struct {
		simulator string
		phases    []string
		setup     int
	}{
		{"dustcover", []string{"baseline", "connect", "disconnect"}, 0},
		{"focus", []string{"baseline", "connect", "move", "refuse", "disconnect"}, 0},
		{"dome", []string{"baseline", "connect", "move", "disconnect"}, 0},
		{"ccd", []string{"baseline", "connect", "frame", "slot", "expose", "disconnect"}, 1},
	}
	for _, c := range cases {
		t.Run(c.simulator, func(t *testing.T) {
			script := SimulatorScript(c.simulator, "Simulator")
			var names []string
			for _, p := range script.Phases {
				names = append(names, p.Name)
			}
			if !slices.Equal(names, c.phases) || len(script.Setup) != c.setup {
				t.Errorf("phases = %v with %d setup messages, want %v with %d", names, len(script.Setup), c.phases, c.setup)
			}
		})
	}
}

func TestBareScripts(t *testing.T) {
	cases := []struct {
		bare   string
		phases []string
	}{
		{DefaultDevice, []string{"baseline", "connect", "tcp"}},
		{"ccd", []string{"baseline", "connect"}},
		{"telescope-az-alt", []string{"baseline", "connect", "pier-side"}},
	}
	for _, c := range cases {
		t.Run(c.bare, func(t *testing.T) {
			i := slices.IndexFunc(Bares, func(b Bare) bool { return b.Name == c.bare })
			var names []string
			for _, p := range Bares[i].Script("Bare").Phases {
				names = append(names, p.Name)
			}
			if !slices.Equal(names, c.phases) {
				t.Errorf("phases = %v, want %v", names, c.phases)
			}
		})
	}
}
