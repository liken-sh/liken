package main

// bondfetch copies two settings from the radio's Adapter into the pod's
// settings volume, before bluetoothd and the btmon container start.
//
// The privacy file has spec.privacy, which start-bluetoothd writes into
// main.conf (privacy.go). The btmon file has spec.btmon, "true" or
// "false", and start-btmon runs btmon while it is true. bondfetch
// writes both files before the btmon container starts, so a trace that
// is on records bluetoothd from its start, including the step where
// bluetoothd tells the kernel which bonded devices may reconnect.
// The operator writes the btmon file again when spec.btmon changes
// (btmon.go). It never writes the privacy file, which records the value
// that bluetoothd started with.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const (
	// defaultSettings is the path of the pod's settings volume, and
	// settingsVar overrides it, the way rootVar overrides the bonds
	// tree.
	settingsVar     = "BLUETOOTH_SETTINGS_ROOT"
	defaultSettings = "/var/run/bluetooth.liken.sh/settings"

	privacyFile = "privacy"
	btmonFile   = "btmon"

	// adaptersPath is the API path of the cluster-scoped Adapters. An
	// Adapter's name is its radio's address with dashes (Address.Key).
	adaptersPath = "/apis/bluetooth.liken.sh/v1alpha1/adapters/"
)

// adapterSpec is the part of an Adapter that bondfetch reads. It does
// not validate privacy, because the API server's schema accepts only
// BlueZ's values and start-bluetoothd refuses any other.
type adapterSpec struct {
	Spec struct {
		Privacy string `json:"privacy,omitempty"`
		Btmon   bool   `json:"btmon,omitempty"`
	} `json:"spec"`
}

// writeSettings writes the Adapter's spec.privacy and spec.btmon into
// the settings volume, and returns the privacy value it wrote.
//
// When the Adapter does not exist, both settings are off, because the
// operator creates the Adapter on its first pass, after bluetoothd has
// started. Any other read failure returns an error, as it does for the
// bonds, so the init container fails and bluetoothd does not start with
// a value that nobody chose.
func writeSettings(api *apiclient.Client, adapter bonds.Address, settings string) (string, error) {
	privacy, btmon := privacyOff, false
	object, err := apiclient.Get[adapterSpec](api, adaptersPath+adapter.Key())
	switch {
	case err == nil:
		if object.Spec.Privacy != "" {
			privacy = object.Spec.Privacy
		}
		btmon = object.Spec.Btmon
	case errors.Is(err, apiclient.ErrNotFound):
	default:
		return "", fmt.Errorf("reading the Adapter for %s: %w", adapter, err)
	}

	if err := writeSetting(settings, privacyFile, privacy); err != nil {
		return "", err
	}
	if err := writeSetting(settings, btmonFile, strconv.FormatBool(btmon)); err != nil {
		return "", err
	}
	fmt.Printf("bondfetch: privacy is %s and btmon is %t for %s\n", privacy, btmon, adapter)
	return privacy, nil
}

func writeSetting(settings, name, value string) error {
	path := filepath.Join(settings, name)
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
