package main

// grub-editenv wrote testdata/grubenv-editenv.golden with create, then
// set default_slot=A try_slot=B.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGRUBEnvParsesWhatGRUBEditenvWrites(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "grubenv-editenv.golden"))
	if err != nil {
		t.Fatalf("reading the golden block: %v", err)
	}
	vars, err := parseGRUBEnv(raw)
	if err != nil {
		t.Fatalf("parsing the golden block: %v", err)
	}
	if vars["default_slot"] != "A" || vars["try_slot"] != "B" {
		t.Errorf("parsed %v, want default_slot=A and try_slot=B", vars)
	}
	if len(vars) != 2 {
		t.Errorf("parsed %d variables, want 2, because comments are not variables", len(vars))
	}
}

func TestGRUBEnvRoundTrip(t *testing.T) {
	want := map[string]string{"default_slot": "B", "try_slot": ""}
	block, err := renderGRUBEnv(want)
	if err != nil {
		t.Fatalf("rendering the block: %v", err)
	}
	if len(block) != grubEnvSize {
		t.Fatalf("rendered %d bytes, want %d", len(block), grubEnvSize)
	}
	got, err := parseGRUBEnv(block)
	if err != nil {
		t.Fatalf("parsing the rendered block: %v", err)
	}
	if got["default_slot"] != "B" {
		t.Errorf("default_slot is %q, want B", got["default_slot"])
	}
	if v, ok := got["try_slot"]; !ok || v != "" {
		t.Errorf("parsed %v, want try_slot present with an empty value", got)
	}
}

func TestGRUBEnvRenderIsDeterministic(t *testing.T) {
	vars := map[string]string{"b": "2", "a": "1", "c": "3"}
	first, err := renderGRUBEnv(vars)
	if err != nil {
		t.Fatalf("rendering the first block: %v", err)
	}
	second, err := renderGRUBEnv(map[string]string{"c": "3", "a": "1", "b": "2"})
	if err != nil {
		t.Fatalf("rendering the second block: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("the same variables in two map orders rendered different bytes, want the same bytes")
	}
	if !bytes.HasPrefix(first, []byte(grubEnvSignature)) {
		t.Error("the block does not start with the signature line, want it to")
	}
	if first[grubEnvSize-1] != '#' {
		t.Error("the last byte of the block is not '#', want padding to the full size")
	}
}

func TestGRUBEnvRejectsMalformedBlocks(t *testing.T) {
	if _, err := parseGRUBEnv(make([]byte, 512)); err == nil {
		t.Error("parsing a short block returned no error, want one")
	}
	block := bytes.Repeat([]byte{'#'}, grubEnvSize)
	if _, err := parseGRUBEnv(block); err == nil {
		t.Error("parsing a block without the signature returned no error, want one")
	}
	signed, err := renderGRUBEnv(nil)
	if err != nil {
		t.Fatalf("rendering an empty block: %v", err)
	}
	copy(signed[len(grubEnvSignature):], "not a variable\n")
	if _, err := parseGRUBEnv(signed); err == nil {
		t.Error("parsing a line that is neither a comment nor name=value returned no error, want one")
	}
}

func TestGRUBEnvRejectsUnrepresentableVariables(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"equals in name":   {"a=b": "1"},
		"newline in name":  {"a\nb": "1"},
		"empty name":       {"": "1"},
		"newline in value": {"a": "1\n2"},
		"overflow":         {"a": strings.Repeat("x", grubEnvSize)},
	} {
		if _, err := renderGRUBEnv(vars); err == nil {
			t.Errorf("rendering %s returned no error, want one", name)
		}
	}
}

func TestGRUBEnvUpdatePreservesOtherVariables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grubenv")
	block, err := renderGRUBEnv(map[string]string{"default_slot": "A", "try_slot": "B"})
	if err != nil {
		t.Fatalf("rendering the block: %v", err)
	}
	if err := os.WriteFile(path, block, 0o644); err != nil {
		t.Fatalf("writing the block: %v", err)
	}

	// Clearing try_slot, as grub.cfg does after it starts a trial,
	// must not change default_slot.
	if err := updateGRUBEnv(path, map[string]string{"try_slot": ""}); err != nil {
		t.Fatalf("updating try_slot: %v", err)
	}
	vars, err := readGRUBEnv(path)
	if err != nil {
		t.Fatalf("reading the updated block: %v", err)
	}
	if vars["default_slot"] != "A" {
		t.Errorf("read %v, want default_slot to stay A", vars)
	}
	if v, ok := vars["try_slot"]; !ok || v != "" {
		t.Errorf("read %v, want try_slot present with an empty value", vars)
	}
}

func TestGRUBEnvUpdateReportsAMissingBlock(t *testing.T) {
	err := updateGRUBEnv(filepath.Join(t.TempDir(), "grubenv"), map[string]string{"a": "1"})
	if err == nil {
		t.Error("updating a missing block returned no error, want one")
	}
}
