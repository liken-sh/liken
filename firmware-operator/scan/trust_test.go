package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// configMapVolume lays out a directory the way the kubelet mounts a
// ConfigMap: the data in a dated directory behind the ..data link, and
// one link for each key.
func configMapVolume(t *testing.T, keys map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	dated := filepath.Join(dir, "..2026_10_22_20_00_00.000000001")
	if err := os.Mkdir(dated, 0o755); err != nil {
		t.Fatal(err)
	}
	for key, value := range keys {
		if err := os.WriteFile(filepath.Join(dated, key), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Base(dated), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	for key := range keys {
		if err := os.Symlink(filepath.Join("..data", key), filepath.Join(dir, key)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fwupd verifies the catalog's jcat against the keys in its pki
// directory when it refreshes, so the catalog's certificate must be
// there first, beside the LVFS's.
func TestTheCatalogsTrustIsInstalledBeforeTheRefresh(t *testing.T) {
	r := newRig(t)
	r.trustDir = configMapVolume(t, map[string]string{"signer.pem": "-----BEGIN CERTIFICATE-----\nlab\n"})
	lvfs := filepath.Join(r.pkiDir, "LVFS-CA.pem")
	if err := os.WriteFile(lvfs, []byte("lvfs"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(r.pkiDir, "catalog-lvfs-signer.pem")
	var atRefresh []byte
	r.tool.effects = map[string]func(){"refresh": func() { atRefresh, _ = os.ReadFile(installed) }}

	got := r.scan(context.Background())

	if got.Result != resultScanned {
		t.Fatalf("got %+v, want Scanned", got)
	}
	if string(atRefresh) != "-----BEGIN CERTIFICATE-----\nlab\n" {
		t.Errorf("at the refresh %s holds %q, want the ConfigMap's certificate", installed, atRefresh)
	}
	if held, _ := os.ReadFile(lvfs); string(held) != "lvfs" {
		t.Errorf("the LVFS's key reads %q, want it kept", held)
	}
	entries, _ := os.ReadDir(r.pkiDir)
	if len(entries) != 2 {
		t.Errorf("the pki directory holds %d files, want the LVFS's key and the one certificate, not the ConfigMap's bookkeeping", len(entries))
	}
}

func TestACatalogWithNoTrustChangesNoKeys(t *testing.T) {
	r := newRig(t)

	got := r.scan(context.Background())

	entries, _ := os.ReadDir(r.pkiDir)
	if got.Result != resultScanned || len(entries) != 0 {
		t.Errorf("got %+v and %d keys, want Scanned with no key installed", got, len(entries))
	}
}
