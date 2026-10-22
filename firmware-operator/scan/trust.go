package main

// A FirmwareCatalog that the LVFS did not sign names its signer's
// certificate with spec.trust, and the operator mounts that ConfigMap in
// the pod at trustDir (catalogtrust.go in the operator). fwupd refreshes
// only from a catalog whose jcat it verifies against a key in its pki
// directory for metadata, so installTrust copies each certificate there,
// beside the LVFS's keys, before the refresh.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// trustDir is where a pod mounts its catalog's ConfigMap.
	trustDir = "/run/catalog-trust"
	// pkiDir is the directory of keys that fwupd trusts for metadata.
	pkiDir = "/etc/pki/fwupd-metadata"
)

// installTrust copies each key of the mounted ConfigMap into pki under
// a name that starts with the catalog's, such as
// catalog-lab-signer.pem, so it never replaces one of the LVFS's keys.
// fwupd reads a certificate by its name's extension, so the key's own
// name keeps it. The kubelet keeps its bookkeeping in entries whose
// names start with "..", which are skipped. A pod with no trust has no
// directory, and nothing changes.
func installTrust(trust, pki, catalogName string) error {
	entries, err := os.ReadDir(trust)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "..") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(trust, e.Name()))
		if err != nil {
			return err
		}
		name := fmt.Sprintf("catalog-%s-%s", catalogName, e.Name())
		if err := os.WriteFile(filepath.Join(pki, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
