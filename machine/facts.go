package machine

// The facts file is how init passes what it observed to the operator.
//
// Init learns things no in-cluster program can observe firsthand: the
// DHCP exchange, the moment of boot, the hardware as the kernel first
// presented it. It writes them here, shaped exactly like
// MachineStatus, so the operator's half is nearly a copy: read the
// file, fold in what it observes itself (sysctl values, conditions),
// publish to the API. The protocol is deliberately just a file: init
// stays free of any Kubernetes dependency, and the operator needs
// only a read-only hostPath mount.

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// WriteFacts publishes the machine's facts atomically (writeAtomic),
// so the operator, which re-reads this file on its own schedule, sees
// either the old facts or the new, never a torn write.
func WriteFacts(path string, facts *MachineStatus) error {
	raw, err := yaml.Marshal(facts)
	if err != nil {
		return fmt.Errorf("marshalling facts: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, raw)
}

// ReadFacts is the operator's side of the channel. A missing file is
// an error rather than a default: facts describe a boot, and if they
// are missing on a running machine, the operator should report that
// in a condition instead of publishing an empty status.
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
