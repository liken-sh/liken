package main

// The Machine manifest: how a person tells a liken machine who it is.
//
// liken's configuration is one document, shaped exactly like a
// Kubernetes resource. That's a deliberate, well-precedented move —
// kubelet's KubeletConfiguration, k0s's config, and Talos's machine
// config all work this way — because a schema-disciplined document can
// ride two transports: today init reads it from a file baked into the
// image (there is no API server yet when PID 1 wakes up), and later the
// same document becomes a real CRD in the cluster, where a liken
// operator publishes live facts into its status. One mental model from
// first boot to fleet: `kubectl get machine -o yaml` on day 300 shows
// the same shape you hand-wrote on day one.
//
// The API group is liken.sh — CRD groups are DNS names, and we own
// that one.

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// The struct tags are json, not yaml, because we parse with
// sigs.k8s.io/yaml — the same converter Kubernetes tooling uses. It
// turns YAML into JSON before unmarshalling, which is what gives
// Kubernetes documents their camelCase convention and means this struct
// would serialize identically as a real in-cluster object.
type Machine struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		// The machine's name becomes its hostname, and eventually its
		// Kubernetes node name.
		Name string `json:"name"`
	} `json:"metadata"`
	Spec MachineSpec `json:"spec"`
}

type MachineSpec struct {
	Network NetworkSpec `json:"network"`
}

// NetworkSpec is deliberately almost empty: liken's default posture is
// zero-config — DHCP on the first physical interface, hostname from the
// manifest, DNS from the lease. Fields appear here only when a machine
// genuinely needs to deviate (a pinned interface today; static
// addressing someday).
type NetworkSpec struct {
	// Interface pins network bring-up to a specific interface name
	// (e.g. "eth1"). Empty means: the first interface that looks like
	// real hardware.
	Interface string `json:"interface,omitempty"`
}

const machinePath = "/etc/liken/machine.yaml"

// loadMachine reads the manifest the image was built with. A machine
// with no manifest is still a valid machine — everything defaults — but
// a manifest that exists and doesn't parse, or claims to be something
// other than a Machine, is a configuration error worth hearing about
// on the console.
func loadMachine() (*Machine, error) {
	m := &Machine{}
	raw, err := os.ReadFile(machinePath)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.UnmarshalStrict(raw, m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", machinePath, err)
	}
	if m.Kind != "Machine" {
		return nil, fmt.Errorf("%s: expected kind Machine, got %q", machinePath, m.Kind)
	}
	return m, nil
}
