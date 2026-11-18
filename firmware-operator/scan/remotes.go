package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// enabledLine matches the Enabled=true line of a fwupd remote, with any
// spacing around the equals sign.
var enabledLine = regexp.MustCompile(`(?m)^(\s*Enabled\s*=\s*)true[ \t]*$`)

// remoteID is the fwupd remote of a FirmwareCatalog. fwupd reports it as
// RemoteId in each release.
func remoteID(catalogName string) string {
	return "catalog-" + catalogName
}

// configureRemotes makes the FirmwareCatalog fwupd's only source of
// releases. It sets Enabled=false in each remote that the fwupd package
// ships, such as the LVFS and the vendor directories, and writes a
// remote for the catalog. It changes nothing else in fwupd's
// configuration, because fwupd hides real updates when plugins are
// disabled there.
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
