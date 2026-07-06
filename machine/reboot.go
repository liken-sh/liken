package machine

// The reboot channel: how the operator asks init to reboot.
//
// Only PID 1 can shut a machine down properly, so a reboot is a
// request, not an act: the operator writes an intent file, and init
// (which polls for it) stops k3s cleanly and reboots. The channel is
// a directory of its own under /run/liken because the two programs'
// mounts enforce the two directions: facts flow init → operator
// through a read-only mount, intents flow operator → init through
// this one. /run is a fresh tmpfs every boot, so an intent can never
// survive the reboot it asked for and fire twice.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// OperatorRunDir is the operator's writable channel to init; init
// creates it (with the rest of /run/liken) before starting k3s.
const OperatorRunDir = "/run/liken/operator"

const rebootIntentFile = "reboot-intent.yaml"

// A RebootIntent says why the machine should reboot and which staged
// manifest the reboot is meant to apply; the content only enriches
// the console narration, since the file's presence is the trigger.
type RebootIntent struct {
	Reason       string    `json:"reason"`
	ManifestHash string    `json:"manifestHash,omitempty"`
	RequestedAt  time.Time `json:"requestedAt"`
}

// WriteRebootIntent asks for a reboot, atomically (temp file and
// rename, like the facts): init polling mid-write must see a whole
// intent or none. No fsync; tmpfs has nothing to sync to.
func WriteRebootIntent(dir string, intent *RebootIntent) error {
	raw, err := yaml.Marshal(intent)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".intent-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, rebootIntentFile))
}

// ReadRebootIntent reports the pending intent, or nil when no reboot
// has been requested.
func ReadRebootIntent(dir string) (*RebootIntent, error) {
	raw, err := os.ReadFile(filepath.Join(dir, rebootIntentFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	intent := &RebootIntent{}
	if err := yaml.UnmarshalStrict(raw, intent); err != nil {
		return nil, err
	}
	return intent, nil
}
