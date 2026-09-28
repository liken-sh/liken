package machine

// The record a machine leaves behind when it refuses to boot.
//
// Init stops a boot for two failures, identity and storage
// (init/main.go's failBoot states both). The stop prints its reason
// and powers the machine off. The power-off is what makes that reason
// hard to keep: it ends the boot long before k3s starts, so no log
// relay carries the console anywhere, and a powered-off machine has no
// console to read. A person who was not sitting at the serial port at
// that moment has nothing to go on.
//
// So the reason goes into one small file on machineState before the
// power-off. The next boot reads the file, prints it, and publishes it
// as status.lastFailStop. The question "why did this machine refuse"
// then takes one kubectl get, rather than a source read of init.
//
// The record is never cleared. It answers one question, the last time
// this machine refused to boot and why, and that answer stays true
// until the next refusal replaces it. A machine that boots cleanly for
// months still reports the refusal it had before that, the same way
// status.lastCrash keeps reporting an old panic.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// failStopRecord is the record's file name under the machineState
// root. It sits at the root rather than in a directory of its own,
// because there is only ever one of these: each refusal replaces the
// last.
const failStopRecord = "failstop.yaml"

// FailStop is one refused boot: what init would not run with, and
// when. One type serves as both the file's schema and the status
// field, so the console message, the record on disk, and the cluster's
// status all carry the same words.
type FailStop struct {
	// Reason is failBoot's own message, the text the console printed
	// before the machine powered off.
	Reason string `json:"reason"`

	// Time is the machine's clock at the refusal. A boot stops well
	// before its first clock synchronization, so this is the hardware
	// clock's reading, reported as recorded.
	Time time.Time `json:"time"`
}

// WriteFailStop records a refusal under the machineState root. The
// write is durable and not merely atomic, in the same way as every
// other machineState write (staging.go explains the four steps). The
// whole purpose of this record is to survive the power-off that
// follows it, and a rename that never reached the disk would leave
// nothing behind.
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

// ReadFailStop reads the standing record. A root with no record is the
// ordinary case, a machine that has never refused a boot, so it
// returns nothing and no error.
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
	// A record with no reason is not a refusal. An empty file
	// unmarshals into an empty struct and reports no error, so without
	// this test a truncated record would read back as a refusal that
	// happened in the year one and gave no reason. The write is durable
	// precisely so this does not happen, and a machine can still lose
	// the file's contents to hardware that acknowledges a flush it did
	// not make. Nothing beats no news here, because the field's whole
	// job is to say something a person can act on.
	if f.Reason == "" {
		return nil, nil
	}
	return f, nil
}
