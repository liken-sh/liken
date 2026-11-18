package main

// fwupd refreshes only from a catalog whose jcat it can verify against
// a key in its metadata pki directory. A FirmwareCatalog that the LVFS
// did not sign names a ConfigMap of its signer's certificates in
// spec.trust, and the operator mounts that ConfigMap in the pod at
// trustDir (catalogtrust.go in the operator). So before the refresh,
// installTrust copies each certificate into the pki directory, beside
// the LVFS's keys.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	trustDir = "/run/catalog-trust"
	// pkiDir holds the keys that fwupd trusts to sign metadata.
	pkiDir = "/etc/pki/fwupd-metadata"
)

// installTrust copies each key of the ConfigMap mounted at trust into
// pki, as catalog-<catalogName>-<key>, so that a copy cannot replace
// one of the LVFS's keys. The name ends with the key's own name,
// because fwupd uses the file extension to tell what kind of
// certificate it is. The kubelet adds entries whose names start with
// "..", and installTrust skips them. When trust does not exist, the
// catalog has no trust, and installTrust changes nothing.
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
