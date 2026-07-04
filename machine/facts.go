package machine

// The facts file: how init tells the operator what it knows.
//
// Init learns things no in-cluster program can observe firsthand — the
// DHCP exchange, the moment of boot, the world before k3s. It writes
// them here, shaped exactly like MachineStatus, so the operator's job
// on this half is nearly a copy: read the file, fold in what it
// observes itself (sysctl values, conditions), publish to the API.
// A file is the whole protocol on purpose: init stays free of any
// Kubernetes dependency, and the operator needs only a read-only
// hostPath mount to listen.

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// WriteFacts publishes the machine's facts atomically: write a
// temporary file, then rename it into place. Rename within a filesystem
// is atomic, so the operator — which re-reads this file on its own
// schedule — sees either the old facts or the new, never a torn write.
func WriteFacts(path string, facts *MachineStatus) error {
	raw, err := yaml.Marshal(facts)
	if err != nil {
		return fmt.Errorf("marshalling facts: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".facts-*")
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
	return os.Rename(tmp.Name(), path)
}

// ReadFacts is the operator's side of the channel. A missing file is an
// error rather than a default: facts describe a boot, and an operator
// running on a machine that claims not to have booted should say so
// loudly (in a condition) instead of publishing an empty status.
func ReadFacts(path string) (*MachineStatus, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	facts := &MachineStatus{}
	if err := yaml.Unmarshal(raw, facts); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return facts, nil
}
