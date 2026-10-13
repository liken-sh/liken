package worker

// The operator and the worker are the same binary, but a rollout runs
// an old worker beside a new operator for a while, so these tests fix
// the wire form of each message.

import (
	"encoding/json"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/sky"
)

func TestTheWireFormOfEachMessage(t *testing.T) {
	cases := []struct {
		name    string
		message any
		want    string
	}{
		{"an extraction of a centering frame",
			ExtractRequest{
				Frame: "east-main/east-tonight/_centering/0199d3a0-0000-7000-8000-000000000001.fits",
				Hint:  Hint{Center: sky.Position{RA: 83.822083, Dec: -5.391111}, Radius: 5, ScaleLow: 2.0115, ScaleHigh: 2.4585},
			},
			`{"frame":"east-main/east-tonight/_centering/0199d3a0-0000-7000-8000-000000000001.fits","hint":{"center":{"ra":83.822083,"dec":-5.391111},"radius":5,"scaleLow":2.0115,"scaleHigh":2.4585}}`},
		{"the stars of a frame",
			ExtractAnswer{AXY: []byte("SIMPLE"), Stars: 212, MedianFWHM: 2.7},
			`{"axy":"U0lNUExF","stars":212,"medianFWHM":2.7}`},
		{"the outbox",
			OutboxAnswer{FreeBytes: 1073741824, Frames: []string{"east-main/east-tonight/m42/M42_0001.fits"}},
			`{"freeBytes":1073741824,"frames":["east-main/east-tonight/m42/M42_0001.fits"]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(c.message)
			if err != nil || string(raw) != c.want {
				t.Errorf("the message is\n%s, %v\nwant\n%s", raw, err, c.want)
			}
		})
	}
}
