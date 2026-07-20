package image

// Tests for the newc reader, and for what the stick builder asks of
// it: the machine names a deployment layer carries.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liken-sh/liken/identity"
)

func TestReadCPIOReturnsWhatFollowsTheTrailer(t *testing.T) {
	var buf bytes.Buffer
	w := newArchive(&buf)
	if err := w.file("hello", []byte("first archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	first := buf.Len()
	// This appends a second archive, the way images concatenate them.
	w = newArchive(&buf)
	if err := w.file("world", []byte("second archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}

	entries, rest, err := readCPIO(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].name != "hello" {
		t.Errorf("the first archive's entries: %+v", entries)
	}
	if len(rest) != buf.Len()-first {
		t.Errorf("rest is %d bytes, want the whole second archive (%d)", len(rest), buf.Len()-first)
	}
	if second, _, err := readCPIO(rest); err != nil || len(second) != 1 || second[0].name != "world" {
		t.Errorf("the rest parses as the next archive: %+v, %v", second, err)
	}
}

func TestReadCPIORefusesDamage(t *testing.T) {
	var buf bytes.Buffer
	w := newArchive(&buf)
	if err := w.file("hello", []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	whole := buf.Bytes()

	cases := []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"garbage", []byte("not a cpio archive at all, sorry")},
		{"truncated mid-header", whole[:50]},
		{"truncated mid-file", whole[:120]},
		{"missing trailer", whole[:len(whole)-64]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := readCPIO(c.raw); err == nil {
				t.Error("damage must be an error, never a partial result")
			}
		})
	}
}

// mintedLayer builds a real deployment layer that carries the named
// machines, through the same Layer call the build uses.
func mintedLayer(t *testing.T, machines ...string) []byte {
	t.Helper()
	manifests := t.TempDir()
	if err := os.MkdirAll(filepath.Join(manifests, "machines"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range machines {
		doc := "apiVersion: liken.sh/v1alpha1\nkind: Machine\nmetadata:\n  name: " + name + "\n"
		if err := os.WriteFile(filepath.Join(manifests, "machines", name+".yaml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	identityDir := t.TempDir()
	if err := identity.Mint(identityDir, io.Discard); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "deployment.cpio")
	if err := Layer(manifests, identityDir, "unused", out, io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMachineNamesComeFromTheLayer(t *testing.T) {
	names, err := machineNames(mintedLayer(t, "node-2", "node-1", "big"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"big", "node-1", "node-2"}) {
		t.Errorf("sorted machine names: %v", names)
	}
}

func TestMachineNamesRefusesAnEmptyDeployment(t *testing.T) {
	// This is a layer packed from a deployment with no machine
	// manifests, identity only. There is nobody to install.
	if _, err := machineNames(mintedLayer(t)); err == nil {
		t.Error("a layer with no machines must be refused")
	}
}

func TestMachineNamesRefusesGarbage(t *testing.T) {
	if _, err := machineNames([]byte("not an archive")); err == nil {
		t.Error("garbage must be an error")
	}
}
