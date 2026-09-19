// The arithmetic between the receiver's own half steps and the digits
// a person reads.

package denon

import (
	"strconv"
	"testing"
)

func TestParseHalfStepsReadsTheDigitsTheReceiverSends(t *testing.T) {
	cases := []struct {
		digits string
		halves int
		ok     bool
	}{
		{digits: "50", halves: 100, ok: true},
		{digits: "505", halves: 101, ok: true},
		{digits: "05", halves: 10, ok: true},
		{digits: "055", halves: 11, ok: true},
		{digits: "695", halves: 139, ok: true},
		{digits: "00", halves: 0, ok: true},
		{digits: "5", halves: unknownHalves, ok: false},
		{digits: "5055", halves: unknownHalves, ok: false},
		{digits: "abc", halves: unknownHalves, ok: false},
		{digits: "501", halves: unknownHalves, ok: false},
		{digits: "-5", halves: unknownHalves, ok: false},
	}
	for _, test := range cases {
		t.Run(test.digits, func(t *testing.T) {
			halves, ok := ParseHalfSteps(test.digits)
			mustMatch(t, ok, test.ok)
			mustMatch(t, halves, test.halves)
		})
	}
}

func TestFormatHalfStepsWritesTheVolumeAPersonReads(t *testing.T) {
	cases := []struct {
		halves int
		want   string
	}{
		{halves: 100, want: "50"},
		{halves: 101, want: "50.5"},
		{halves: 139, want: "69.5"},
		{halves: 0, want: "0"},
		{halves: unknownHalves, want: ""},
	}
	for _, test := range cases {
		t.Run(strconv.Itoa(test.halves), func(t *testing.T) {
			mustMatch(t, FormatHalfSteps(test.halves), test.want)
		})
	}
}

func TestHalfStepDigitsWritesTheDigitsASetCommandCarries(t *testing.T) {
	cases := []struct {
		halves int
		want   string
	}{
		{halves: 100, want: "50"},
		{halves: 101, want: "505"},
		{halves: 10, want: "05"},
		{halves: 11, want: "055"},
		{halves: 139, want: "695"},
	}
	for _, test := range cases {
		t.Run(strconv.Itoa(test.halves), func(t *testing.T) {
			mustMatch(t, HalfStepDigits(test.halves), test.want)
		})
	}
}

// Every count the receiver can report survives the trip out to digits
// and back, which is what makes the set command and the echo
// comparable.
func TestEveryHalfStepCountSurvivesTheRoundTrip(t *testing.T) {
	for count := 0; count <= 196; count++ {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			halves, ok := ParseHalfSteps(HalfStepDigits(count))
			mustMatch(t, ok, true)
			mustMatch(t, halves, count)
		})
	}
}
