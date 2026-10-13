package promote

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

func number(v float64) *float64 { return &v }

func TestTheWireFormOfAReport(t *testing.T) {
	report := Report{
		Totals:   []Total{{Settings: catalog.Settings{Filter: "Luminance", ExposureSeconds: 300, Binning: 1, Gain: number(100), Offset: number(10)}, Frames: 37}},
		Promoted: 2, Bytes: 5253120, Left: 2,
		Waiting:  []Frame{{File: "east-main/east-tonight/m42/M42_0003.fits", Reason: ReasonIncomplete}},
		Failures: []Frame{{File: "east-main/east-tonight/m42/M42_0004.fits", Reason: "NameConflict", Message: "astro/M42_0004.fits is another frame"}},
	}
	want := `{"totals":[{"filter":"Luminance","exposureSeconds":300,"binning":1,"gain":100,"offset":10,"frames":37}],` +
		`"promoted":2,"bytes":5253120,"left":2,` +
		`"waiting":[{"file":"east-main/east-tonight/m42/M42_0003.fits","reason":"Incomplete"}],` +
		`"failures":[{"file":"east-main/east-tonight/m42/M42_0004.fits","reason":"NameConflict","message":"astro/M42_0004.fits is another frame"}]}`
	raw, err := json.Marshal(report)
	if err != nil || string(raw) != want {
		t.Errorf("the report is\n%s, %v\nwant\n%s", raw, err, want)
	}
}

// Plan 03 states that the termination message's 4,096 bytes fit about
// 25 sequences. The largest totals that MaxTotals allows, with room for
// a few frames that wait, must fit.
func TestMaxTotalsFitInTheTerminationMessage(t *testing.T) {
	total := Total{Settings: catalog.Settings{
		Filter: strings.Repeat("H", 24), ExposureSeconds: 1234.5, Binning: 16, Gain: number(65535), Offset: number(65535),
	}, Frames: 99999}
	report := Report{Truncated: true, Promoted: 9999, Bytes: 999999999999}
	for range MaxTotals {
		report.Totals = append(report.Totals, total)
	}
	for range 4 {
		report.Waiting = append(report.Waiting, Frame{File: strings.Repeat("x", 120) + ".fits", Reason: ReasonIncomplete})
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > MaxReportBytes {
		t.Errorf("the report takes %d bytes, %v; the limit is %d", len(raw), err, MaxReportBytes)
	}
}

// A night of many frames that wait, or of many combinations of
// settings, still fits: Fit keeps the first totals and as many frames
// as fit, and Left still counts every frame.
func TestFitCutsAReportToTheTerminationMessage(t *testing.T) {
	report := Report{Left: 100}
	for frames := range MaxTotals + 5 {
		report.Totals = append(report.Totals, Total{Settings: catalog.Settings{ExposureSeconds: float64(frames)}, Frames: 1})
	}
	for range 50 {
		report.Waiting = append(report.Waiting, Frame{File: strings.Repeat("w", 100), Reason: ReasonIncomplete})
		report.Failures = append(report.Failures, Frame{File: strings.Repeat("f", 100), Reason: ReasonCopyFailed})
	}

	fit := report.Fit()

	raw, _ := json.Marshal(fit)
	if len(raw) > MaxReportBytes || len(fit.Totals) != MaxTotals || !fit.Truncated || fit.Left != 100 || len(fit.Waiting) != 0 || len(fit.Failures) == 0 {
		t.Errorf("the report takes %d bytes, with %d totals, %d waiting, %d failures, and %d left",
			len(raw), len(fit.Totals), len(fit.Waiting), len(fit.Failures), fit.Left)
	}
}
