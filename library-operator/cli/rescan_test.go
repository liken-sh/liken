package main

import (
	"io"
	"strings"
	"testing"
)

func TestRunRescanNeedsALibrary(t *testing.T) {
	if err := runRescan(nil, rescanOptions{}, io.Discard); err == nil {
		t.Fatal("runRescan returned no error with no library")
	}
}

func TestRunRescanIsNotYetImplemented(t *testing.T) {
	err := runRescan(nil, rescanOptions{Name: "movies"}, io.Discard)
	if err == nil {
		t.Fatal("runRescan returned no error")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Fatalf("error %q does not say the verb is a stub", err)
	}
}
