package cluster

import (
	"encoding/json"
	"testing"
)

// specWith applies one mutation to a fully populated base spec. Each
// case then exercises exactly one domain, and the comparison cannot
// pass by accident because of zero values.
func specWith(mutate func(*ClusterSpec)) ClusterSpec {
	spec := ClusterSpec{
		Origin:   OriginFounded,
		Leaders:  []string{"node-1", "node-2", "node-3"},
		Endpoint: "https://10.10.0.1:6443",
		Network:  ClusterNetworkSpec{NodeCIDR: "10.10.0.0/24"},
		Time:     ClusterTimeSpec{Upstreams: []string{"time.example"}},
		Disruption: ClusterDisruptionSpec{
			MaxUnavailable: 1,
		},
		Features: map[string]*FeatureConfig{"iscsi": {}},
		Registries: RegistriesSpec{
			Mirrors:  map[string][]string{"docker.io": {"https://mirror.example:5000"}},
			Embedded: true,
		},
		Runtime: ClusterRuntimeSpec{K3s: K3sRuntimeSpec{GoMemoryLimit: "25%"}},
	}
	if mutate != nil {
		mutate(&spec)
	}
	return spec
}

func TestRestartAppliesByDomain(t *testing.T) {
	cases := map[string]struct {
		mutate func(*ClusterSpec)
		want   bool
	}{
		"a feature toggle":        {func(s *ClusterSpec) { s.Features["traefik"] = &FeatureConfig{} }, true},
		"a feature retraction":    {func(s *ClusterSpec) { s.Features = nil }, true},
		"a mirror edit":           {func(s *ClusterSpec) { s.Registries.Embedded = false }, true},
		"a runtime tuning":        {func(s *ClusterSpec) { s.Runtime.K3s.GoMemoryLimit = "off" }, true},
		"a runtime GoGC edit":     {func(s *ClusterSpec) { n := 80; s.Runtime.K3s.GoGC = &n }, true},
		"runtime and a feature":   {func(s *ClusterSpec) { s.Runtime.K3s.GoMemoryLimit = "off"; s.Features["traefik"] = &FeatureConfig{} }, true},
		"features and registries": {func(s *ClusterSpec) { s.Features = nil; s.Registries.Embedded = false }, true},
		"runtime with a reboot field": {func(s *ClusterSpec) {
			s.Runtime.K3s.GoMemoryLimit = "off"
			s.Endpoint = "https://10.10.0.2:6443"
		}, false},
		"the origin":            {func(s *ClusterSpec) { s.Origin = OriginAdopted }, false},
		"the leaders":           {func(s *ClusterSpec) { s.Leaders = []string{"node-1"} }, false},
		"the endpoint":          {func(s *ClusterSpec) { s.Endpoint = "https://10.10.0.2:6443" }, false},
		"the network plan":      {func(s *ClusterSpec) { s.Network.ClusterCIDR = "10.44.0.0/16" }, false},
		"the time hierarchy":    {func(s *ClusterSpec) { s.Time.Upstreams = nil }, false},
		"the disruption budget": {func(s *ClusterSpec) { s.Disruption.MaxUnavailable = 2 }, false},
		"a mixed edit":          {func(s *ClusterSpec) { s.Features = nil; s.Endpoint = "https://10.10.0.2:6443" }, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := RestartApplies(specWith(nil), specWith(tc.mutate)); got != tc.want {
				t.Errorf("RestartApplies = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRestartAppliesIdenticalSpecsNeedNoDisruption(t *testing.T) {
	if RestartApplies(specWith(nil), specWith(nil)) {
		t.Error("no drift needs no disruption at all")
	}
}

func TestRestartAppliesIgnoresVersionAndReleases(t *testing.T) {
	// Canonical documents never carry version or releases, because
	// the operator strips them before hashing. The classifier must
	// never treat them as drift either. Alone, they are no change.
	// Alongside a feature toggle, they must not drag the change to a
	// reboot.
	releasesOnly := specWith(func(s *ClusterSpec) {
		s.Version = "0.3.0"
		s.Releases = ClusterReleasesSpec{Source: "https://releases.example"}
	})
	if RestartApplies(specWith(nil), releasesOnly) {
		t.Error("a release-feed edit alone is no drift at all")
	}
	both := specWith(func(s *ClusterSpec) {
		s.Version = "0.3.0"
		s.Features["traefik"] = &FeatureConfig{}
	})
	if !RestartApplies(specWith(nil), both) {
		t.Error("the release feed must not drag a feature toggle to a reboot")
	}
}

func TestRestartAppliesTreatsAnUnknownFieldAsRebootClass(t *testing.T) {
	// The safety property is structural: a field the classifier does
	// not recognize must read as reboot-class. The test cannot
	// simulate this by round-tripping a spec through JSON with an
	// extra field, because the strict parser refuses it. Instead,
	// this test pins the mechanism directly. The subtraction zeroes
	// only the restart-class fields. Anything else that differs
	// survives the subtraction and answers reboot. Here, the endpoint
	// field stands in for a future field.
	changed := specWith(func(s *ClusterSpec) {
		s.Endpoint = "https://10.10.0.9:6443"
		s.Features["traefik"] = &FeatureConfig{}
	})
	if RestartApplies(specWith(nil), changed) {
		t.Error("any residual difference beyond the restart-class fields must fall to reboot")
	}
}

func TestRestartAppliesDoesNotMutateItsArguments(t *testing.T) {
	// RestartApplies zeroes fields on its copies, which it holds by
	// value. The caller's specs must come back untouched, or the
	// operator would corrupt the live document mid-reconcile.
	old, new := specWith(nil), specWith(func(s *ClusterSpec) { s.Features = nil })
	before, _ := json.Marshal(old)
	RestartApplies(old, new)
	after, _ := json.Marshal(old)
	if string(before) != string(after) {
		t.Error("the caller's spec must not be mutated")
	}
}
