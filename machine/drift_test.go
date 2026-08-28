package machine

import (
	"slices"
	"strings"
	"testing"
)

// driftLabStorage returns the lab machine's storage shape: five
// roles across two disks. Tests reuse this fixture for drift
// comparisons.
func driftLabStorage() StorageSpec {
	return StorageSpec{
		MachineState:     &StorageRole{Device: "/dev/vda", Size: "64Mi"},
		MachineEphemeral: &StorageRole{Device: "/dev/vdb", Size: "512Mi"},
		ClusterState:     &StorageRole{Device: "/dev/vda"},
		PodStorage:       &StorageRole{Device: "/dev/vdb", Size: "2Gi"},
		PodEphemeral:     &StorageRole{Device: "/dev/vdb"},
	}
}

func TestStorageDriftSeesNoDriftInTheSameSpec(t *testing.T) {
	if diffs := StorageDrift(driftLabStorage(), driftLabStorage()); len(diffs) != 0 {
		t.Errorf("identical specs should not drift: %v", diffs)
	}
}

func TestStorageDriftNormalizesSizes(t *testing.T) {
	desired := driftLabStorage()
	desired.PodStorage.Size = "2048Mi" // the same size as 2Gi, written differently
	if diffs := StorageDrift(desired, driftLabStorage()); len(diffs) != 0 {
		t.Errorf("2048Mi and 2Gi are the same size: %v", diffs)
	}
}

func TestStorageDriftSeesAGrow(t *testing.T) {
	desired := driftLabStorage()
	desired.PodStorage.Size = "3Gi"
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "podStorage") {
		t.Errorf("expected one podStorage diff: %v", diffs)
	}
}

func TestStorageDriftSeesAnAddedRole(t *testing.T) {
	actuated := driftLabStorage()
	actuated.PodStorage = nil
	diffs := StorageDrift(driftLabStorage(), actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "declared but not actuated") {
		t.Errorf("expected an added-role diff: %v", diffs)
	}
}

func TestStorageDriftSeesARemovedRole(t *testing.T) {
	desired := driftLabStorage()
	desired.PodEphemeral = nil
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "no longer declared") {
		t.Errorf("expected a removed-role diff: %v", diffs)
	}
}

func TestStorageDriftSeesADeviceChange(t *testing.T) {
	desired := driftLabStorage()
	desired.ClusterState.Device = "/dev/vdc"
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "device") {
		t.Errorf("expected a device diff: %v", diffs)
	}
}

func TestStorageDriftFallsBackToStringsForUnparseableSizes(t *testing.T) {
	// Validation refuses these sizes anyway. Drift detection must not
	// panic on them. String equality is the only comparison left.
	desired := driftLabStorage()
	desired.PodStorage.Size = "a-whole-bunch"
	actuated := driftLabStorage()
	actuated.PodStorage.Size = "a-whole-bunch"
	if diffs := StorageDrift(desired, actuated); len(diffs) != 0 {
		t.Errorf("identical spellings should not drift, parseable or not: %v", diffs)
	}
	actuated.PodStorage.Size = "even-more"
	if diffs := StorageDrift(desired, actuated); len(diffs) != 1 {
		t.Errorf("different spellings should drift: %v", diffs)
	}
}

func TestStorageDriftNamesTheRemainder(t *testing.T) {
	// A remainder role's size is spelled "" in the spec. The diff
	// message must say "(remainder)" instead of showing nothing.
	desired := driftLabStorage()
	desired.ClusterState.Size = "3Gi" // held the remainder before this line
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "(remainder)") {
		t.Errorf("expected the diff to name the remainder: %v", diffs)
	}
}

// driftLabNetwork returns the lab machine's network shape: a DHCP
// uplink, and a cluster segment with a static address on it. Tests
// reuse this fixture the way the storage tests reuse
// driftLabStorage.
func driftLabNetwork() NetworkSpec {
	return NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth1", Address: "10.10.0.1/24"},
	}}
}

func TestNetworkDriftSeesNoDriftInTheSameSpec(t *testing.T) {
	actuated := driftLabNetwork()
	if diffs := NetworkDrift(driftLabNetwork(), &actuated); len(diffs) != 0 {
		t.Errorf("identical specs should not drift: %v", diffs)
	}
}

func TestNetworkDriftTreatsTwoEmptySpecsAsConverged(t *testing.T) {
	// A machine that declared nothing and still declares nothing takes
	// the zero-configuration default on both sides. If this case
	// drifted, every machine that never wrote a network stanza would
	// ask for a reboot forever.
	if diffs := NetworkDrift(NetworkSpec{}, &NetworkSpec{}); len(diffs) != 0 {
		t.Errorf("nothing declared, nothing actuated: %v", diffs)
	}
}

