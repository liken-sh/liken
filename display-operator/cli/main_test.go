package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunPrintsTheVersion(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run --version returned an error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Fatalf("stdout = %q, want %q", stdout.String(), version)
	}
}

func TestRunWithNoVerbPrintsUsage(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), nil, &stdout, &stderr); err != nil {
		t.Fatalf("run with no arguments returned an error: %v", err)
	}
	if !strings.Contains(stderr.String(), usageText) {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout is %q, want empty", stdout.String())
	}
}

func TestRunRejectsAnUnknownVerb(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{"frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run returned no error for an unknown verb")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("error is %q, want it to name the verb frobnicate", err)
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
		t.Fatalf("run --help returned an error: %v", err)
	}
	if !strings.Contains(stderr.String(), usageText) {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestRunDispatchesCapture(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{"capture", "HDMI-A-1", "--format", "gif"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run capture --format gif returned no error, want one for the unknown format")
	}
}

func TestArgAt(t *testing.T) {
	positional := []string{"capture", "HDMI-A-1"}
	if got := argAt(positional, 1); got != "HDMI-A-1" {
		t.Fatalf("argAt(1) = %q, want HDMI-A-1", got)
	}
	if got := argAt(positional, 2); got != "" {
		t.Fatalf("argAt(2) = %q, want an empty string", got)
	}
}
