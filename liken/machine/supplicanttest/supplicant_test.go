package supplicanttest

import (
	"net"
	"testing"
	"time"
)

// sendCommand sends one command from a client socket bound to the path local,
// the way a client of the control socket must, and returns the
// connection and the reply.
func sendCommand(t *testing.T, s *Supplicant, local, command string) (*net.UnixConn, string) {
	t.Helper()
	conn, err := net.DialUnix("unixgram",
		&net.UnixAddr{Name: local, Net: "unixgram"},
		&net.UnixAddr{Name: s.Socket, Net: "unixgram"})
	if err != nil {
		t.Fatalf("dialing the control socket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("setting the connection deadline: %v", err)
	}
	if _, err := conn.Write([]byte(command)); err != nil {
		t.Fatalf("writing the command: %v", err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	return conn, string(buf[:n])
}

func TestTheFakeRepliesWithTheConfiguredReply(t *testing.T) {
	s := Start(t, t.TempDir(), "wlan0")
	s.Answer("STATUS", "wpa_state=COMPLETED\n")
	_, got := sendCommand(t, s, "@supplicanttest-status", "STATUS")
	if got != "wpa_state=COMPLETED\n" {
		t.Errorf("STATUS replied %q, want the configured reply", got)
	}
	_, got = sendCommand(t, s, "@supplicanttest-unknown", "PING")
	if got != "UNKNOWN COMMAND\n" {
		t.Errorf("PING with no configured reply replied %q, want \"UNKNOWN COMMAND\\n\"", got)
	}
	if asked := s.Asked(); len(asked) != 2 || asked[0] != "STATUS" || asked[1] != "PING" {
		t.Errorf("the commands received are %q, want STATUS then PING", asked)
	}
}

func TestTheFakePushesEventsToAnAttachedClient(t *testing.T) {
	s := Start(t, t.TempDir(), "wlan0")
	conn, got := sendCommand(t, s, "@supplicanttest-attach", "ATTACH")
	if got != "OK\n" || !s.Attached() {
		t.Fatalf("ATTACH replied %q and Attached is %v, want \"OK\\n\" and true", got, s.Attached())
	}
	s.Push(t, "CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed")
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("reading the pushed event: %v", err)
	}
	if want := "<3>CTRL-EVENT-CONNECTED - Connection to 04:4a:2c:11:22:33 completed"; string(buf[:n]) != want {
		t.Errorf("the pushed event is %q, want %q", buf[:n], want)
	}
}
