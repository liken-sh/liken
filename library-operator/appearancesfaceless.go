package main

// appearancesfaceless.go reads the faceless end of a detections record: the
// final run of one-second samples in which the detector found no face. The
// marks estimate reads the start of that run as the last clue to where a
// film's credits start (markfaces.go). The record holds a vector for every
// face and runs to megabytes for a feature, so the walk never reads it. The
// appearances worker reads it once, after the detect pass or in place of
// it, and records the run in its ledger entry, which the walk reads.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// One line of a detections record after the header. The faces are counted
// and not read, because only their presence matters here.
type detectionsSample struct {
	Time     float64           `json:"time"`
	Keyframe bool              `json:"keyframe"`
	Faces    []json.RawMessage `json:"faces"`
}

// The final run of the record's samples with no face, from the first sample
// of the run to the last sample of the record, in milliseconds. Where the
// last sample holds a face, the run is empty and starts and ends at that
// sample. A keyframe is left out: the tool reads it besides the sample of
// its second, and the limits of the estimate were measured on the
// one-second samples alone.
func readFacelessTail(video string) (timeSpan, error) {
	file, err := os.Open(detectionsPath(video))
	if err != nil {
		return timeSpan{}, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	if _, err := reader.ReadBytes('\n'); err != nil && !errors.Is(err, io.EOF) {
		return timeSpan{}, err
	}
	var run timeSpan
	open := false
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var sample detectionsSample
			if err := json.Unmarshal(line, &sample); err != nil {
				return timeSpan{}, fmt.Errorf("reading a sample of %s: %w", detectionsPath(video), err)
			}
			if !sample.Keyframe {
				at := int64(math.Round(sample.Time * 1000))
				if len(sample.Faces) > 0 || !open {
					run.Start = at
				}
				run.End = at
				open = len(sample.Faces) == 0
			}
		}
		if errors.Is(err, io.EOF) {
			return run, nil
		}
		if err != nil {
			return timeSpan{}, err
		}
	}
}
