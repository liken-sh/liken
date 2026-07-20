package machine

// The facts file carries what init observed to the operator.
//
// Init learns facts that no program inside the cluster can observe
// directly: the DHCP exchange, the moment of boot, the hardware as
// the kernel first showed it. Init writes these facts to this file,
// in the same shape as MachineStatus. So the operator's half of the
// work is close to a copy: read the file, add what the operator
// observes itself (sysctl values, conditions), and publish the
// result to the API. The protocol is a plain file by design. Init
// stays free of any Kubernetes dependency. The operator needs only a
// read-only hostPath mount.

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// WriteFacts publishes the machine's facts atomically, through
// writeAtomic. The operator rereads this file on its own schedule.
// The operator always sees either the old facts or the new facts,
// never a torn write.
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
// an error, not a default value. Facts describe a boot. If the facts
// are missing on a running machine, the operator must report that
// fact in a condition. The operator must not publish an empty status
// instead.
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
