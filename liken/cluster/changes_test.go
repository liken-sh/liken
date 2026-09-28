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
		// One feature that leaves host state and one that does not,
		// so the cases below can retract either kind.
		Features: map[string]*FeatureConfig{"iscsi": {}, "metrics-server": {}},
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
		"a feature toggle": {func(s *ClusterSpec) { s.Features["traefik"] = &FeatureConfig{} }, true},
		// metrics-server leaves nothing on the host, so retracting it
		// still converges by a restart. iscsi leaves a loaded module
		// and any live session, so retracting it needs a boot, and it
		// carries anything else in the same edit to the boot tier.
		"a plain retraction":      {func(s *ClusterSpec) { delete(s.Features, "metrics-server") }, true},
		"a host-state retraction": {func(s *ClusterSpec) { delete(s.Features, "iscsi") }, false},
		"every feature retracted": {func(s *ClusterSpec) { s.Features = nil }, false},
		"a host-state retraction beside a mirror edit": {func(s *ClusterSpec) {
			delete(s.Features, "iscsi")
			s.Registries.Embedded = false
		}, false},
		"a mirror edit":       {func(s *ClusterSpec) { s.Registries.Embedded = false }, true},
		"a runtime tuning":    {func(s *ClusterSpec) { s.Runtime.K3s.GoMemoryLimit = "off" }, true},
		"a runtime GoGC edit": {func(s *ClusterSpec) { n := 80; s.Runtime.K3s.GoGC = &n }, true},
		"an image GC policy": {func(s *ClusterSpec) {
			n := 70
			s.Runtime.Kubelet.ImageGC.HighThresholdPercent = &n
			s.Runtime.Kubelet.ImageGC.MaximumAge = "168h"
		}, true},
		"a log level edit": {func(s *ClusterSpec) {
			s.Runtime.K3s.Debug = true
			s.Runtime.Containerd.LogLevel = "warn"
		}, true},
		"runtime and a feature": {func(s *ClusterSpec) { s.Runtime.K3s.GoMemoryLimit = "off"; s.Features["traefik"] = &FeatureConfig{} }, true},
		"features and registries": {func(s *ClusterSpec) {
			s.Features["traefik"] = &FeatureConfig{}
			s.Registries.Embedded = false
		}, true},
		"runtime with a reboot field": {func(s *ClusterSpec) {
			s.Runtime.K3s.GoMemoryLimit = "off"
			s.Network.ClusterCIDR = "10.44.0.0/16"
		}, false},
		// The origin and the endpoint are next-boot-class, which is
		// lighter than a restart, so they never drag an edit up to the
		// reboot tier. Alone they answer true here as well, and the
		// operator asks NextBootApplies first.
		"the origin":               {func(s *ClusterSpec) { s.Origin = OriginAdopted }, true},
		"the endpoint":             {func(s *ClusterSpec) { s.Endpoint = "https://10.10.0.2:6443" }, true},
		"runtime with an endpoint": {func(s *ClusterSpec) { s.Runtime.K3s.GoMemoryLimit = "off"; s.Endpoint = "https://10.10.0.2:6443" }, true},
		"the leaders":              {func(s *ClusterSpec) { s.Leaders = []string{"node-1"} }, false},
		"the NodePort networks":    {func(s *ClusterSpec) { s.Network.NodePortCIDRs = []string{"10.10.0.0/24"} }, true},
		"the network plan":         {func(s *ClusterSpec) { s.Network.ClusterCIDR = "10.44.0.0/16" }, false},
		"NodePorts with a reboot field": {func(s *ClusterSpec) {
			s.Network.NodePortCIDRs = []string{"10.10.0.0/24"}
			s.Network.ClusterCIDR = "10.44.0.0/16"
		}, false},
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

func TestNextBootAppliesByDomain(t *testing.T) {
	cases := map[string]struct {
		mutate func(*ClusterSpec)
		want   bool
	}{
		"the origin":   {func(s *ClusterSpec) { s.Origin = OriginAdopted }, true},
		"the endpoint": {func(s *ClusterSpec) { s.Endpoint = "https://10.10.0.2:6443" }, true},
		"both": {func(s *ClusterSpec) {
			s.Origin = OriginAdopted
			s.Endpoint = "https://10.10.0.2:6443"
		}, true},
		// A restart-class field beside the origin takes the restart
		// tier, and a reboot-class field beside it takes the reboot
		// tier. Either way this classifier answers false, and the
		// operator falls through to the heavier gate.
		"the origin with a feature": {func(s *ClusterSpec) {
			s.Origin = OriginAdopted
			s.Features["traefik"] = &FeatureConfig{}
		}, false},
		"the origin with the network plan": {func(s *ClusterSpec) {
			s.Origin = OriginAdopted
			s.Network.ClusterCIDR = "10.44.0.0/16"
		}, false},
		"the leaders": {func(s *ClusterSpec) { s.Leaders = []string{"node-1"} }, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := NextBootApplies(specWith(nil), specWith(tc.mutate)); got != tc.want {
				t.Errorf("NextBootApplies = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNextBootAppliesIdenticalSpecsNeedNoStaging(t *testing.T) {
	if NextBootApplies(specWith(nil), specWith(nil)) {
		t.Error("no drift needs no staging at all")
	}
	// The release feed is not part of the canonical document, so it is
	// no drift for this classifier either.
	releasesOnly := specWith(func(s *ClusterSpec) {
		s.Version = "0.3.0"
		s.Releases = ClusterReleasesSpec{Source: "https://releases.example"}
	})
	if NextBootApplies(specWith(nil), releasesOnly) {
		t.Error("a release-feed edit alone is no drift at all")
	}
}

func TestNextBootAppliesDoesNotMutateItsArguments(t *testing.T) {
	old, new := specWith(nil), specWith(func(s *ClusterSpec) { s.Endpoint = "https://10.10.0.2:6443" })
	before, _ := json.Marshal(old)
	NextBootApplies(old, new)
	after, _ := json.Marshal(old)
	if string(before) != string(after) {
		t.Error("the caller's spec must not be mutated")
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
	// only the restart-class and next-boot-class fields. Anything else
	// that differs survives the subtraction and answers reboot. Here,
	// the cluster CIDR stands in for a future field.
	changed := specWith(func(s *ClusterSpec) {
		s.Network.ClusterCIDR = "10.44.0.0/16"
		s.Features["traefik"] = &FeatureConfig{}
	})
	if RestartApplies(specWith(nil), changed) {
		t.Error("any residual difference beyond the restart-class fields must fall to reboot")
	}
	if NextBootApplies(specWith(nil), changed) {
		t.Error("any residual difference beyond the origin and the endpoint must fall to a heavier tier")
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
