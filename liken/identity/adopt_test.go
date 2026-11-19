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
		t.Fatalf("adopting the harvest: %v", err)
	}
	for _, f := range Bundle {
		want, err := os.ReadFile(filepath.Join(harvest, f))
		if err != nil {
			t.Fatalf("reading the harvested file: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("reading the adopted file: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("adopted %s differs from the harvested file, want the same bytes", f)
		}
	}
}

func TestAdoptedFilesArePrivate(t *testing.T) {
	harvest := mintedIdentity(t)
	dir := t.TempDir()
	if err := Adopt(harvest, dir, io.Discard); err != nil {
		t.Fatalf("adopting the harvest: %v", err)
	}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %v, want no access for group or others", path, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the adopted files: %v", err)
	}
}

func TestAdoptRefusesAPartialHarvest(t *testing.T) {
	harvest := mintedIdentity(t)
	if err := os.Remove(filepath.Join(harvest, "tls", "client-ca.key")); err != nil {
		t.Fatalf("removing tls/client-ca.key from the harvest: %v", err)
	}
	dir := t.TempDir()

	err := Adopt(harvest, dir, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "tls/client-ca.key") {
		t.Errorf("Adopt of a harvest without tls/client-ca.key returned %v, want an error that names tls/client-ca.key", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("reading the identity directory: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("after a refused adoption the directory has %d entries, want none", len(entries))
	}
}

func TestAdoptRefusesATokenFromAnotherCluster(t *testing.T) {
	harvest := mintedIdentity(t)
	other := mintedIdentity(t)
	stray, err := os.ReadFile(filepath.Join(other, "token"))
	if err != nil {
		t.Fatalf("reading the token of the other identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(harvest, "token"), stray, 0o600); err != nil {
		t.Fatalf("writing the token to the harvest: %v", err)
	}

	err = Adopt(harvest, t.TempDir(), io.Discard)

	if err == nil || !strings.Contains(err.Error(), "different clusters") {
		t.Errorf("Adopt with a token from another cluster returned %v, want an error about different clusters", err)
	}
}

func TestAdoptAcceptsATokenInAnotherFormat(t *testing.T) {
	harvest := mintedIdentity(t)
	if err := os.WriteFile(filepath.Join(harvest, "token"), []byte("a-plain-secret\n"), 0o600); err != nil {
		t.Fatalf("writing the token to the harvest: %v", err)
	}
	if err := Adopt(harvest, t.TempDir(), io.Discard); err != nil {
		t.Errorf("Adopt with a token that is not in K10 format returned %v, want nil because the CA check applies only to K10 tokens", err)
	}
}

func TestAdoptRefusesToOverlayAnExistingIdentity(t *testing.T) {
	harvest := mintedIdentity(t)
	dir := mintedIdentity(t)

	err := Adopt(harvest, dir, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Adopt over an existing identity returned %v, want an error that the files already exist", err)
	}
}
