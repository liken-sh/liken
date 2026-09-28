package main

// Lifting must be exact or absent. A header either matches its
// format byte for byte and yields its facts, or the line ships with
// fallback facts and an untouched body. The near-miss cases here
// matter most, because a half-matched header must never produce
// half-lifted facts.

import (
	"testing"
	"time"
)

func TestLiftLogrus(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		time     string
		severity string
	}{
		{
			name:     "a k3s info line",
			line:     `time="2026-07-07T13:51:16Z" level=info msg="Starting k3s v1.33.1+k3s1"`,
			time:     "2026-07-07T13:51:16Z",
			severity: "info",
		},
		{
			name:     "an error with a zone offset",
			line:     `time="2026-07-07T09:51:16-04:00" level=error msg="tunnel disconnected"`,
			time:     "2026-07-07T09:51:16-04:00",
			severity: "err",
		},
		{
			name:     "warning maps to the syslog word",
			line:     `time="2026-07-07T13:51:16Z" level=warning msg="deprecated flag"`,
			time:     "2026-07-07T13:51:16Z",
			severity: "warning",
		},
		{
			name:     "fatal maps to crit",
			line:     `time="2026-07-07T13:51:16Z" level=fatal msg="no"`,
			time:     "2026-07-07T13:51:16Z",
			severity: "crit",
		},
		{
			name:     "level at end of line",
			line:     `time="2026-07-07T13:51:16Z" level=debug`,
			time:     "2026-07-07T13:51:16Z",
			severity: "debug",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, err := time.Parse(time.RFC3339, c.time)
			if err != nil {
				t.Fatal(err)
			}
			when, severity := lift(c.line, observed)
			if !when.Equal(want) {
				t.Errorf("time: got %v, want %v", when, want)
			}
			if severity != c.severity {
				t.Errorf("severity: got %q, want %q", severity, c.severity)
			}
		})
	}
}

func TestLiftKlog(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		time     time.Time
		severity string
	}{
		{
			name:     "an apiserver info line",
			line:     `I0707 13:51:16.123456       1 controller.go:615] quota admission added`,
			time:     time.Date(2026, 7, 7, 13, 51, 16, 123456000, time.UTC),
			severity: "info",
		},
		{
			name:     "an error line",
			line:     `E0707 13:51:16.000001       1 leaderelection.go:332] error retrieving lease`,
			time:     time.Date(2026, 7, 7, 13, 51, 16, 1000, time.UTC),
			severity: "err",
		},
		{
			name: "december read in january belongs to last year",
			line: `W1231 23:59:59.999999       1 reflector.go:484] watch closed`,
			// observed is July 2026, so December 31 would land five
			// months in the future. The lift moves it back to 2025.
			time:     time.Date(2025, 12, 31, 23, 59, 59, 999999000, time.UTC),
			severity: "warning",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			when, severity := lift(c.line, observed)
			if !when.Equal(c.time) {
				t.Errorf("time: got %v, want %v", when, c.time)
			}
			if severity != c.severity {
				t.Errorf("severity: got %q, want %q", severity, c.severity)
			}
		})
	}
}

func TestLiftFallsBackOnNearMisses(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"empty line", ""},
		{"plain prose", "containerd successfully booted in 0.055561s"},
		{"logrus time unquoted", `time=2026-07-07T13:51:16Z level=info msg="x"`},
		{"logrus time unterminated", `time="2026-07-07T13:51:16Z level=info`},
		{"logrus bad timestamp", `time="yesterday-ish" level=info msg="x"`},
		{"logrus missing level", `time="2026-07-07T13:51:16Z" msg="x"`},
		{"logrus unknown level", `time="2026-07-07T13:51:16Z" level=loud msg="x"`},
		{"klog wrong letter", `X0707 13:51:16.123456       1 x.go:1] x`},
		{"klog letters in the date", `I07O7 13:51:16.123456       1 x.go:1] x`},
		{"klog missing colon", `I0707 13.51:16.123456       1 x.go:1] x`},
		{"klog month zero", `I0007 13:51:16.123456       1 x.go:1] x`},
		{"klog month thirteen", `I1307 13:51:16.123456       1 x.go:1] x`},
		{"klog too short", `I0707 13:51`},
		{"a go panic", "panic: runtime error: invalid memory address"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			when, severity := lift(c.line, observed)
			if !when.Equal(observed) {
				t.Errorf("time should fall back to the observation clock: got %v", when)
			}
			if severity != "info" {
				t.Errorf("severity should fall back to info: got %q", severity)
			}
		})
	}
}
