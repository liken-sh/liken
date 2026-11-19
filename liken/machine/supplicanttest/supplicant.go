// Package supplicanttest serves a fake wpa_supplicant control socket,
// so the clients in init and machine-operator run the real
// datagram transport with no radio and no supplicant.
package supplicanttest

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Supplicant replies to commands on a control socket the way
// wpa_supplicant does. It replies OK to ATTACH, the configured reply
// to a command set with Answer, and UNKNOWN COMMAND to any other
// command. Push sends an event to every client that attached.
type Supplicant struct {
	Socket string

	conn *net.UnixConn

	mu       sync.Mutex
	replies  map[string]string
	attached []*net.UnixAddr
	received []string
}

// Start serves a fake supplicant for ifname under dir, at
// the path that machine.SupplicantSocket returns. The test's cleanup
// closes it.
func Start(t *testing.T, dir, ifname string) *Supplicant {
	t.Helper()
	ctrl := filepath.Join(dir, ifname, "ctrl")
	if err := os.MkdirAll(ctrl, 0o700); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	socket := filepath.Join(ctrl, ifname)
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listening on the control socket: %v", err)
	}
	s := &Supplicant{Socket: socket, conn: conn, replies: map[string]string{}}
	t.Cleanup(func() { conn.Close() })
	go s.serve()
	return s
}

// Answer sets the reply to one command.
func (s *Supplicant) Answer(command, reply string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies[command] = reply
}

// Asked returns every command except ATTACH that the fake has
// received, in order.
func (s *Supplicant) Asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.received...)
}

// Attached reports whether a client has attached for events.
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
			t.Fatalf("sending the event: %v", err)
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
		reply, known := s.replies[command]
		switch {
		case command == "ATTACH":
			s.attached = append(s.attached, from)
			reply, known = "OK\n", true
		default:
			s.received = append(s.received, command)
		}
		s.mu.Unlock()
		if !known {
			reply = "UNKNOWN COMMAND\n"
		}
		_, _ = s.conn.WriteToUnix([]byte(reply), from)
	}
}
