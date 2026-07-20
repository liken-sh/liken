package identity

// This file tests adoption: placing an existing cluster's harvested
// identity into a deployment directory. Most of the interesting
// behavior is refusal. A partial harvest, a token from a different
// cluster, and a deployment that already holds an identity each
// produce a distinct error before anything is written. The
// successful case is a faithful copy. A minted identity also serves
// as the harvest fixture, because mint produces exactly the layout
// that a harvest carries.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdoptRoundTripsAnIdentity(t *testing.T) {
	harvest := mintedIdentity(t)
	dir := t.TempDir()
	if err := Adopt(harvest, dir, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, f := range Bundle {
		want, err := os.ReadFile(filepath.Join(harvest, f))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: adopted bytes differ from the harvest", f)
		}
	}
}

func TestAdoptedFilesArePrivate(t *testing.T) {
	harvest := mintedIdentity(t)
	dir := t.TempDir()
	if err := Adopt(harvest, dir, io.Discard); err != nil {
		t.Fatal(err)
	}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s: mode %v is readable beyond the owner", path, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAdoptRefusesAPartialHarvest(t *testing.T) {
	harvest := mintedIdentity(t)
	if err := os.Remove(filepath.Join(harvest, "tls", "client-ca.key")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	err := Adopt(harvest, dir, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "tls/client-ca.key") {
		t.Errorf("error does not name the missing file: %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Error("a refused adoption still wrote files")
	}
}

func TestAdoptRefusesATokenFromAnotherCluster(t *testing.T) {
	harvest := mintedIdentity(t)
	other := mintedIdentity(t)
	stray, err := os.ReadFile(filepath.Join(other, "token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(harvest, "token"), stray, 0o600); err != nil {
		t.Fatal(err)
	}

	err = Adopt(harvest, t.TempDir(), io.Discard)

	if err == nil || !strings.Contains(err.Error(), "different clusters") {
		t.Errorf("mismatched token was not refused: %v", err)
	}
}

func TestAdoptAcceptsATokenInAnotherFormat(t *testing.T) {
	harvest := mintedIdentity(t)
	if err := os.WriteFile(filepath.Join(harvest, "token"), []byte("a-plain-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Adopt(harvest, t.TempDir(), io.Discard); err != nil {
		t.Errorf("a non-K10 token should skip the CA cross-check: %v", err)
	}
}

func TestAdoptRefusesToOverlayAnExistingIdentity(t *testing.T) {
	harvest := mintedIdentity(t)
	dir := mintedIdentity(t)

	err := Adopt(harvest, dir, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "already holds an identity") {
		t.Errorf("existing identity was not protected: %v", err)
	}
}