func TestNetworkDriftReportsNothingWithoutABootRecord(t *testing.T) {
	// A boot that recorded no network cannot be judged. Reading the
	// missing record as an empty spec would report every declared
	// interface as drift, and a fleet of such machines would all stage
	// a manifest and ask for a reboot at once.
	if diffs := NetworkDrift(driftLabNetwork(), nil); len(diffs) != 0 {
		t.Errorf("an unrecorded network is not drift: %v", diffs)
	}
}

func TestNetworkDriftSeesAnAddedInterface(t *testing.T) {
	actuated := NetworkSpec{Interfaces: driftLabNetwork().Interfaces[:1]}
	diffs := NetworkDrift(driftLabNetwork(), &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "eth1 declared but not actuated") {
		t.Errorf("expected an added-interface diff: %v", diffs)
	}
}

func TestNetworkDriftSeesARemovedInterface(t *testing.T) {
	actuated := driftLabNetwork()
	desired := NetworkSpec{Interfaces: driftLabNetwork().Interfaces[:1]}
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "actuated but no longer declared") {
		t.Errorf("expected a removed-interface diff: %v", diffs)
	}
}

func TestNetworkDriftSeesAChangedAddress(t *testing.T) {
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[1].Address = "10.10.0.9/24"
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "address 10.10.0.9/24 declared, 10.10.0.1/24 actuated") {
		t.Errorf("expected an address diff: %v", diffs)
	}
}

func TestNetworkDriftNamesTheDHCPDefault(t *testing.T) {
	// An interface with no address asks for DHCP. The diff must say
	// that word, because the person reading it has an empty field on
	// one side and needs to know what the empty field means.
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[0].Address = "192.168.1.5/24"
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "(DHCP) actuated") {
		t.Errorf("expected the diff to name DHCP: %v", diffs)
	}
}

func TestNetworkDriftSeesAChangedNameserverList(t *testing.T) {
	// This is the edit the lab drilled: a nameserver added to an
	// interface that had none. Nameserver order is preference order,
	// so the list is compared as a list, not as a set.
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[0].Nameservers = []string{"10.10.0.1"}
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "nameservers 10.10.0.1 declared, (none) actuated") {
		t.Errorf("expected a nameservers diff: %v", diffs)
	}
}

func TestNetworkDriftSeesAChangedGateway(t *testing.T) {
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[1].Gateway = "10.10.0.254"
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "gateway 10.10.0.254 declared, (none) actuated") {
		t.Errorf("expected a gateway diff: %v", diffs)
	}
}

func TestNetworkDriftReportsAPortSwapOnce(t *testing.T) {
	// When a position names a different port, the addressing under it
	// belongs to that other port. Reporting each field as well would
	// say the same difference several times over.
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[1] = InterfaceSpec{Name: "eth2", Address: "10.10.0.9/24"}
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "interface 2: eth2 declared, eth1 actuated") {
		t.Errorf("expected one identity diff: %v", diffs)
	}
}

func TestNetworkDriftSeesAReorderedList(t *testing.T) {
	// Interface order is the order each interface's nameservers reach
	// resolv.conf, so the same two ports in the other order are a
	// different request.
	actuated := driftLabNetwork()
	desired := NetworkSpec{Interfaces: []InterfaceSpec{
		actuated.Interfaces[1], actuated.Interfaces[0],
	}}
	if diffs := NetworkDrift(desired, &actuated); len(diffs) != 2 {
		t.Errorf("a reorder is a change at both positions: %v", diffs)
	}
}

// Host entries reconcile live, through the machine operator, on
// every pass. An edit here must never report drift and never stage a
// manifest, because the pass that read the edit already applied it.
func TestNetworkDriftIgnoresHostEntries(t *testing.T) {
	desired := NetworkSpec{HostEntries: []HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}}
	actuated := NetworkSpec{HostEntries: []HostEntry{{Address: "10.10.0.21", Names: []string{"printer"}}}}
	if diffs := NetworkDrift(desired, &actuated); len(diffs) != 0 {
		t.Errorf("host entries must never drift: %v", diffs)
	}
}

// driftLabWireless is the lab radio's shape as a boot record: a
// wireless entry with a static address on it.
func driftLabWireless() NetworkSpec {
	spec := driftLabNetwork()
	spec.Interfaces = append(spec.Interfaces, InterfaceSpec{
		Name: "wlan0", Address: "10.10.0.2/24",
		Wireless: &WirelessSpec{SSID: "stonypoint", Security: WirelessWPAPSK},
	})
	return spec
}

func TestNetworkDriftSeesNoDriftInTheSameWirelessSpec(t *testing.T) {
	actuated := driftLabWireless()
	if diffs := NetworkDrift(driftLabWireless(), &actuated); len(diffs) != 0 {
		t.Errorf("identical specs should not drift: %v", diffs)
	}
}

