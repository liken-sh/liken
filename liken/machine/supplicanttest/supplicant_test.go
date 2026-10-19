package supplicanttest

import (
	"net"
	"testing"
	"time"
)

// ask sends one command from a bound client socket and answers the
// reply, the way every client of the control socket must.
func ask(t *testing.T, s *Supplicant, local, command string) (*net.UnixConn, string) {
	t.Helper()
	conn, err := net.DialUnix("unixgram",
		&net.UnixAddr{Name: local, Net: "unixgram"},
		&net.UnixAddr{Name: s.Socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(command)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return conn, string(buf[:n])
}

func TestTheStandInAnswersWhatItWasGiven(t *testing.T) {
	s := Start(t, t.TempDir(), "wlan0")
	s.Answer("STATUS", "wpa_state=COMPLETED\n")
	_, got := ask(t, s, "@supplicanttest-status", "STATUS")
	if got != "wpa_state=COMPLETED\n" {
		t.Errorf("got %q", got)
	}
	_, got = ask(t, s, "@supplicanttest-unknown", "PING")
	if got != "UNKNOWN COMMAND\n" {
		t.Errorf("got %q for a command with no answer", got)
	}
	if asked := s.Asked(); len(asked) != 2 || asked[0] != "STATUS" || asked[1] != "PING" {
		t.Errorf("asked %q", asked)
	}
}

func TestTheStandInPushesEventsToAnAttachedClient(t *testing.T) {
	s := Start(t, t.TempDir(), "wlan0")
	conn, got := ask(t, s, "@supplicanttest-attach", "ATTACH")
	if got != "OK\n" || !s.Attached() {
		t.Fatalf("ATTACH answered %q", got)
	}
	s.Push(t, "CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed")
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if want := "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed"; string(buf[:n]) != want {
		t.Errorf("got %q, want %q", buf[:n], want)
	}
}
