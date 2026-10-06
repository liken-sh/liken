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
	read()
	return out
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
