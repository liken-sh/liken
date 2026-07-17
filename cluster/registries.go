package cluster

// Private registries, the cluster's half: spec.registries on the
// Cluster (RegistriesSpec) declares mirror endpoints containerd pulls
// through, and k3s's embedded peer-to-peer registry. Those are
// cluster facts — any node may be asked to pull any image — and they
// converge inside the canonical cluster document like every other
// fleet-wide fact.
//
// Credentials are deliberately not declared here. A spec is public:
// anyone who can get the Cluster can read every field. They enter
// through a Kubernetes Secret instead, which the machine operator
// renders into a per-machine credentials document riding the staged/
// proven lifecycle (machine/registries.go tells that story).

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
)

// RegistriesSpec is the Cluster's declaration of how images arrive.
// The zero value means nothing declared: no mirrors, no embedded
// registry, no registries.yaml written at all.
type RegistriesSpec struct {
	// Mirrors maps a registry host, as an image reference names it
	// (docker.io, registry.example:5000), to the endpoint URLs
	// containerd should try, in preference order, before falling
	// back to the registry itself.
	Mirrors map[string][]string `json:"mirrors,omitempty"`

	// Embedded turns on k3s's embedded registry mirror (Spegel):
	// every node serves the images it already holds to its peers,
	// which is what keeps a fleet on a slow uplink from pulling the
	// same bytes once per machine.
	Embedded bool `json:"embedded,omitempty"`
}

// validateRegistries holds spec.registries to its shape. The spec
// itself may be null or absent — both decode to the zero struct and
// genuinely mean "no registries configuration", a contrast with
// spec.features, where a null could be mistaken for an opt-in. But a
// mirror host with a null or empty endpoint list is refused loudly:
// a mirror with nowhere to point is neither a mirror nor nothing,
// and `docker.io:` in hand-written YAML is a mistake to name, not to
// guess about.
func validateRegistries(r RegistriesSpec) error {
	for _, host := range slices.Sorted(maps.Keys(r.Mirrors)) {
		if host == "" {
			return fmt.Errorf("spec.registries.mirrors: a mirror's key must be the registry host it stands in for (docker.io, registry.example:5000)")
		}
		endpoints := r.Mirrors[host]
		if len(endpoints) == 0 {
			return fmt.Errorf("spec.registries.mirrors: %s lists no endpoints; list at least one endpoint URL, or remove the host entirely", host)
		}
		for _, endpoint := range endpoints {
			u, err := url.Parse(endpoint)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("spec.registries.mirrors: %s endpoint %q must be an http:// or https:// URL", host, endpoint)
			}
		}
	}
	return nil
}
