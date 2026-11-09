package main

// appearancesfaceless_test.go proves the read of the faceless end of a
// detections record.

import (
	"path/filepath"
	"strings"
	"testing"
)

// A record body of one-second samples from 0, where faced holds the seconds
// with a face, and a keyframe with a face at each second in keyed.
func detectionsSamples(seconds int, faced []int, keyed ...int) string {
	face := `{"x":1,"y":2,"w":30,"h":40,"score":0.9,"landmarks":[],"embedding":"AA=="}`
	var lines strings.Builder
	for second := range seconds {
		for _, at := range keyed {
			if at == second {
				lines.WriteString(`{"time":` + itoa(int64(second)) + `.5,"keyframe":true,"faces":[` + face + `]}` + "\n")
			}
		}
		faces := ""
		for _, at := range faced {
			if at == second {
				faces = face
			}
		}
		lines.WriteString(`{"time":` + itoa(int64(second)) + `.001,"faces":[` + faces + `]}` + "\n")
	}
	return lines.String()
}

func TestTheFacelessEndOfADetectionsRecord(t *testing.T) {
	cases := []struct {
		name    string
		samples string
		want    timeSpan
	}{
		{
			name:    "faces end before the last sample",
			samples: detectionsSamples(10, []int{0, 1, 2, 5}),
			want:    timeSpan{Start: 6001, End: 9001},
		},
		{
			name:    "a face in the last sample",
			samples: detectionsSamples(10, []int{2, 9}),
			want:    timeSpan{Start: 9001, End: 9001},
		},
		{
			name:    "no face at all",
			samples: detectionsSamples(4, nil),
			want:    timeSpan{Start: 1, End: 3001},
		},
		{
			// The run is measured in the one-second samples, as the limits
			// of the estimate were measured.
			name:    "a face in a keyframe inside the run",
			samples: detectionsSamples(10, []int{0, 1}, 7),
			want:    timeSpan{Start: 2001, End: 9001},
		},
		{
			name:    "no samples",
			samples: "",
			want:    timeSpan{},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			video := filepath.Join(t.TempDir(), "Film.mkv")
			writeFile(t, detectionsPath(video), detectionsLine(t, 5)+test.samples)

			got, err := readFacelessTail(video)

			if err != nil || got != test.want {
				t.Errorf("faceless = %+v, %v, want %+v", got, err, test.want)
			}
		})
	}
}

func TestADetectionsRecordThatDoesNotReadIsAnError(t *testing.T) {
	cases := []struct {
		name   string
		record string
	}{
		{name: "no record"},
		{name: "a sample that is not JSON", record: "{}\nnot json\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			video := filepath.Join(t.TempDir(), "Film.mkv")
			if test.record != "" {
				writeFile(t, detectionsPath(video), test.record)
			}

			if _, err := readFacelessTail(video); err == nil {
				t.Error("read a record that does not read, want an error")
			}
		})
	}
}
