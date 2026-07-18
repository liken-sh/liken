package machine

// The reboot channel is how the operator asks init to reboot.
//
// Only PID 1 can shut a machine down properly, so the operator never
// reboots the machine itself. It writes an intent file, and init
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
// manifest the reboot is meant to apply. The file's presence is the
// trigger; the content only adds detail to what init prints on the
// console.
type RebootIntent struct {
	Reason       string    `json:"reason"`
	ManifestHash string    `json:"manifestHash,omitempty"`
	RequestedAt  time.Time `json:"requestedAt"`
}

// writeIntent writes one intent file atomically (writeAtomic, like
// the facts): init polling mid-write must see a whole intent or none.
// The channel directory is init's to create, so a missing one is an
// error to surface, never a directory to invent.
func writeIntent(dir, name string, intent any) error {
	raw, err := yaml.Marshal(intent)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, name), raw)
}

// readIntent reads one intent file into out, reporting presence:
// false with no error means no intent stands, which is almost every
// poll.
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

// WriteRebootIntent asks for a reboot.
func WriteRebootIntent(dir string, intent *RebootIntent) error {
	return writeIntent(dir, rebootIntentFile, intent)
}

// ReadRebootIntent reports the pending intent, or nil when no reboot
// has been requested.
func ReadRebootIntent(dir string) (*RebootIntent, error) {
	intent := &RebootIntent{}
	present, err := readIntent(dir, rebootIntentFile, intent)
	if !present || err != nil {
		return nil, err
	}
	return intent, nil
}

const restartIntentFile = "restart-intent.yaml"

// A RestartIntent asks init to bounce the k3s child in place, for
// changes k3s reads only at process start (cluster/changes.go names
// them). It is a sibling file to the reboot intent, deliberately not
// a field on it: init honors an *unreadable* reboot intent by
// rebooting anyway, so a new field in that file would read as a
// surprise reboot to any init that predates it, while a sibling file
// is simply invisible to one. The two intents also live differently.
// A reboot intent is never consumed — /run dies with the boot it
// asked for — but a restart intent must be, or the poll that found
// it would bounce k3s forever. Like the reboot intent, the file's
// presence is the trigger and the staged stores on machineState are
// the truth about what to apply, so a duplicate intent is harmless:
// init checks the stores, not the intent.
type RestartIntent struct {
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requestedAt"`
}

// WriteRestartIntent asks for a k3s restart.
func WriteRestartIntent(dir string, intent *RestartIntent) error {
	return writeIntent(dir, restartIntentFile, intent)
}

// ReadRestartIntent reports the pending intent, or nil when no
// restart has been requested.
func ReadRestartIntent(dir string) (*RestartIntent, error) {
	intent := &RestartIntent{}
	present, err := readIntent(dir, restartIntentFile, intent)
	if !present || err != nil {
		return nil, err
	}
	return intent, nil
}

// ClearRestartIntent consumes the intent. Init clears before it
// bounces: a crash between the two loses one restart, and the
// operator's next pass re-requests it — the self-healing order. (The
// reverse order would bounce k3s forever.) An absent file is fine;
// clearing is idempotent.
func ClearRestartIntent(dir string) error {
	err := os.Remove(filepath.Join(dir, restartIntentFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

const modulesIntentFile = "modules-intent.yaml"

// A ModulesIntent asks init to load the staged spec's added kernel
// modules into the running kernel, for the one machine-spec change
// that needs no disruption at all: module loading is live-capable
// (a resident driver claims already-plugged hardware on its own),
// so an additive spec.modules edit shouldn't cost a drain and a
// reboot. It is a third sibling file, for the same reasons the
// restart intent is a sibling rather than a field: invisible to any
// init that predates it, and consumed like the restart intent — the
// machine lives on, so leaving the file would load forever. The
// file's presence is the trigger and the staged store is the truth
// about what to load; init re-derives the staged manifest's
// live-applicability for itself and refuses anything that would
// need a boot, so a stale or duplicate intent is harmless.
type ModulesIntent struct {
	Reason       string    `json:"reason"`
	ManifestHash string    `json:"manifestHash,omitempty"`
	RequestedAt  time.Time `json:"requestedAt"`
}

// WriteModulesIntent asks for a live module load.
func WriteModulesIntent(dir string, intent *ModulesIntent) error {
	return writeIntent(dir, modulesIntentFile, intent)
}

// ReadModulesIntent reports the pending intent, or nil when no load
// has been requested.
func ReadModulesIntent(dir string) (*ModulesIntent, error) {
	intent := &ModulesIntent{}
	present, err := readIntent(dir, modulesIntentFile, intent)
	if !present || err != nil {
		return nil, err
	}
	return intent, nil
}

// ClearModulesIntent consumes the intent, in the same clear-before-
// acting order the restart intent uses and for the same reason: a
// crash between the two loses one request, and the operator's next
// pass re-requests it.
func ClearModulesIntent(dir string) error {
	err := os.Remove(filepath.Join(dir, modulesIntentFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
