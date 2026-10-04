package main

// The arithmetic between a figure in a receiver's own scale and a
// driver's smallest steps.

import "testing"

func TestStepsFromScaleCountsTheDriversSmallestSteps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		value      float64
		resolution int
		want       int
	}{
		{"a whole unit", 52, 2, 104},
		{"a half unit", 69.5, 2, 139},
		{"a figure between two steps", 44.3, 2, 89},
		{"below zero", -5, 2, -10},
		{"no resolution at all", 72, 0, 0},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, stepsFromScale(one.value, one.resolution), one.want)
		})
	}
}

func TestFormatStepsWritesTheDriversScale(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		steps      int
		resolution int
		want       string
	}{
		{"a whole unit", 104, 2, "52"},
		{"a half unit", 139, 2, "69.5"},
		{"an unknown volume", -1, 2, ""},
		{"no resolution at all", 10, 0, ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, formatSteps(one.steps, one.resolution), one.want)
		})
	}
}
