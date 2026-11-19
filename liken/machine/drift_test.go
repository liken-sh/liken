package machine

import (
	"slices"
	"strings"
	"testing"
)

// driftLabStorage returns the storage of a lab machine: five roles on
// two disks.
func driftLabStorage() StorageSpec {
	return StorageSpec{
		MachineState:     &StorageRole{Device: "/dev/vda", Size: "64Mi"},
		MachineEphemeral: &StorageRole{Device: "/dev/vdb", Size: "512Mi"},
		ClusterState:     &StorageRole{Device: "/dev/vda"},
		PodStorage:       &StorageRole{Device: "/dev/vdb", Size: "2Gi"},
		PodEphemeral:     &StorageRole{Device: "/dev/vdb"},
	}
}

func TestStorageDriftReportsNoDriftInTheSameSpec(t *testing.T) {
	if diffs := StorageDrift(driftLabStorage(), driftLabStorage()); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none for identical specs", diffs)
	}
}

func TestStorageDriftNormalizesSizes(t *testing.T) {
	desired := driftLabStorage()
	desired.PodStorage.Size = "2048Mi" // the same size as 2Gi, written differently
	if diffs := StorageDrift(desired, driftLabStorage()); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none because 2048Mi and 2Gi are the same size", diffs)
	}
}

func TestStorageDriftReportsAGrow(t *testing.T) {
	desired := driftLabStorage()
	desired.PodStorage.Size = "3Gi"
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "podStorage") {
		t.Errorf("the diffs are %v, want one diff that names podStorage", diffs)
	}
}

func TestStorageDriftReportsAnAddedRole(t *testing.T) {
	booted := driftLabStorage()
	booted.PodStorage = nil
	diffs := StorageDrift(driftLabStorage(), booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "declared but this boot ran without it") {
		t.Errorf("the diffs are %v, want one diff that says declared but this boot ran without it", diffs)
	}
}

func TestStorageDriftReportsARemovedRole(t *testing.T) {
	desired := driftLabStorage()
	desired.PodEphemeral = nil
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "no longer declared") {
		t.Errorf("the diffs are %v, want one diff that says no longer declared", diffs)
	}
}

func TestStorageDriftReportsADeviceChange(t *testing.T) {
	desired := driftLabStorage()
	desired.ClusterState.Device = "/dev/vdc"
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "device") {
		t.Errorf("the diffs are %v, want one diff that names the device", diffs)
	}
}

func TestStorageDriftFallsBackToStringsForUnparseableSizes(t *testing.T) {
	// Validation refuses these sizes, so the comparison falls back to
	// the strings.
	desired := driftLabStorage()
	desired.PodStorage.Size = "a-whole-bunch"
	booted := driftLabStorage()
	booted.PodStorage.Size = "a-whole-bunch"
	if diffs := StorageDrift(desired, booted); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none for identical spellings, parseable or not", diffs)
	}
	booted.PodStorage.Size = "even-more"
	if diffs := StorageDrift(desired, booted); len(diffs) != 1 {
		t.Errorf("the diffs are %v, want one diff for different spellings", diffs)
	}
}

func TestStorageDriftNamesTheRemainder(t *testing.T) {
	// A role that takes the rest of its disk has an empty size, and
	// the diff must show "(remainder)" in place of a blank.
	desired := driftLabStorage()
	desired.ClusterState.Size = "3Gi" // the booted clusterState has no size
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "(remainder)") {
		t.Errorf("the diffs are %v, want one diff that contains (remainder)", diffs)
	}
}

// driftLabNetwork returns the network of a lab machine: one
// interface on DHCP and one with a static address.
func driftLabNetwork() NetworkSpec {
	return NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth1", Address: "10.10.0.1/24"},
	}}
}

func TestNetworkDriftReportsNoDriftInTheSameSpec(t *testing.T) {
	booted := driftLabNetwork()
	if diffs := NetworkDrift(driftLabNetwork(), &booted); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none for identical specs", diffs)
	}
}

func TestNetworkDriftTreatsTwoEmptySpecsAsConverged(t *testing.T) {
	// If two empty specs drifted, every machine that declares no
	// network would request a reboot after every boot.
	if diffs := NetworkDrift(NetworkSpec{}, &NetworkSpec{}); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none when nothing is declared or in this boot", diffs)
	}
}

func TestNetworkDriftReportsNothingWithoutABootRecord(t *testing.T) {
	if diffs := NetworkDrift(driftLabNetwork(), nil); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none when no network is recorded", diffs)
	}
}

