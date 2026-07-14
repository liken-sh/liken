package main

// Tests for the GRUB environment block codec. The golden file in
// testdata was produced by grub-editenv itself (create, then set
// default_slot=A try_slot=B), pinning the format this codec must
// agree with: what grub-editenv writes, parseGRUBEnv must read, and
// what renderGRUBEnv writes, GRUB must read.

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
		t.Fatal(err)
	}
	vars, err := parseGRUBEnv(raw)
	if err != nil {
		t.Fatal(err)
	}
	if vars["default_slot"] != "A" || vars["try_slot"] != "B" {
		t.Errorf("parsed %v, want default_slot=A try_slot=B", vars)
	}
	if len(vars) != 2 {
		t.Errorf("parsed %d variables, want 2 (comments are not variables)", len(vars))
	}
}

func TestGRUBEnvRoundTrip(t *testing.T) {
	want := map[string]string{"default_slot": "B", "try_slot": ""}
	block, err := renderGRUBEnv(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(block) != grubEnvSize {
		t.Fatalf("rendered %d bytes, want %d", len(block), grubEnvSize)
	}
	got, err := parseGRUBEnv(block)
	if err != nil {
		t.Fatal(err)
	}
	if got["default_slot"] != "B" {
		t.Errorf("default_slot: %q", got["default_slot"])
	}
	if v, ok := got["try_slot"]; !ok || v != "" {
		t.Errorf("an empty value must survive the round trip present-but-empty: %v", got)
	}
}

func TestGRUBEnvRenderIsDeterministic(t *testing.T) {
	vars := map[string]string{"b": "2", "a": "1", "c": "3"}
	first, err := renderGRUBEnv(vars)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderGRUBEnv(map[string]string{"c": "3", "a": "1", "b": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("the same variables must render the same bytes regardless of map order")
	}
	if !bytes.HasPrefix(first, []byte(grubEnvSignature)) {
		t.Error("the signature line must lead the block")
	}
	if first[grubEnvSize-1] != '#' {
		t.Error("the block must be padded with '#' to its full size")
	}
}

func TestGRUBEnvRejectsMalformedBlocks(t *testing.T) {
	if _, err := parseGRUBEnv(make([]byte, 512)); err == nil {
		t.Error("a short block must be rejected")
	}
	block := bytes.Repeat([]byte{'#'}, grubEnvSize)
	if _, err := parseGRUBEnv(block); err == nil {
		t.Error("a block without the signature must be rejected")
	}
	signed, err := renderGRUBEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	copy(signed[len(grubEnvSignature):], "not a variable\n")
	if _, err := parseGRUBEnv(signed); err == nil {
		t.Error("a line that is neither comment nor name=value must be rejected")
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
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestGRUBEnvUpdatePreservesOtherVariables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grubenv")
	block, err := renderGRUBEnv(map[string]string{"default_slot": "A", "try_slot": "B"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, block, 0o644); err != nil {
		t.Fatal(err)
	}

	// The one-shot consumed: try_slot cleared, default_slot untouched.
	if err := updateGRUBEnv(path, map[string]string{"try_slot": ""}); err != nil {
		t.Fatal(err)
	}
	vars, err := readGRUBEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if vars["default_slot"] != "A" {
		t.Errorf("default_slot must survive an unrelated update: %v", vars)
	}
	if v, ok := vars["try_slot"]; !ok || v != "" {
		t.Errorf("try_slot should read present-but-empty: %v", vars)
	}
}

func TestGRUBEnvUpdateReportsAMissingBlock(t *testing.T) {
	err := updateGRUBEnv(filepath.Join(t.TempDir(), "grubenv"), map[string]string{"a": "1"})
	if err == nil {
		t.Error("updating a block that was never installed must fail, not invent one")
	}
}
