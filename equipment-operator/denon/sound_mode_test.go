package denon

// The sound mode compare: a declared mode against the mode the
// receiver reports.

import "testing"

func TestSameSoundModeMatchesTheReportForTheDeclaredFamily(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		reported string
		same     bool
	}{
		{"the same word", "STEREO", "STEREO", true},
		{"a declared word in lowercase", "stereo", "STEREO", true},
		{"a report with spaces at the end", "MULTI CH IN", "MULTI CH IN  ", true},
		{"a report with a run of spaces", "DOLBY DIGITAL", "DOLBY AUDIO - DD+   + DSUR", true},
		{"a Dolby report for the Dolby family", "dolby digital", "DOLBY AUDIO-DD", true},
		{"a DTS report for the DTS family", "DTS SURROUND", "DTS HD MSTR", true},
		{"a report the Dolby and DTS families share", "DTS SURROUND", "MULTI CH IN", true},
		{"a multichannel stereo report", "MCH STEREO", "MULTI CH STEREO", true},
		{"a movie report for the movie family", "MOVIE", "DOLBY PL2 MOVIE", true},
		{"another family", "STEREO", "DOLBY AUDIO-DD", false},
		{"a Dolby report for the DTS family", "DTS SURROUND", "DOLBY AUDIO-DD", false},
		{"a report the table lists under another family", "MOVIE", "DOLBY SURROUND", false},
		{"a declared word that is no family", "MULTI CH IN", "DTS HD MSTR", false},
		{"no report", "STEREO", "", false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, SameSoundMode(one.declared, one.reported), one.same)
		})
	}
}
