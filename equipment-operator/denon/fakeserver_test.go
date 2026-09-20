// The fake receiver the client tests run against. It holds the state a
// real AVR holds, answers the five queries the way the AVR-X1700H
// answered them, echoes every set as an event, and can turn its own
// knob or drop the connection.

package denon

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
var noiseLines = []string{"SVOFF", "PSDRC OFF", "PSLFE 00"}

type fakeReceiver struct {
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

// startFakeReceiver listens on the loopback and answers until the test
// ends.
func startFakeReceiver(t *testing.T) *fakeReceiver {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	mustSucceed(t, err)

	receiver := &fakeReceiver{
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

func (f *fakeReceiver) address() string {
	return f.listener.Addr().String()
}

func (f *fakeReceiver) accept() {
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

func (f *fakeReceiver) serve(conn net.Conn) {
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
func (f *fakeReceiver) answer(command string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	switch {
	case command == "PW?":
		f.send(f.power)
	case command == "MV?":
		f.send("MV" + HalfStepDigits(f.volume))
		f.send("MVMAX " + HalfStepDigits(f.volumeMax))
	case command == "MU?":
		f.send(f.muteLine())
	case command == "SI?":
		f.send("SI" + f.input)
	case command == "MS?":
		f.send("MS" + f.soundMode)
		for _, line := range noiseLines {
			f.send(line)
		}
	case command == PowerOnCommand, command == powerStandbyCommand:
		f.power = command
		f.send(f.power)
	case command == MuteOnCommand, command == MuteOffCommand:
		f.mute = command == MuteOnCommand
		f.send(f.muteLine())
	case strings.HasPrefix(command, VolumePrefix):
		halves, ok := ParseHalfSteps(command[len(VolumePrefix):])
		if !ok {
			return
		}
		f.volume = halves
		f.send("MV" + HalfStepDigits(f.volume))
	case strings.HasPrefix(command, InputPrefix):
		f.input = command[len(InputPrefix):]
		f.send("SI" + f.input)
	case isSettingCommand(command):
		// A set command for a setting is echoed back, which the parser
		// folds into the state the way a real receiver would.
		f.send(command)
	}
}

// settingEchoPrefixes are the prefixes a set command for a setting
// carries. The fake echoes them so a test can apply a Settings and read
// it back.
var settingEchoPrefixes = []string{
	"ECO", "DIM ", "STBY", "SPPR ", "SD", "SV", "BTTX ", "PSMULTEQ:", "PSDYNEQ ",
	"PSREFLEV ", "PSDYNVOL ", "PSLOM ", "PSDRC ", "PSLFE ", "PSEFF ", "PSDELAY ",
	"PSDEL ", "PSSWR ", "PSRSTR ", "PSGEQ ", "PSHEQ ", "PSSPV ", "PSDEH ",
	"PSBAS ", "PSTRE ", "PSTONE CTRL ", "CV",
}

// isSettingCommand answers whether one command sets a setting, which is
// a set prefix and not a query.
func isSettingCommand(command string) bool {
	if strings.HasSuffix(command, "?") {
		return false
	}
	for _, prefix := range settingEchoPrefixes {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeReceiver) muteLine() string {
	if f.mute {
		return MuteOnCommand
	}
	return MuteOffCommand
}

// send writes one event to every open connection. The caller holds the
// lock.
func (f *fakeReceiver) send(line string) {
	for _, conn := range f.conns {
		conn.SetWriteDeadline(time.Now().Add(testTimeout))
		conn.Write([]byte(line + "\r"))
	}
}

// turnKnob is a hand on the equipment: the receiver moves its own
// volume and volunteers the event, with nothing having asked.
func (f *fakeReceiver) turnKnob(halves int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.volume = halves
	f.send("MV" + HalfStepDigits(f.volume))
}

// setMute is the same hand on the mute button.
func (f *fakeReceiver) setMute(muted bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.mute = muted
	f.send(f.muteLine())
}

// dropConnections closes every open connection, the way a router reboot
// takes the socket with it.
func (f *fakeReceiver) dropConnections() {
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
func (f *fakeReceiver) waitForCommand(t *testing.T) string {
	t.Helper()
	select {
	case command := <-f.commands:
		return command
	case <-time.After(testTimeout):
		t.Fatal("the client sent no command")
		return ""
	}
}

// waitForCommands reads until it sees the command it wants, and reports
// what it passed over, so a test names the one command it cares about
// out of the burst around it.
func (f *fakeReceiver) waitForCommands(t *testing.T, want string) []string {
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
