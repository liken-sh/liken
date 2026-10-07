package main

// The settings that come from the radio's Adapter, written into the
// pod's settings volume before bluetoothd and the btmon container
// start.
//
// The privacy file holds spec.privacy, which start-bluetoothd writes
// into main.conf (privacy.go). The btmon file holds spec.btmon, true
// or false, which start-btmon reads to decide whether btmon runs. The
// file is in place before the btmon container starts, so a radio with
// the trace on traces from the start of bluetoothd, when bluetoothd
// tells the kernel which bonded devices may reconnect. The operator
// writes the btmon file again when spec.btmon changes. It never writes
// the privacy file, which records the value bluetoothd started with.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/liken-sh/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

const (
	// defaultSettings is the pod's settings volume. start-bluetoothd
	// reads the privacy file in it, start-btmon reads the btmon file,
	// and the operator reads both and writes the btmon file.
	// settingsVar overrides the directory, the same way rootVar
	// overrides the bonds tree.
	settingsVar     = "BLUETOOTH_SETTINGS_ROOT"
	defaultSettings = "/var/run/bluetooth.liken.sh/settings"

	// privacyFile holds the value of the Adapter's spec.privacy, and
	// btmonFile holds the value of its spec.btmon.
	privacyFile = "privacy"
	btmonFile   = "btmon"

	// adaptersPath is the collection of the operator's Adapters. An
	// Adapter is cluster-scoped and named for its radio's address.
	adaptersPath = "/apis/bluetooth.liken.sh/v1alpha1/adapters/"
)

// adapterSpec is the part of an Adapter that this program reads. The
// API server's schema refuses any privacy value outside BlueZ's five,
// and start-bluetoothd refuses one too, so this program passes the
// value on as it reads it.
type adapterSpec struct {
	Spec struct {
		Privacy string `json:"privacy,omitempty"`
		Btmon   bool   `json:"btmon,omitempty"`
	} `json:"spec"`
}

// writeSettings reads the radio's Adapter once, writes its spec.privacy
// and spec.btmon into the settings volume, and answers the privacy
// value it wrote.
//
// An Adapter that does not exist yet has privacy off and the trace
// off, because the operator creates it on its first pass, after
// bluetoothd has started. Any other failure to read it is an error,
// the same as a failure to read the bonds, so the pod stays in Init
// and bluetoothd does not start with a value that a person did not
// choose.
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

// writeSetting writes one value into the settings volume, on a line
// of its own.
func writeSetting(settings, name, value string) error {
	path := filepath.Join(settings, name)
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
