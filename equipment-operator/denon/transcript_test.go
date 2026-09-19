// The parser against a real receiver: every line the house's AVR-X1700H
// sent, folded into the state it should produce.

package denon

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

// foldTranscript reads one captured transcript and folds every line
// that is not a comment.
func foldTranscript(t *testing.T, path string) denonState {
	t.Helper()
	file, err := os.Open(path)
	mustSucceed(t, err)
	defer file.Close()

	state := newDenonState()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		state, _, _, _ = applyDenonLine(state, line)
	}
	mustSucceed(t, scanner.Err())
	return state
}

func TestTheAVRX1700HTranscriptFoldsIntoItsState(t *testing.T) {
	state := foldTranscript(t, "testdata/avr-x1700h.txt")

	mustMatch(t, state.Reachable, equipment.ConditionUnknown)
	mustMatch(t, state.System.Power, powerOn)
	mustMatch(t, state.Main.Power, equipment.PowerOn)
	mustMatch(t, state.Main.Volume, 130)
	mustMatch(t, state.Main.VolumeMax, 168)
	mustMatch(t, state.Main.Input, "MPLAY")
	mustMatch(t, state.Main.SoundMode, "STEREO")
	mustMatch(t, state.Main.Mute, false)
	mustMatch(t, state.Main.Sleep, 0)
	mustMatch(t, state.Main.Quick, 0)

	mustMatch(t, state.Zone2.Power, equipment.PowerOn)
	mustMatch(t, state.Zone2.Input, "PHONO")
	mustMatch(t, state.Zone2.Volume, 180)
	mustMatch(t, state.Zone2.Mute, false)
	mustMatch(t, state.Zone2.Sleep, 0)

	mustMatch(t, state.System.Eco, "auto")
	mustMatch(t, state.System.Dimmer, "bright")
	mustMatch(t, state.System.AutoStandby, "off")
	mustMatch(t, state.System.SpeakerPreset, 1)
	mustMatch(t, state.System.AudioInputMode, "hdmi")
	mustMatch(t, state.System.VideoSelect, "off")
	mustMatch(t, state.System.BluetoothTransmitter, "off")
	mustMatch(t, state.System.BluetoothOutput, "speakers")

	mustMatch(t, state.Tone.Control, false)
	mustMatch(t, state.Tone.Bass, 0)
	mustMatch(t, state.Tone.Treble, 0)

	mustMatch(t, state.Audyssey.Multeq, "reference")
	mustMatch(t, state.Audyssey.DynamicEq, true)
	mustMatch(t, state.Audyssey.ReferenceLevelOffset, 0)
	mustMatch(t, state.Audyssey.DynamicVolume, "off")
	mustMatch(t, state.Audyssey.LoudnessManagement, true)

	mustMatch(t, state.Audio.DRC, "off")
	mustMatch(t, state.Audio.LFE, 0)
	mustMatch(t, state.Audio.Effect, 0)
	mustMatch(t, state.Audio.Delay, 0)
	mustMatch(t, state.Audio.AudioDelay, 0)
	mustMatch(t, state.Audio.Subwoofer, true)
	mustMatch(t, state.Audio.Restorer, "off")
	mustMatch(t, state.Audio.GraphicEq, "off")
	mustMatch(t, state.Audio.HeadphoneEq, "off")
	mustMatch(t, state.Audio.SpeakerVirtualizer, true)
	mustMatch(t, state.Audio.DialogEnhancer, "off")

	mustMatch(t, state.Channels["FL"], 0)
	mustMatch(t, state.Channels["FR"], 0)
	mustMatch(t, state.Channels["SW"], 0)
}