func TestNetworkDriftSeesAChangedSSID(t *testing.T) {
	actuated := driftLabWireless()
	desired := driftLabWireless()
	desired.Interfaces[2].Wireless.SSID = "stonypoint-guest"
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "wireless stonypoint-guest (wpa-psk) declared, stonypoint (wpa-psk) actuated") {
		t.Errorf("expected an SSID diff: %v", diffs)
	}
}

func TestNetworkDriftSeesAChangedSecurity(t *testing.T) {
	actuated := driftLabWireless()
	desired := driftLabWireless()
	desired.Interfaces[2].Wireless.Security = WirelessOpen
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "stonypoint (open) declared, stonypoint (wpa-psk) actuated") {
		t.Errorf("expected a security diff: %v", diffs)
	}
}

func TestNetworkDriftReadsTheUnsetSecurityAsItsDefault(t *testing.T) {
	// The API server writes the default into a spec applied through
	// it, and a manifest from a stick keeps the field empty, so the
	// comparison resolves both sides before it compares them.
	actuated := driftLabWireless()
	desired := driftLabWireless()
	desired.Interfaces[2].Wireless.Security = ""
	if diffs := NetworkDrift(desired, &actuated); len(diffs) != 0 {
		t.Errorf("an unset security means wpa-psk: %v", diffs)
	}
}

func TestNetworkDriftSeesARadioAddedToAWiredInterface(t *testing.T) {
	actuated := driftLabWireless()
	actuated.Interfaces[2].Wireless = nil
	diffs := NetworkDrift(driftLabWireless(), &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "stonypoint (wpa-psk) declared, (none) actuated") {
		t.Errorf("expected an added-wireless diff: %v", diffs)
	}
}

func TestNetworkDriftSeesARadioRemoved(t *testing.T) {
	actuated := driftLabWireless()
	desired := driftLabWireless()
	desired.Interfaces[2].Wireless = nil
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "wireless (none) declared, stonypoint (wpa-psk) actuated") {
		t.Errorf("expected a removed-wireless diff: %v", diffs)
	}
}

func TestModulesDriftIgnoresOrderAndRepetition(t *testing.T) {
	diffs := ModulesDrift([]string{"nvidia", "zram", "nvidia"}, []string{"zram", "nvidia"}, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("the lists are the same set: %v", diffs)
	}
}

// The set diff above answers what loads. This comparison answers in
// which order, which decides whether a codec's driver reaches the
// codec before the controller binds it to the generic driver.
func TestModuleOrderDrift(t *testing.T) {
	cases := []struct {
		name     string
		desired  []string
		actuated []string
		want     []string
	}{
		{
			name:     "the same order",
			desired:  []string{"i915", "snd_hda_intel"},
			actuated: []string{"i915", "snd_hda_intel"},
		},
		{
			name:     "nothing on either side",
			desired:  nil,
			actuated: []string{},
		},
		{
			name:     "a codec driver moved ahead of its controller",
			desired:  []string{"snd_hda_codec_hdmi", "snd_hda_codec_alc269", "snd_hda_intel"},
			actuated: []string{"snd_hda_codec_hdmi", "snd_hda_intel", "snd_hda_codec_alc269"},
			want:     []string{"modules: the declared order changed: snd_hda_codec_alc269 now loads before snd_hda_intel"},
		},
		{
			name:     "an added module is no reorder",
			desired:  []string{"i915", "btusb", "snd_hda_intel"},
			actuated: []string{"i915", "snd_hda_intel"},
		},
		{
			name:     "a retracted module is no reorder",
			desired:  []string{"i915", "snd_hda_intel"},
			actuated: []string{"i915", "btusb", "snd_hda_intel"},
		},
		{
			name:     "repetition carries no meaning",
			desired:  []string{"i915", "snd_hda_intel", "i915"},
			actuated: []string{"i915", "snd_hda_intel"},
		},
		{
			name:     "the earliest place that changed names the change",
			desired:  []string{"a", "b", "c"},
			actuated: []string{"c", "b", "a"},
			want:     []string{"modules: the declared order changed: a now loads before c"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if diffs := ModuleOrderDrift(c.desired, c.actuated); !slices.Equal(diffs, c.want) {
				t.Errorf("got %v, want %v", diffs, c.want)
			}
		})
	}
}

func TestModulesDriftTreatsNilAndEmptyAlike(t *testing.T) {
	if diffs := ModulesDrift(nil, []string{}, nil, nil); len(diffs) != 0 {
		t.Errorf("nothing declared, nothing actuated: %v", diffs)
	}
}

func TestModulesDriftSeesAnAddedModule(t *testing.T) {
	diffs := ModulesDrift([]string{"nvidia"}, nil, nil, nil)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "nvidia declared but this boot ran without it") {
		t.Errorf("got %v", diffs)
	}
}

func TestModulesDriftSeesARemovedModule(t *testing.T) {
	diffs := ModulesDrift(nil, []string{"zram"}, nil, nil)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "zram no longer declared") {
		t.Errorf("got %v", diffs)
	}
}
