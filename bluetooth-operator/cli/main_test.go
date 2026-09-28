package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunPrintsTheVersion(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"--version"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run --version: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Fatalf("stdout = %q, want %q", stdout.String(), version)
	}
}

func TestRunWithNoVerbPrintsUsage(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), nil, strings.NewReader(""), &stdout, &stderr); err != nil {
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
	err := run(context.Background(), []string{"frobnicate"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("run returned no error for an unknown verb")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("error %q does not name the verb", err)
	}
}

func TestRunRejectsAnUnknownFlag(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"--nope"}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("run returned no error for an unknown flag")
	}
}

func TestRunHelpIsNotAnError(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"--help"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run --help: %v", err)
	}
	if !strings.Contains(stderr.String(), usageText) {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestRunDispatchesUnpair(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{"unpair"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("run dispatched unpair without surfacing the missing device")
	}
}

func TestArgAt(t *testing.T) {
	positional := []string{"unpair", "a0-ab-51-33-b7-12"}
	if got := argAt(positional, 1); got != "a0-ab-51-33-b7-12" {
		t.Fatalf("argAt(1) = %q, want the address", got)
	}
	if got := argAt(positional, 2); got != "" {
		t.Fatalf("argAt(2) = %q, want an empty string", got)
	}
}

func TestPick(t *testing.T) {
	if got := pick("flag", "positional"); got != "flag" {
		t.Fatalf("pick with a flag = %q, want flag", got)
	}
	if got := pick("", "positional"); got != "positional" {
		t.Fatalf("pick with no flag = %q, want positional", got)
	}
}
