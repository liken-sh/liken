package machine

// The reboot channel is how the operator asks init to reboot the
// machine.
//
// Only PID 1 can shut a machine down properly, so the operator never
// reboots the machine itself. Instead, the operator writes an intent
// file. Init polls for this file. When init finds the file, init
// stops k3s cleanly and reboots the machine.
//
// The channel is a directory of its own under /run/liken, because
// the two programs' mounts enforce the two directions of the flow.
// Facts flow from init to the operator through a read-only mount.
// Intents flow from the operator to init through this directory.
// /run is a fresh tmpfs on every boot. So an intent can never survive
// the reboot that it requested, and it can never cause a second
// reboot.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// OperatorRunDir is the operator's writable channel to init. Init
// creates this directory, along with the rest of /run/liken, before
// it starts k3s.
const OperatorRunDir = "/run/liken/operator"

const rebootIntentFile = "reboot-intent.yaml"

// A RebootIntent states why the machine should reboot and which
// staged manifest the reboot is meant to apply. The presence of the
// file is the trigger for the reboot. The content of the file only
// adds detail to what init prints on the console.
type RebootIntent struct {
	Reason       string    `json:"reason"`
	ManifestHash string    `json:"manifestHash,omitempty"`
	RequestedAt  time.Time `json:"requestedAt"`
}

// writeIntent writes one intent file atomically, using writeAtomic,
// the same function the facts use. This atomic write matters because
// init, when it polls the file mid-write, must see either a whole
// intent or no file at all. The channel directory belongs to init to
// create, so writeIntent reports a missing directory as an error.
// writeIntent never creates the directory itself.
func writeIntent(dir, name string, intent any) error {
	raw, err := yaml.Marshal(intent)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, name), raw)
}

// readIntent reads one intent file into out, and reports whether the
// file was present. A result of false with no error means that no
// intent exists, which is the result for almost every poll.
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

// WriteRebootIntent writes a request to reboot the machine.
func WriteRebootIntent(dir string, intent *RebootIntent) error {
	return writeIntent(dir, rebootIntentFile, intent)
}

// ReadRebootIntent reports the pending intent, or reports nil when no
// reboot has been requested.
func ReadRebootIntent(dir string) (*RebootIntent, error) {
	intent := &RebootIntent{}
	present, err := readIntent(dir, rebootIntentFile, intent)
	if !present || err != nil {
		return nil, err
	}
	return intent, nil
}

const restartIntentFile = "restart-intent.yaml"

// A RestartIntent asks init to bounce the k3s child process in
// place. Init uses this bounce for changes that k3s reads only at
// process start; cluster/changes.go names these changes. This intent
// is a sibling file to the reboot intent, and it is deliberately not
// a field on the reboot intent. Init honors an unreadable reboot
// intent by rebooting anyway. Suppose restart information became a
// new field on the reboot intent instead. An older init that predates
// that field would fail to parse the reboot-intent file whenever only
// a restart was requested. Because of that failure, that older init
// would reboot the machine by surprise, instead of only restarting
// k3s. A sibling file avoids this problem. An older init that
// predates the restart-intent file does not look for it. So the file
// stays invisible to that init, and causes no reaction at all.
//
// The two intents also differ in how they are used. Init never
// consumes a reboot intent, because /run is destroyed along with the
// boot that the intent requested. But init must consume a restart
// intent, or the poll that found it would bounce k3s forever. Like
// the reboot intent, the presence of the restart-intent file is the
// trigger. The staged stores on machineState hold the truth about
// what to apply. This means a duplicate intent is harmless: init
// checks the stores, not the intent, to decide what to apply.
type RestartIntent struct {
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requestedAt"`
}

// WriteRestartIntent writes a request to restart k3s.
func WriteRestartIntent(dir string, intent *RestartIntent) error {
	return writeIntent(dir, restartIntentFile, intent)
}

// ReadRestartIntent reports the pending intent, or reports nil when
// no restart has been requested.
func ReadRestartIntent(dir string) (*RestartIntent, error) {
	intent := &RestartIntent{}
	present, err := readIntent(dir, restartIntentFile, intent)
	if !present || err != nil {
		return nil, err
	}
	return intent, nil
}

// ClearRestartIntent consumes the intent. Init clears the intent file
// before it bounces k3s. If a crash happens between these two steps,
// the machine loses one restart request, but the operator's next pass
// re-requests it. This order is the self-healing order. The reverse
// order would bounce k3s forever, because init would find the same
// intent file on every poll after each bounce. An absent file is
// fine; clearing an absent file is idempotent.
func ClearRestartIntent(dir string) error {
	err := os.Remove(filepath.Join(dir, restartIntentFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

const modulesIntentFile = "modules-intent.yaml"

// A ModulesIntent asks init to load the staged spec's added kernel
// modules into the running kernel. This intent exists for the one
// machine-spec change that needs no disruption at all: module
// loading. Module loading is possible while the machine is live. A
// driver that is already loaded into the kernel can take control of
// already-plugged-in hardware without a reboot. So an additive
// spec.modules edit should not cost a node drain and a reboot.
//
// This file is a third sibling file, for the same reasons that the
// restart intent is a sibling file rather than a field. First, the
// file is invisible to any init that predates it. Second, init must
// consume the file, like the restart intent, because the machine
// keeps running afterward. If init left the file in place, init would
// load the modules again on every later poll. The presence of the
// file is the trigger, and the staged store holds the truth about
// what to load. Init re-derives whether the staged manifest can be
// applied live, on its own, and init refuses to act on anything that
// would need a boot. This means a stale or duplicate intent is
// harmless.
type ModulesIntent struct {
	Reason       string    `json:"reason"`
	ManifestHash string    `json:"manifestHash,omitempty"`
	RequestedAt  time.Time `json:"requestedAt"`
}

// WriteModulesIntent writes a request for a live module load.
func WriteModulesIntent(dir string, intent *ModulesIntent) error {
	return writeIntent(dir, modulesIntentFile, intent)
}

// ReadModulesIntent reports the pending intent, or reports nil when
// no load has been requested.
func ReadModulesIntent(dir string) (*ModulesIntent, error) {
	intent := &ModulesIntent{}
	present, err := readIntent(dir, modulesIntentFile, intent)
	if !present || err != nil {
		return nil, err
	}
	return intent, nil
}

// ClearModulesIntent consumes the intent. It clears the intent file
// before it acts, in the same order that the restart intent uses, and
// for the same reason. If a crash happens between these two steps,
// the machine loses one request, but the operator's next pass
// re-requests it.
func ClearModulesIntent(dir string) error {
	err := os.Remove(filepath.Join(dir, modulesIntentFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
