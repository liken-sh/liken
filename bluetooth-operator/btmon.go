package main

// The pod's btmon container writes a trace of the radio's HCI traffic
// and management channel to its log, so the trace is still readable
// after the pod is replaced. btmon prints key material in plain text, so
// the trace stays off until a person sets the Adapter's spec.btmon.
//
// start-btmon (bluetoothd/btmon) watches the pod's settings directory
// and runs btmon while the btmon file there contains true. bondfetch
// writes the file before the container starts, and a pass writes it
// again when spec.btmon differs from it. So turning the trace on or off
// restarts neither bluetoothd nor any controller's link.
//
// The operator does not write the privacy file in the same directory.
// That file records the value bluetoothd started with, and a new value
// needs a new pod (privacy.go).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// btmonFile is the file in the settings volume that contains true or
// false.
const btmonFile = "btmon"

// readBtmon returns the value in the btmon file. A missing file reads as
// false, as it does for start-btmon.
func readBtmon(settings string) (bool, error) {
	contents, err := os.ReadFile(filepath.Join(settings, btmonFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(contents)) == "true", nil
}

// writeBtmon writes value to a new file and renames it over the btmon
// file. start-btmon reads the file on each change in the directory, and
// the rename makes it see the whole value at once, never a partly
// written file.
func writeBtmon(settings string, value bool) error {
	path := filepath.Join(settings, btmonFile)
	next := filepath.Join(settings, "."+btmonFile+".new")
	if err := os.WriteFile(next, []byte(strconv.FormatBool(value)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(next, path)
}

// applyBtmon writes the Adapter's spec.btmon into the btmon file when
// they differ, posts a BtmonChanged Event for each write, and records
// the file's value for status.btmon. It records nil when it cannot read
// the file, and keeps the old value when the write fails, so reconcile
// marks the pass failed in both cases.
func (i *inventory) applyBtmon(adapter *Adapter) {
	current, err := readBtmon(i.settings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the btmon file: %v\n", err)
		i.btmon = nil
		return
	}
	i.btmon = &current
	wanted := adapter.Spec.Btmon
	if wanted == current {
		return
	}
	if err := writeBtmon(i.settings, wanted); err != nil {
		fmt.Fprintf(os.Stderr, "writing %t to the btmon file: %v\n", wanted, err)
		return
	}
	i.btmon = &wanted
	message := "spec.btmon is now false, so the btmon container stops its trace"
	if wanted {
		message = "spec.btmon is now true, so the btmon container traces this radio, and its log contains key material in plain text"
	}
	i.recorder.Normal(adapterReference(adapter), reasonBtmonChanged, message)
	fmt.Printf("btmon: %s %s\n", adapter.Metadata.Name, message)
}

// reportedBtmon returns the value in the btmon file after this pass, or
// the value already in status.btmon when the pass could not read the
// file.
func (i *inventory) reportedBtmon(adapter *Adapter) *bool {
	if i.btmon == nil {
		return adapter.Status.Btmon
	}
	value := *i.btmon
	return &value
}
