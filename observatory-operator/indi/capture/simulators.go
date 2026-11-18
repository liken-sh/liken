package capture

import "fmt"

// Simulators are the INDI simulator drivers that observatory-operator
// supports, named without their indi_simulator_ prefix. Each has a
// directory of transcripts in indi/inditest/testdata/.
var Simulators = []string{
	"telescope", "ccd", "guide", "wheel", "focus", "rotator", "dome",
	"weather", "dustcover", "lightpanel", "gps", "pac", "io",
	"receiver",
}

// SimulatorScript returns what a capture sends to one simulator: the
// phases baseline, connect, the simulator's own steps, and disconnect.
// The server's reply to each phase is saved in a file named for the
// phase, and the replay tests serve that file as the reply to the
// phase's message.
func SimulatorScript(simulator, device string) Script {
	phases := []Phase{
		{"baseline", Baseline},
		{"connect", switchMessage(device, "CONNECTION", "CONNECT")},
	}
	phases = append(phases, simulatorSteps(device)[simulator]...)
	phases = append(phases, Phase{"disconnect", switchMessage(device, "CONNECTION", "DISCONNECT")})
	return Script{Setup: simulatorSetup(device)[simulator], Phases: phases}
}

// simulatorSetup enables BLOBs from the camera before it connects, so
// the reply to its exposure contains the frame.
func simulatorSetup(device string) map[string][]string {
	return map[string][]string{
		"ccd": {fmt.Sprintf(`<enableBLOB device=%q name="CCD1">Also</enableBLOB>`, device)},
	}
}

// simulatorSteps are the changes that a capture sends between connect
// and disconnect. The focuser moves within its limits, then to a
// position beyond them, which the driver refuses with the Alert state.
// The dome turns, so the transcript records the Busy state.
func simulatorSteps(device string) map[string][]Phase {
	return map[string][]Phase{
		"ccd": {
			{"frame", fmt.Sprintf(`<newNumberVector device=%q name="CCD_FRAME"><oneNumber name="X">0</oneNumber><oneNumber name="Y">0</oneNumber><oneNumber name="WIDTH">8</oneNumber><oneNumber name="HEIGHT">8</oneNumber></newNumberVector>`, device)},
			{"slot", numberMessage(device, "FILTER_SLOT", "FILTER_SLOT_VALUE", "2")},
			{"expose", numberMessage(device, "CCD_EXPOSURE", "CCD_EXPOSURE_VALUE", "0.1")},
		},
		"focus": {
			{"move", numberMessage(device, "ABS_FOCUS_POSITION", "FOCUS_ABSOLUTE_POSITION", "52000")},
			{"refuse", numberMessage(device, "ABS_FOCUS_POSITION", "FOCUS_ABSOLUTE_POSITION", "200000")},
		},
		"dome": {
			{"move", numberMessage(device, "ABS_DOME_POSITION", "DOME_ABSOLUTE_POSITION", "30")},
		},
	}
}
