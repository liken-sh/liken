package main

// The btmon trace of the radio this pod holds.
//
// The pod's btmon container traces the radio's HCI link and the
// management channel into its log, which keeps the evidence of a
// stall after a roll of the pod. btmon prints key material in plain
// text, so the trace is off until a person sets the Adapter's
// spec.btmon.
//
// start-btmon in that container watches the btmon file in the pod's
// settings volume, and runs btmon while the file says true
// (bluetoothd/btmon). bondfetch writes the file before the container
// starts. On each pass, the operator compares spec.btmon with the file
// and writes the file when the two differ, so a change reaches the
// container in seconds, and no controller disconnects.
//
// The operator writes only the btmon file. The privacy file in the
// same volume records the value bluetoothd started with, and a new
// value there takes a new pod (privacy.go).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// btmonFile is the file in the settings volume that holds the trace
// setting, true or false.
const btmonFile = "btmon"

// readBtmon answers the value in the btmon file. A missing file is
// false, the same as start-btmon reads it.
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

// writeBtmon writes value into the btmon file.
//
// It writes a new file and renames it over the old one. start-btmon
// reads the file on each change in the directory, and a rename shows
// it the whole value at once, never a file that is half written.
func writeBtmon(settings string, value bool) error {
	path := filepath.Join(settings, btmonFile)
	next := filepath.Join(settings, "."+btmonFile+".new")
	if err := os.WriteFile(next, []byte(strconv.FormatBool(value)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(next, path)
}

// applyBtmon writes the Adapter's spec.btmon into the btmon file when
// they differ, posts one BtmonChanged Event for each write, and records
// the file's value for status.btmon.
func (i *inventory) applyBtmon(adapter *Adapter) {
	current, err := readBtmon(i.settings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the btmon setting: %v\n", err)
		i.btmon = nil
		return
	}
	i.btmon = &current
	wanted := adapter.Spec.Btmon
	if wanted == current {
		return
	}
	if err := writeBtmon(i.settings, wanted); err != nil {
		fmt.Fprintf(os.Stderr, "writing the btmon setting %t: %v\n", wanted, err)
		return
	}
	i.btmon = &wanted
	message := "btmon changes to false; the btmon container stops its trace"
	if wanted {
		message = "btmon changes to true; the btmon container traces this radio, and its log holds key material in plain text"
	}
	i.recorder.Normal(adapterReference(adapter), reasonBtmonChanged, message)
	fmt.Printf("btmon: %s %s\n", adapter.Metadata.Name, message)
}

// reportedBtmon answers the value for status.btmon: the value in the
// btmon file after this pass, or the value the status already has when
// the pass could not read the file.
func (i *inventory) reportedBtmon(adapter *Adapter) *bool {
	if i.btmon == nil {
		return adapter.Status.Btmon
	}
	value := *i.btmon
	return &value
}
