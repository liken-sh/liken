// Package supplicanttest serves a stand-in for wpa_supplicant's control
// socket, so the clients in init and machine-operator run the real
// datagram transport with no radio and no supplicant.
package supplicanttest

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Supplicant answers commands on a control socket the way
// wpa_supplicant does: ATTACH with OK, a command it holds an answer
// for with that answer, and any other command with UNKNOWN COMMAND.
// Push sends an event to every client that attached.
type Supplicant struct {
	// Socket is the control socket's path.
	Socket string

	conn *net.UnixConn

	mu       sync.Mutex
	answers  map[string]string
	attached []*net.UnixAddr
	asked    []string
}

// Start serves a stand-in for the supplicant of ifname under dir, at
// the path machine.SupplicantSocket names. The test's cleanup closes
// it.
func Start(t *testing.T, dir, ifname string) *Supplicant {
	t.Helper()
	ctrl := filepath.Join(dir, ifname, "ctrl")
	if err := os.MkdirAll(ctrl, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(ctrl, ifname)
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Supplicant{Socket: socket, conn: conn, answers: map[string]string{}}
	t.Cleanup(func() { conn.Close() })
	go s.serve()
	return s
}

// Answer sets the reply to one command.
func (s *Supplicant) Answer(command, reply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers[command] = reply
}

// Asked answers every command the stand-in received other than
// ATTACH, in order.
func (s *Supplicant) Asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

// Attached reports whether a client attached for events.
func (s *Supplicant) Attached() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.attached) > 0
}

// Push sends one event to every attached client, with the priority
// prefix the supplicant puts on each event.
func (s *Supplicant) Push(t *testing.T, event string) {
	t.Helper()
	s.mu.Lock()
	peers := append([]*net.UnixAddr(nil), s.attached...)
	s.mu.Unlock()
	for _, peer := range peers {
		if _, err := s.conn.WriteToUnix([]byte("<3>"+event), peer); err != nil {
			t.Fatal(err)
		}
	}
}

func (s *Supplicant) serve() {
	buf := make([]byte, 4096)
	for {
		n, from, err := s.conn.ReadFromUnix(buf)
		if err != nil {
			return
		}
		command := string(buf[:n])
		s.mu.Lock()
		reply, known := s.answers[command]
		switch {
		case command == "ATTACH":
			s.attached = append(s.attached, from)
			reply, known = "OK\n", true
		default:
			s.asked = append(s.asked, command)
		}
		s.mu.Unlock()
		if !known {
			reply = "UNKNOWN COMMAND\n"
		}
		_, _ = s.conn.WriteToUnix([]byte(reply), from)
	}
}
