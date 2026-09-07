package main

// The fake receiver every controller test runs against. It holds the
// state a real AVR holds, answers the five queries the way the
// AVR-X1700H answered them, echoes every set as an event, and can turn
// its own knob or drop the connection.

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The lines the real receiver volunteered after the queries, which this
// operator reads none of.
var denonNoise = []string{"SVOFF", "PSDRC OFF", "PSLFE 00"}

type fakeDenon struct {
	listener net.Listener
	commands chan string

	mutex     sync.Mutex
	power     string
	volume    int
	volumeMax int
	mute      bool
	input     string
	soundMode string
	conns     []net.Conn
}

// startFakeDenon listens on the loopback and answers until the test
// ends.
func startFakeDenon(t *testing.T) *fakeDenon {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	mustSucceed(t, err)

	receiver := &fakeDenon{
		listener:  listener,
		commands:  make(chan string, 64),
		power:     "PWSTANDBY",
		volume:    100,
		volumeMax: 139,
		input:     "MPLAY",
		soundMode: "MULTI CH IN",
	}
	t.Cleanup(func() {
		listener.Close()
		receiver.dropConnections()
	})
	go receiver.accept()
	return receiver
}

func (f *fakeDenon) address() string {
	return f.listener.Addr().String()
}

func (f *fakeDenon) accept() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		f.mutex.Lock()
		f.conns = append(f.conns, conn)
		f.mutex.Unlock()
		go f.serve(conn)
	}
}

func (f *fakeDenon) serve(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\r')
		if err != nil {
			return
		}
		command := strings.Trim(line, "\r\n")
		if command == "" {
			continue
		}
		select {
		case f.commands <- command:
		default:
		}
		f.answer(command)
	}
}

// answer folds one command into the state and sends every line the
// receiver would send back, to every client at once.
func (f *fakeDenon) answer(command string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	switch {
	case command == "PW?":
		f.send(f.power)
	case command == "MV?":
		f.send("MV" + halfStepDigits(f.volume))
		f.send("MVMAX " + halfStepDigits(f.volumeMax))
	case command == "MU?":
		f.send(f.muteLine())
	case command == "SI?":
		f.send("SI" + f.input)
	case command == "MS?":
		f.send("MS" + f.soundMode)
		for _, line := range denonNoise {
			f.send(line)
		}
	case command == "PWON", command == "PWSTANDBY":
		f.power = command
		f.send(f.power)
	case command == "MUON", command == "MUOFF":
		f.mute = command == "MUON"
		f.send(f.muteLine())
	case strings.HasPrefix(command, "MV"):
		halves, ok := parseHalfSteps(command[2:])
		if !ok {
			return
		}
		f.volume = halves
		f.send("MV" + halfStepDigits(f.volume))
	case strings.HasPrefix(command, "SI"):
		f.input = command[2:]
		f.send("SI" + f.input)
	}
}

func (f *fakeDenon) muteLine() string {
	if f.mute {
		return "MUON"
	}
	return "MUOFF"
}

// send writes one event to every open connection. The caller holds the
// lock.
func (f *fakeDenon) send(line string) {
	for _, conn := range f.conns {
		conn.SetWriteDeadline(time.Now().Add(testTimeout))
		conn.Write([]byte(line + "\r"))
	}
}

// turnKnob is a hand on the equipment: the receiver moves its own
// volume and volunteers the event, with nothing having asked.
func (f *fakeDenon) turnKnob(halves int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.volume = halves
	f.send("MV" + halfStepDigits(f.volume))
}

// setMute is the same hand on the mute button.
func (f *fakeDenon) setMute(muted bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.mute = muted
	f.send(f.muteLine())
}

// dropConnections closes every open connection, the way a router reboot
// takes the socket with it.
func (f *fakeDenon) dropConnections() {
	f.mutex.Lock()
	conns := f.conns
	f.conns = nil
	f.mutex.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
}

// waitForCommand reads the next command the client sent, and fails the
// test rather than hanging when none arrives.
func (f *fakeDenon) waitForCommand(t *testing.T) string {
	t.Helper()
	select {
	case command := <-f.commands:
		return command
	case <-time.After(testTimeout):
		t.Fatal("the client sent no command")
		return ""
	}
}

// refuseCommand fails the test if the client sends the named command
// inside the window.
func (f *fakeDenon) refuseCommand(t *testing.T, unwanted string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case command := <-f.commands:
			if command == unwanted {
				t.Fatalf("the client sent %q again", unwanted)
			}
		case <-deadline:
			return
		}
	}
}

// waitForCommands reads until it sees the command it wants, and reports
// what it passed over, so a test names the one command it cares about
// out of the burst around it.
func (f *fakeDenon) waitForCommands(t *testing.T, want string) []string {
	t.Helper()
	var seen []string
	deadline := time.After(testTimeout)
	for {
		select {
		case command := <-f.commands:
			seen = append(seen, command)
			if command == want {
				return seen
			}
		case <-deadline:
			t.Fatalf("the client never sent %q; it sent %v", want, seen)
			return seen
		}
	}
}
