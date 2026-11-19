package machine

// This file reads and writes the record of the last boot that init
// refused.
//
// When init refuses to boot (failBoot in init/main.go), it prints the
// reason and powers the machine off. k3s has not started, so no log
// relay has sent the console output anywhere, and the reason is lost
// unless someone watched the serial port. So init writes the reason to
// a file on machineState before the power-off, and the next boot
// prints it and publishes it as status.lastFailStop.
//
// Nothing clears the record. Each refusal overwrites the previous one,
// as status.lastCrash keeps the last panic.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// failStopRecord is the record's file name at the machineState root.
const failStopRecord = "failstop.yaml"

// FailStop is one refused boot. The same type is the file on disk and
// the status field, so both have the text that the console showed.
type FailStop struct {
	// Reason is the error that failBoot printed to the console.
	Reason string `json:"reason"`

	// Time is the machine's clock at the refusal. The boot stops before
	// the clock synchronizes, so the time comes from the hardware clock.
	Time time.Time `json:"time"`
}

// WriteFailStop records a refusal under the machineState root. It
// writes through WriteDurable because the machine powers off right
// after, and the record must be on the disk by then.
func WriteFailStop(root string, f FailStop) error {
	raw, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return WriteDurable(filepath.Join(root, failStopRecord), raw)
}

// ReadFailStop returns the record under the machineState root, or nil
// and no error when there is none.
func ReadFailStop(root string) (*FailStop, error) {
	raw, err := os.ReadFile(filepath.Join(root, failStopRecord))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := &FailStop{}
	if err := yaml.UnmarshalStrict(raw, f); err != nil {
		return nil, err
	}
	// An empty file unmarshals without an error into a refusal with no
	// reason, dated to the year one. WriteDurable should prevent an empty
	// file, but a disk can acknowledge a flush that it did not perform.
	if f.Reason == "" {
		return nil, nil
	}
	return f, nil
}
