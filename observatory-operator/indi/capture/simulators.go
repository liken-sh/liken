package capture

import "fmt"

// Simulators are the drivers that observatory-operator runs, without
// their indi_simulator_ prefix. Plan 06 gives a kind to each. Each one
// has a directory of transcripts in indi/testdata/.
var Simulators = []string{
	"telescope", "ccd", "guide", "wheel", "focus", "rotator", "dome",
	"weather", "dustcover", "lightpanel", "gps", "pac", "sqm", "io",
	"receiver",
}

// SimulatorScript is what a capture sends to one simulator. Every
// simulator's transcripts have the phases baseline, connect, its own
// steps, then disconnect, and each phase's reply is the file of the
// same name. The replay tests serve each file in answer to its phase's
// message.
func SimulatorScript(simulator, device string) Script {
	phases := []Phase{
		{"baseline", Baseline},
		{"connect", switchMessage(device, "CONNECTION", "CONNECT")},
	}
	phases = append(phases, simulatorSteps(device)[simulator]...)
	phases = append(phases, Phase{"disconnect", switchMessage(device, "CONNECTION", "DISCONNECT")})
	return Script{Setup: simulatorSetup(device)[simulator], Phases: phases}
}

// simulatorSetup asks for the camera's frames before it connects, so
// the reply to its exposure holds the BLOB.
func simulatorSetup(device string) map[string][]string {
	return map[string][]string{
		"ccd": {fmt.Sprintf(`<enableBLOB device=%q name="CCD1">Also</enableBLOB>`, device)},
	}
}

// simulatorSteps are the changes captured between connect and
// disconnect. The focuser moves within its limits and then past them,
// which the driver refuses with Alert. The dome turns, which takes it
// through Busy. The camera sets a frame of 8 by 8 pixels, selects slot
// 2 of its own wheel, and takes a frame of 0.1 s.
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
