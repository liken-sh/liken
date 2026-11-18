// The fake receiver that the client tests run against. It keeps the
// state of a receiver and responds to commands and queries as an
// AVR-X1700H does.

package denon

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The lines that an AVR-X1700H sends after the sound mode, in response
// to an MS? query.
var soundModeExtraLines = []string{"SVOFF", "PSDRC OFF", "PSLFE 00"}

type fakeReceiver struct {
	listening string
	commands  chan string

	mutex     sync.Mutex
	power     string
	volume    int
	volumeMax int
	mute      bool
	input     string
	soundMode string
	hdmi      map[string]string
	conns     []net.Conn
}

// hdmiKeys are the HDMI setup keys the fake keeps, in the order that
// an AVR-X1700H responds to SSHOS ?. VSAUDIO is a separate query.
var hdmiKeys = []string{
	"SSHOSCONARC", "SSHOSCONPSV", "SSHOSRSS", "SSHOSCON", "SSHOSCONSTS",
	"SSHOSCONPOF", "SSHOSPAS", "SSHOSTAS",
}

// avrHDMISettings returns the values that an AVR-X1700H returned for
// VSAUDIO ? and SSHOS ?.
func avrHDMISettings() map[string]string {
	return map[string]string{
		"VSAUDIO": "AMP", "SSHOSCONARC": "OFF", "SSHOSCONPSV": "OFF",
		"SSHOSRSS": "POS", "SSHOSCON": "ON", "SSHOSCONSTS": "LAS",
		"SSHOSCONPOF": "ALL", "SSHOSPAS": "ON", "SSHOSTAS": "OFF",
	}
}

func startFakeReceiver(t *testing.T) *fakeReceiver {
	t.Helper()
	receiver := &fakeReceiver{
		commands:  make(chan string, 64),
		power:     "PWSTANDBY",
		volume:    100,
		volumeMax: 139,
		input:     "MPLAY",
		soundMode: "MULTI CH IN",
		hdmi:      avrHDMISettings(),
	}
	receiver.listening = testNetwork.listen(t, receiver.serve)
	t.Cleanup(receiver.dropConnections)
	return receiver
}

func (f *fakeReceiver) address() string {
	return f.listening
}

func (f *fakeReceiver) serve(conn net.Conn) {
	f.mutex.Lock()
	f.conns = append(f.conns, conn)
	f.mutex.Unlock()
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

// answer applies one command to the state and sends the response lines
// to every connected client, as a receiver does.
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
		for _, line := range soundModeExtraLines {
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
	case command == "VSAUDIO ?":
		f.send("VSAUDIO " + f.hdmi["VSAUDIO"])
	case command == "SSHOS ?":
		for _, key := range hdmiKeys {
			f.send(key + " " + f.hdmi[key])
		}
		f.send("SSHOS END")
	case strings.HasPrefix(command, "VSAUDIO "), strings.HasPrefix(command, "SSHOS"):
		// An AVR-X1700H sends no line after an HDMI setup command, so
		// the fake stores the value and reports it only when queried.
		key, word, held := strings.Cut(command, " ")
		if held {
			f.hdmi[key] = word
		}
	case isSettingCommand(command):
		// A receiver echoes a setting command, and the client applies the
		// echo to its state.
		f.send(command)
	}
}

// settingEchoPrefixes are the prefixes of the setting commands that the
// fake echoes, so a test can apply a Settings and read it back.
var settingEchoPrefixes = []string{
	"ECO", "DIM ", "STBY", "SPPR ", "SD", "SV", "BTTX ", "PSMULTEQ:", "PSDYNEQ ",
	"PSREFLEV ", "PSDYNVOL ", "PSLOM ", "PSDRC ", "PSLFE ", "PSEFF ", "PSDELAY ",
	"PSDEL ", "PSSWR ", "PSRSTR ", "PSGEQ ", "PSHEQ ", "PSSPV ", "PSDEH ",
	"PSBAS ", "PSTRE ", "PSTONE CTRL ", "CV",
}

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

// send writes one line to every open connection. The caller holds
// mutex.
func (f *fakeReceiver) send(line string) {
	for _, conn := range f.conns {
		conn.SetWriteDeadline(time.Now().Add(testTimeout))
		conn.Write([]byte(line + "\r"))
	}
}

// turnKnob changes the volume on the receiver itself, as when someone
// turns the knob, and sends the volume line.
func (f *fakeReceiver) turnKnob(halves int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.volume = halves
	f.send("MV" + HalfStepDigits(f.volume))
}

// setMute changes the mute state on the receiver itself and sends the
// mute line.
func (f *fakeReceiver) setMute(muted bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.mute = muted
	f.send(f.muteLine())
}

// dropConnections closes every open connection, as a network outage
// does.
func (f *fakeReceiver) dropConnections() {
	f.mutex.Lock()
	conns := f.conns
	f.conns = nil
	f.mutex.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
}

func (f *fakeReceiver) waitForCommand(t *testing.T) string {
	t.Helper()
	return f.waitForCommandWithin(t, testTimeout)
}

func (f *fakeReceiver) waitForCommandWithin(t *testing.T, window time.Duration) string {
	t.Helper()
	select {
	case command := <-f.commands:
		return command
	case <-time.After(window):
		t.Fatal("the client sent no command")
		return ""
	}
}

// waitForCommands reads commands until it reads want, and returns all
// that it read, ending with want.
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
			t.Fatalf("the client never sent %q, it sent %v", want, seen)
			return seen
		}
	}
}
