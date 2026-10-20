package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// enabledLine matches a remote's Enabled=true line in fwupd's key file
// format, with any spacing around the equals sign.
var enabledLine = regexp.MustCompile(`(?m)^(\s*Enabled\s*=\s*)true[ \t]*$`)

// remoteID is the name fwupd gives the remote of one FirmwareCatalog. It shows
// in each release as RemoteId, and the refresh command names it.
func remoteID(catalogName string) string {
	return "catalog-" + catalogName
}

// configureRemotes makes the FirmwareCatalog the only source of releases. The
// packages ship remotes for the LVFS and for vendor directories, and a
// release from any of them would be one that no FirmwareCatalog vouches for.
// The scan changes only the remote files, because fwupd hides real
// updates when the configuration disables plugins.
func configureRemotes(dir, catalogName, catalogURL string) error {
	existing, err := filepath.Glob(filepath.Join(dir, "*.conf"))
	if err != nil {
		return err
	}
	for _, file := range existing {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		disabled := enabledLine.ReplaceAll(data, []byte("${1}false"))
		if string(disabled) == string(data) {
			continue
		}
		if err := os.WriteFile(file, disabled, 0o644); err != nil {
			return err
		}
	}
	remote := fmt.Sprintf("[fwupd Remote]\nEnabled=true\nTitle=%s\nMetadataURI=%s\n", catalogName, catalogURL)
	return os.WriteFile(filepath.Join(dir, remoteID(catalogName)+".conf"), []byte(remote), 0o644)
}