func TestNetworkDriftReportsAnAddedInterface(t *testing.T) {
	booted := NetworkSpec{Interfaces: driftLabNetwork().Interfaces[:1]}
	diffs := NetworkDrift(driftLabNetwork(), &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "eth1 declared but this boot ran without it") {
		t.Errorf("the diffs are %v, want one diff for the added interface eth1", diffs)
	}
}

func TestNetworkDriftReportsARemovedInterface(t *testing.T) {
	booted := driftLabNetwork()
	desired := NetworkSpec{Interfaces: driftLabNetwork().Interfaces[:1]}
	diffs := NetworkDrift(desired, &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "no longer declared but this boot ran with it") {
		t.Errorf("the diffs are %v, want one diff for the removed interface", diffs)
	}
}

func TestNetworkDriftReportsAChangedAddress(t *testing.T) {
	booted := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[1].Address = "10.10.0.9/24"
	diffs := NetworkDrift(desired, &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "address 10.10.0.9/24 declared, 10.10.0.1/24 in this boot") {
		t.Errorf("the diffs are %v, want one diff for the changed address", diffs)
	}
}

func TestNetworkDriftNamesTheDHCPDefault(t *testing.T) {
	// An interface with no address uses DHCP, and the diff must say
	// so in place of a blank.
	booted := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[0].Address = "192.168.1.5/24"
	diffs := NetworkDrift(desired, &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "(DHCP) in this boot") {
		t.Errorf("the diffs are %v, want one diff that contains (DHCP)", diffs)
	}
}

func TestNetworkDriftReportsAChangedNameserverList(t *testing.T) {
	// The lists are compared in order, because nameserver order is
	// preference order.
	booted := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[0].Nameservers = []string{"10.10.0.1"}
	diffs := NetworkDrift(desired, &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "nameservers 10.10.0.1 declared, (none) in this boot") {
		t.Errorf("the diffs are %v, want one diff for the changed nameservers", diffs)
	}
}

func TestNetworkDriftReportsAChangedGateway(t *testing.T) {
	booted := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[1].Gateway = "10.10.0.254"
	diffs := NetworkDrift(desired, &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "gateway 10.10.0.254 declared, (none) in this boot") {
		t.Errorf("the diffs are %v, want one diff for the changed gateway", diffs)
	}
}

func TestNetworkDriftReportsAPortSwapOnce(t *testing.T) {
	booted := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[1] = InterfaceSpec{Name: "eth2", Address: "10.10.0.9/24"}
	diffs := NetworkDrift(desired, &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "interface 2: eth2 declared, eth1 in this boot") {
		t.Errorf("the diffs are %v, want one diff for the changed interface name", diffs)
	}
}

func TestNetworkDriftReportsAReorderedList(t *testing.T) {
	// init writes each interface's nameservers to resolv.conf in
	// interface order, so a reorder is a change.
	booted := driftLabNetwork()
	desired := NetworkSpec{Interfaces: []InterfaceSpec{
		booted.Interfaces[1], booted.Interfaces[0],
	}}
	if diffs := NetworkDrift(desired, &booted); len(diffs) != 2 {
		t.Errorf("the diffs are %v, want one for each of the two reordered positions", diffs)
	}
}

func TestNetworkDriftIgnoresHostEntries(t *testing.T) {
	desired := NetworkSpec{HostEntries: []HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}}
	booted := NetworkSpec{HostEntries: []HostEntry{{Address: "10.10.0.21", Names: []string{"printer"}}}}
	if diffs := NetworkDrift(desired, &booted); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none because host entries never drift", diffs)
	}
}

// driftLabWireless returns driftLabNetwork with a wireless interface
// that has a static address.
func driftLabWireless() NetworkSpec {
	spec := driftLabNetwork()
	spec.Interfaces = append(spec.Interfaces, InterfaceSpec{
		Name: "wlan0", Address: "10.10.0.2/24",
		Wireless: &WirelessSpec{SSID: "homenet", Security: WirelessWPAPSK},
	})
	return spec
}

func TestNetworkDriftReportsNoDriftInTheSameWirelessSpec(t *testing.T) {
	booted := driftLabWireless()
	if diffs := NetworkDrift(driftLabWireless(), &booted); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none for identical specs", diffs)
	}
}

func TestNetworkDriftReportsAChangedSSID(t *testing.T) {
	booted := driftLabWireless()
	desired := driftLabWireless()
	desired.Interfaces[2].Wireless.SSID = "homenet-guest"
	diffs := NetworkDrift(desired, &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "wireless homenet-guest (wpa-psk) declared, homenet (wpa-psk) in this boot") {
		t.Errorf("the diffs are %v, want one diff for the changed SSID", diffs)
	}
}

