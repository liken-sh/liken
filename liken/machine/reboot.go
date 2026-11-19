package machine

// machine-operator requests that init reboot, restart k3s, or load
// modules by writing an intent file into OperatorRunDir, which init watches
// with inotify. The operator does not reboot the machine itself,
// because only init, as PID 1, can stop k3s and unmount the disks
// before the reboot.
//
// The presence of an intent file is the request, so init acts on a
// file that it cannot parse. Reason goes into the message that init
// prints on the console. /run is a new tmpfs on every boot, so a
// reboot intent is gone after the reboot that it requested.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// OperatorRunDir is the directory of intent files. Init creates it
// before it starts k3s.
const OperatorRunDir = "/run/liken/operator"

const rebootIntentFile = "reboot-intent.yaml"

// A RebootIntent requests that init reboot the machine. ManifestHash
// identifies the staged manifest that the reboot applies.
//
// KeepBootNext names the Disruption whose spec.keepBootNext asked init
// to keep the UEFI BootNext variable that its requester set, such as
// firmware-operator for a capsule update. init/handover.go decides
// whether it is safe to keep it.
type RebootIntent struct {
	Reason       string    `json:"reason"`
	ManifestHash string    `json:"manifestHash,omitempty"`
	KeepBootNext string    `json:"keepBootNext,omitempty"`
	RequestedAt  time.Time `json:"requestedAt"`
}

// writeIntent writes one intent file with writeAtomic, so init reads
// a whole file or no file. It does not create the directory, because a
// write before init created it is a bug.
func writeIntent(dir, name string, intent any) error {
	raw, err := yaml.Marshal(intent)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, name), raw)
}

// readIntent reads one intent file into out and reports whether the
// file exists.
func readIntent(dir, name string, out any) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := yaml.UnmarshalStrict(raw, out); err != nil {
		return false, err
	}
	return true, nil
}

func WriteRebootIntent(dir string, intent *RebootIntent) error {
	return writeIntent(dir, rebootIntentFile, intent)
}

// ReadRebootIntent returns the pending reboot intent, or nil when
// there is none.
func ReadRebootIntent(dir string) (*RebootIntent, error) {
	intent := &RebootIntent{}
	present, err := readIntent(dir, rebootIntentFile, intent)
	if !present || err != nil {
		return nil, err
	}
	return intent, nil
}

const restartIntentFile = "restart-intent.yaml"

// A RestartIntent requests that init restart k3s without a reboot, for the
// changes that k3s reads only at startup. cluster/changes.go lists
// them.
//
// A restart request is its own file and not a field of RebootIntent,
// because init reboots when it cannot parse a reboot intent. An older
// init would fail to parse the new field and reboot the machine. It
// does not look for restart-intent.yaml, so it ignores the file.
//
// Init deletes a restart intent, because the machine keeps running and
// the next scan would restart k3s again. Init reads what to apply from
// the staged stores on machineState and not from the intent, so a
// duplicate intent is harmless.
type RestartIntent struct {
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requestedAt"`
}

func WriteRestartIntent(dir string, intent *RestartIntent) error {
	return writeIntent(dir, restartIntentFile, intent)
}

// ReadRestartIntent returns the pending restart intent, or nil when
// there is none.
func ReadRestartIntent(dir string) (*RestartIntent, error) {
	intent := &RestartIntent{}
	present, err := readIntent(dir, restartIntentFile, intent)
	if !present || err != nil {
		return nil, err
	}
	return intent, nil
}

// ClearRestartIntent deletes the restart intent file. It succeeds when
// the file does not exist.
//
// Init clears the file before it restarts k3s, so an interruption
// between the two steps loses the request instead of repeating it. The
// operator's next pass writes a lost request again.
func ClearRestartIntent(dir string) error {
	err := os.Remove(filepath.Join(dir, restartIntentFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

const modulesIntentFile = "modules-intent.yaml"

// A ModulesIntent requests that init load the modules that the staged spec
// adds to spec.modules into the running kernel. A driver loaded at
// runtime binds to hardware that is already plugged in, so the change
// needs no drain and no reboot.
//
// It is its own file, and init deletes it, for the reasons given at
// RestartIntent. Init reads the staged store to decide whether the
// change can load without a reboot, and does nothing when it cannot,
// so a stale or duplicate intent is harmless.
type ModulesIntent struct {
	Reason       string    `json:"reason"`
	ManifestHash string    `json:"manifestHash,omitempty"`
	RequestedAt  time.Time `json:"requestedAt"`
}

func WriteModulesIntent(dir string, intent *ModulesIntent) error {
	return writeIntent(dir, modulesIntentFile, intent)
}

// ReadModulesIntent returns the pending modules intent, or nil when
// there is none.
func ReadModulesIntent(dir string) (*ModulesIntent, error) {
	intent := &ModulesIntent{}
	present, err := readIntent(dir, modulesIntentFile, intent)
	if !present || err != nil {
		return nil, err
	}
	return intent, nil
}

// ClearModulesIntent deletes the modules intent file. It succeeds when
// the file does not exist. Init clears the file before it loads the
// modules, for the reason given at ClearRestartIntent.
func ClearModulesIntent(dir string) error {
	err := os.Remove(filepath.Join(dir, modulesIntentFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
