package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunPrintsTheVersion(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run --version: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Fatalf("stdout = %q, want %q", stdout.String(), version)
	}
}

func TestRunWithNoVerbPrintsUsage(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), nil, &stdout, &stderr); err != nil {
		t.Fatalf("run with no args: %v", err)
	}
	if !strings.Contains(stderr.String(), usageText) {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout carried %q", stdout.String())
	}
}

func TestRunRejectsAnUnknownVerb(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{"frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run returned no error for an unknown verb")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("error %q does not name the verb", err)
	}
}

func TestRunRejectsAnUnknownFlag(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"--nope"}, &stdout, &stderr); err == nil {
		t.Fatal("run returned no error for an unknown flag")
	}
}

func TestRunHelpIsNotAnError(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("run --help: %v", err)
	}
	if !strings.Contains(stderr.String(), usageText) {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
}

// Each verb checks its words before it reaches a cluster.
func TestRunChecksTheWordsOfAVerb(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"set-up with no kind", []string{"set-up"}, "set-up needs a kind and a name"},
		{"take-down with no name", []string{"take-down", "telescope"}, "take-down needs a kind and a name"},
		{"a kind with no procedures", []string{"set-up", "guider", "east"}, `"guider" is not a kind that can be set up`},
		{"an until that is no time", []string{"set-up", "telescope", "east", "--until", "soon"}, `--until "soon" is neither a time`},
		{"an until on a take-down", []string{"take-down", "telescope", "east", "--until", "6h"}, "--until applies to set-up, not to take-down"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			err := run(context.Background(), c.args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("run(%v) = %v, want an error with %q", c.args, err, c.want)
			}
		})
	}
}

func TestArgAt(t *testing.T) {
	positional := []string{"set-up", "telescope", "east"}
	if got := argAt(positional, 2); got != "east" {
		t.Fatalf("argAt(2) = %q, want east", got)
	}
	if got := argAt(positional, 3); got != "" {
		t.Fatalf("argAt(3) = %q, want an empty string", got)
	}
}