func TestNetworkDriftReportsAChangedSecurity(t *testing.T) {
	booted := driftLabWireless()
	desired := driftLabWireless()
	desired.Interfaces[2].Wireless.Security = WirelessOpen
	diffs := NetworkDrift(desired, &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "homenet (open) declared, homenet (wpa-psk) in this boot") {
		t.Errorf("the diffs are %v, want one diff for the changed security", diffs)
	}
}

func TestNetworkDriftReadsTheUnsetSecurityAsItsDefault(t *testing.T) {
	// The API server writes the default security into a spec, but a
	// manifest from an install stick leaves the field empty.
	booted := driftLabWireless()
	desired := driftLabWireless()
	desired.Interfaces[2].Wireless.Security = ""
	if diffs := NetworkDrift(desired, &booted); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none because an unset security means wpa-psk", diffs)
	}
}

func TestNetworkDriftReportsARadioAddedToAWiredInterface(t *testing.T) {
	booted := driftLabWireless()
	booted.Interfaces[2].Wireless = nil
	diffs := NetworkDrift(driftLabWireless(), &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "homenet (wpa-psk) declared, (none) in this boot") {
		t.Errorf("the diffs are %v, want one diff for the added wireless settings", diffs)
	}
}

func TestNetworkDriftReportsARadioRemoved(t *testing.T) {
	booted := driftLabWireless()
	desired := driftLabWireless()
	desired.Interfaces[2].Wireless = nil
	diffs := NetworkDrift(desired, &booted)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "wireless (none) declared, homenet (wpa-psk) in this boot") {
		t.Errorf("the diffs are %v, want one diff for the removed wireless settings", diffs)
	}
}

func TestModulesDriftIgnoresOrderAndRepetition(t *testing.T) {
	diffs := ModulesDrift([]string{"nvidia", "zram", "nvidia"}, []string{"zram", "nvidia"}, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none because the lists have the same members", diffs)
	}
}

func TestModuleOrderDrift(t *testing.T) {
	cases := []struct {
		name    string
		desired []string
		booted  []string
		want    []string
	}{
		{
			name:    "the same order",
			desired: []string{"i915", "snd_hda_intel"},
			booted:  []string{"i915", "snd_hda_intel"},
		},
		{
			name:    "nothing on either side",
			desired: nil,
			booted:  []string{},
		},
		{
			name:    "a codec driver moved ahead of its controller",
			desired: []string{"snd_hda_codec_hdmi", "snd_hda_codec_alc269", "snd_hda_intel"},
			booted:  []string{"snd_hda_codec_hdmi", "snd_hda_intel", "snd_hda_codec_alc269"},
			want:    []string{"modules: the declared order changed: snd_hda_codec_alc269 now loads before snd_hda_intel"},
		},
		{
			name:    "an added module is no reorder",
			desired: []string{"i915", "btusb", "snd_hda_intel"},
			booted:  []string{"i915", "snd_hda_intel"},
		},
		{
			name:    "a retracted module is no reorder",
			desired: []string{"i915", "snd_hda_intel"},
			booted:  []string{"i915", "btusb", "snd_hda_intel"},
		},
		{
			name:    "a repeated module does not change the order",
			desired: []string{"i915", "snd_hda_intel", "i915"},
			booted:  []string{"i915", "snd_hda_intel"},
		},
		{
			name:    "the earliest place that changed names the change",
			desired: []string{"a", "b", "c"},
			booted:  []string{"c", "b", "a"},
			want:    []string{"modules: the declared order changed: a now loads before c"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if diffs := ModuleOrderDrift(c.desired, c.booted); !slices.Equal(diffs, c.want) {
				t.Errorf("the diffs are %v, want %v", diffs, c.want)
			}
		})
	}
}

func TestModulesDriftTreatsNilAndEmptyAlike(t *testing.T) {
	if diffs := ModulesDrift(nil, []string{}, nil, nil); len(diffs) != 0 {
		t.Errorf("the diffs are %v, want none when nothing is declared or in this boot", diffs)
	}
}

func TestModulesDriftReportsAnAddedModule(t *testing.T) {
	diffs := ModulesDrift([]string{"nvidia"}, nil, nil, nil)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "nvidia declared but this boot ran without it") {
		t.Errorf("the diffs are %v, want one diff that contains nvidia declared but this boot ran without it", diffs)
	}
}

func TestModulesDriftReportsARemovedModule(t *testing.T) {
	diffs := ModulesDrift(nil, []string{"zram"}, nil, nil)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "zram no longer declared") {
		t.Errorf("the diffs are %v, want one diff that contains zram no longer declared", diffs)
	}
}
