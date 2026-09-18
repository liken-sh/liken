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

func TestRunDispatchesRescan(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{"rescan", "movies"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run dispatched rescan without surfacing the stub")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Fatalf("error %q does not say rescan is a stub", err)
	}
}

func TestRunDispatchesReenrich(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{"reenrich"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run dispatched reenrich without surfacing the missing library")
	}
}

func TestArgAt(t *testing.T) {
	positional := []string{"reenrich", "movies"}
	if got := argAt(positional, 1); got != "movies" {
		t.Fatalf("argAt(1) = %q, want movies", got)
	}
	if got := argAt(positional, 2); got != "" {
		t.Fatalf("argAt(2) = %q, want an empty string", got)
	}
}
