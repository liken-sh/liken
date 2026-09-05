package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunExitCodes(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		code int
	}{
		{name: "the version alone", args: []string{"--version"}, code: 0},
		{name: "a node id alone", args: []string{"--node-id", "node-1"}, code: 0},
		{
			name: "every flag",
			args: []string{
				"--endpoint", "unix:///run/csi/csi.sock",
				"--node-id", "node-1",
				"--store", "/srv/git-csi",
			},
			code: 0,
		},
		{name: "no arguments", args: nil, code: 1},
		{name: "an unknown flag", args: []string{"--bogus"}, code: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			if code := run(c.args, out); code != c.code {
				t.Errorf("run(%q) = %d, want %d (output: %q)", c.args, code, c.code, out)
			}
		})
	}
}

func TestRunPrintsTheVersion(t *testing.T) {
	out := &bytes.Buffer{}
	run([]string{"--version"}, out)
	if out.String() != version+"\n" {
		t.Errorf("run printed %q, want %q", out, version+"\n")
	}
}

func TestRunSaysNothingOnAGoodCommandLine(t *testing.T) {
	out := &bytes.Buffer{}
	run([]string{"--node-id", "node-1"}, out)
	if out.String() != "" {
		t.Errorf("run printed %q, want nothing", out)
	}
}

func TestRunReportsAMissingNodeID(t *testing.T) {
	out := &bytes.Buffer{}
	run(nil, out)
	if !strings.Contains(out.String(), "--node-id is required") {
		t.Errorf("run printed %q, want the missing node id reported", out)
	}
}

func TestRunReportsAnUnknownFlag(t *testing.T) {
	out := &bytes.Buffer{}
	run([]string{"--bogus"}, out)
	if !strings.Contains(out.String(), "not defined") {
		t.Errorf("run printed %q, want the unknown flag reported", out)
	}
}

func TestParseDefaultsTheEndpointAndTheStore(t *testing.T) {
	cfg, err := parse([]string{"--node-id", "node-1"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := config{
		endpoint: "unix:///csi/csi.sock",
		nodeID:   "node-1",
		store:    "/var/lib/liken/git-csi",
	}
	if *cfg != want {
		t.Errorf("parse = %+v, want %+v", *cfg, want)
	}
}

func TestParseTakesEveryFlag(t *testing.T) {
	cfg, err := parse([]string{
		"--endpoint", "unix:///run/csi/csi.sock",
		"--node-id", "node-1",
		"--store", "/srv/git-csi",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := config{
		endpoint: "unix:///run/csi/csi.sock",
		nodeID:   "node-1",
		store:    "/srv/git-csi",
	}
	if *cfg != want {
		t.Errorf("parse = %+v, want %+v", *cfg, want)
	}
}

func TestParseAnswersNothingForTheVersion(t *testing.T) {
	cfg, err := parse([]string{"--version"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg != nil {
		t.Errorf("parse = %+v, want no configuration", cfg)
	}
}
