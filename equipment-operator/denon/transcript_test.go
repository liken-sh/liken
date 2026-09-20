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
	mustMatch(t, state.Settings.System.Power, powerOn)
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

	mustMatch(t, *state.Settings.System.Eco, "auto")
	mustMatch(t, *state.Settings.System.Dimmer, "bright")
	mustMatch(t, *state.Settings.System.AutoStandby, "off")
	mustMatch(t, *state.Settings.System.SpeakerPreset, 1)
	mustMatch(t, *state.Settings.System.AudioInputMode, "hdmi")
	mustMatch(t, *state.Settings.System.VideoSelect, "off")
	mustMatch(t, *state.Settings.System.BluetoothTransmitter, "off")
	mustMatch(t, *state.Settings.System.BluetoothOutput, "speakers")

	mustMatch(t, *state.Settings.Tone.Control, false)
	mustMatch(t, *state.Settings.Tone.Bass, 0)
	mustMatch(t, *state.Settings.Tone.Treble, 0)

	mustMatch(t, *state.Settings.Audyssey.Multeq, "reference")
	mustMatch(t, *state.Settings.Audyssey.DynamicEq, true)
	mustMatch(t, *state.Settings.Audyssey.ReferenceLevelOffset, 0)
	mustMatch(t, *state.Settings.Audyssey.DynamicVolume, "off")
	mustMatch(t, *state.Settings.Audyssey.LoudnessManagement, true)

	mustMatch(t, *state.Settings.Audio.DRC, "off")
	mustMatch(t, *state.Settings.Audio.LFE, 0)
	mustMatch(t, *state.Settings.Audio.Effect, 0)
	mustMatch(t, *state.Settings.Audio.Delay, 0)
	mustMatch(t, *state.Settings.Audio.AudioDelay, 0)
	mustMatch(t, *state.Settings.Audio.Subwoofer, true)
	mustMatch(t, *state.Settings.Audio.Restorer, "off")
	mustMatch(t, *state.Settings.Audio.GraphicEq, "off")
	mustMatch(t, *state.Settings.Audio.HeadphoneEq, "off")
	mustMatch(t, *state.Settings.Audio.SpeakerVirtualizer, true)
	mustMatch(t, *state.Settings.Audio.DialogEnhancer, "off")

	mustMatch(t, state.Settings.ChannelVolumes["FL"], 0)
	mustMatch(t, state.Settings.ChannelVolumes["FR"], 0)
	mustMatch(t, state.Settings.ChannelVolumes["SW"], 0)
}
