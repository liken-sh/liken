package machine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
)

// writeAll calls each subtree's writer with the matching fields of a
// status, so a round-trip test covers the whole tree. It is a test
// helper only, because in production each subtree has one owner.
func writeAll(t *testing.T, tree FactsTree, s *MachineStatus) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("writing the facts tree: %v", err)
		}
	}
	must(tree.WriteRole(s.Role))
	must(tree.WriteLastCrash(s.LastCrash))
	must(tree.WriteLastFailStop(s.LastFailStop))
	must(tree.WriteVersion(s.Version))
	must(tree.WriteNetwork(s.Network))
	must(tree.WriteTime(s.Time))
	must(tree.WriteHardwareBasics(s.Hardware.CPUs, s.Hardware.MemoryBytes))
	must(tree.WriteBlockDevices(s.Hardware.BlockDevices))
	must(tree.WriteUnclaimed(s.Hardware.Unclaimed))
	must(tree.WriteFirmware(s.Firmware))
	must(tree.WriteStorage(s.Storage))
	must(tree.WriteModules(s.Modules))
	must(tree.WriteSerio(s.Serio))
	must(tree.WriteFeatures(s.Features))
	must(tree.WriteRegistries(s.Registries))
	must(tree.WriteRuntime(s.Runtime))
	must(tree.WriteRlimits(s.Rlimits))
	must(tree.WriteBootTime(s.Boot.Time))
	must(tree.WriteBootManifest(s.Boot.ManifestSource, s.Boot.ManifestHash))
	must(tree.WriteBootClusterManifest(s.Boot.ClusterManifestSource, s.Boot.ClusterManifestHash))
	must(tree.WriteBootCredentials(s.Boot.CredentialsSource, s.Boot.CredentialsHash))
	must(tree.WriteBootImports(s.Boot.ImportsSource, s.Boot.ImportsHash, s.Boot.ImportsDiscarded))
	must(tree.WriteBootSlot(s.Boot.Slot))
	must(tree.WriteBootCommandLine(s.Boot.CommandLine))
	must(tree.WriteBootRestarts(s.Boot.Restarts))
	must(tree.WriteBootModules(s.Boot.Modules))
	must(tree.WriteBootRlimits(s.Boot.Rlimits))
	must(tree.WriteBootModuleParameters(s.Boot.ModuleParameters))
	must(tree.WriteBootSerio(s.Boot.Serio))
	must(tree.WriteBootStorage(s.Boot.Storage))
	must(tree.WriteBootNetwork(s.Boot.Network))
	must(tree.WriteRejection(RejectMachine, s.Boot.Rejection))
	must(tree.WriteRejection(RejectCluster, s.Boot.ClusterRejection))
	must(tree.WriteRejection(RejectSystem, s.Boot.SystemRejection))
	must(tree.WriteRejection(RejectCredentials, s.Boot.CredentialsRejection))
}

// sortCollections sorts each collection that the tree stores as a
// directory by its key. A directory has no order on disk, so the test
// sorts both sides and a fixture can list its elements in any order.
func sortCollections(s *MachineStatus) {
	slices.SortFunc(s.Hardware.BlockDevices, func(a, b BlockDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(s.Hardware.Unclaimed, func(a, b UnclaimedDevice) int {
		return strings.Compare(safeKey(a.Modalias), safeKey(b.Modalias))
	})
	slices.SortFunc(s.Network.Interfaces, func(a, b InterfaceStatus) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(s.Modules, func(a, b ModuleStatus) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(s.Features, func(a, b FeatureStatus) int {
		return strings.Compare(a.Name, b.Name)
	})
}

func jsonBytes(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling the facts: %v", err)
	}
	return string(raw)
}

func everythingSet() *MachineStatus {
	booted := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	lease := booted.Add(time.Hour)
	crashed := booted.Add(-5 * time.Minute)
	refused := booted.Add(-2 * time.Hour)
	rejected := booted.Add(-time.Hour)
	storage := AllRolesInMemory()
	storage.ClusterState = StorageRoleStatus{
		Backing: BackingPartition, Device: "vda1",
		Partition: "liken:clusterState", CapacityBytes: 2_146_435_072,
	}
	// A role on a FAT partition that the last stop did not release.
	storage.SystemB = StorageRoleStatus{
		Backing: BackingPartition, Device: "vdc4",
		Partition: "liken:systemB", CapacityBytes: 1_073_741_824,
		LastStopUnclean: true,
	}
	return &MachineStatus{
		Role: api.RoleLeader,
		LastCrash: &CrashStatus{
			Time: &crashed, Reason: CrashPanic,
			Message: "Kernel panic - not syncing: test",
			Records: "/var/lib/liken/machine/crash/0001",
		},
		LastFailStop: &FailStop{
			Reason: "machine identity: /etc/liken/cluster.yaml: spec.registries.mirrors: registry.example lists no endpoints",
			Time:   refused,
		},
		Version: VersionStatus{
			Liken: "0.1.0", Kernel: "6.15.4", Xtables: "v1.8.11 (legacy)",
			K3s: "v1.31.0+k3s1", Trust: "1.0", E2fsprogs: "1.47",
			OpenISCSI: "2.1", NFSUtils: "2.6", SystemdBoot: "256",
			Grub: "2.12", Hwdata: "0.380", Tzdata: "2026c",
			LinuxFirmware: "20260101",
			Microcode:     "20260101", MicrocodeRevision: "0xf0",
		},
		Network: NetworkStatus{
			Interface: "eth0", MAC: "52:54:00:12:34:56",
			Addresses: []string{"10.0.2.15/24"}, Gateway: "10.0.2.2",
			Nameservers: []string{"10.0.2.3"}, LeaseExpires: &lease,
			Interfaces: []InterfaceStatus{
				{
					Name: "eth0", MAC: "52:54:00:12:34:56", Address: "10.0.2.15/24",
					Method: MethodDHCP, Gateway: "10.0.2.2",
					Nameservers: []string{"10.0.2.3"}, LeaseExpires: &lease,
				},
				{Name: "eth1", MAC: "52:54:00:ab:cd:ef", Address: "192.168.1.10/24", Method: MethodStatic},
				// wlan0 joined its network. wlan1 did not, so it has no
				// address, and its Message gives the reason.
				{
					Name: "wlan0", MAC: "04:4a:2c:11:22:33", Address: "192.168.1.11/24", Method: MethodStatic,
					Wireless: &WirelessStatus{
						SSID: "homenet", State: WirelessConnected,
						BSSID: "04:4a:2c:aa:bb:cc", Frequency: 5180, Band: "5GHz",
						AssociatedAt: &booted, LastDisconnectReason: 4,
					},
				},
				{
					Name: "wlan1", MAC: "04:4a:2c:11:22:34",
					Wireless: &WirelessStatus{
						SSID: "homenet-5", State: WirelessWrongKey,
						Message: "the access point refused the passphrase (WRONG_KEY)",
					},
				},
			},
		},
		Time: TimeStatus{
			State: TimeSynchronized, Source: "pool.ntp.org",
			Stratum: 2, Offset: "±5ms",
		},
		Hardware: HardwareStatus{
			CPUs: 4, MemoryBytes: 4_294_967_296,
			BlockDevices: []BlockDevice{
				{Name: "vda", SizeBytes: 2_147_483_648, Serial: "liken-lab-state"},
				{
					Name: "sda", SizeBytes: 4_294_967_296, Model: "QEMU HARDDISK",
					StableNames: []string{
						"/dev/disk/by-id/wwn-0x5002538d40a45c88",
						"/dev/disk/by-id/scsi-35002538d40a45c88",
						"/dev/disk/by-path/pci-0000:00:1f.2-ata-3",
					},
					MaxRequestSize: "128Ki", MaxHardwareRequestSize: "32767Ki",
				},
			},
			Unclaimed: []UnclaimedDevice{
				{
					Modalias: "pci:v00001234d00005678sv00000000sd00000000bc02sc00i00",
					Bus:      "pci", Name: "Acme NIC", Class: "network",
					Candidates: []string{"acme"}, Message: "declare acme",
				},
				{Modalias: "usb:v1/2 weird device", Bus: "usb", Name: "Weird"},
				{Modalias: strings.Repeat("a", 300), Bus: "pci"},
			},
		},
		Firmware: FirmwareStatus{
			Mode: FirmwareUEFI, BootCurrent: "Boot0001", BootNext: "Boot0002",
			BootOrder: []string{"Boot0001", "Boot0002"},
			// False is a different fact from absent: the firmware lost
			// the boot order.
			BootOrderKept: new(bool),
		},
		Storage: storage,
		Modules: []ModuleStatus{
			{Name: "nvme", State: ModuleLoaded},
			{Name: "e1000e", State: ModuleMissing, Message: "not in image"},
			// A module with parameters read back from the kernel, and
			// already resident before init loaded it.
			{
				Name: "snd_hda_intel", State: ModuleLoaded, AlreadyResident: true,
				Parameters: map[string]string{"power_save": "1", "enable_msi": "Y"},
			},
		},
		// An attached adapter with its device nodes, an adapter that
		// the kernel refused to attach, and an entry that no serial
		// line matches.
		Serio: []SerioStatus{
			{
				Protocol: "pulse8-cec", USB: SerioUSB{Vendor: "2548", Product: "1002"},
				TTY: "ttyACM0", Port: "serio0", State: SerioAttached,
				Nodes: []string{"/dev/cec0", "/dev/input/event14"},
			},
			{
				Protocol: "pulse8-cec", USB: SerioUSB{Vendor: "2548", Product: "1002"},
				TTY: "ttyACM1", State: SerioRefused, Message: "TIOCSETD: operation not permitted",
			},
			{
				Protocol: "rainshadow-cec", USB: SerioUSB{Vendor: "0000", Product: "0001", Serial: "RS-7"},
				State: SerioMissing, Message: "no USB device 0000:0001 is plugged in",
			},
		},
		Features: []FeatureStatus{
			{Name: "gpu", State: FeatureActive},
			{Name: "sr-iov", State: FeatureFailed, Message: "module refused"},
		},
		Registries: RegistriesStatus{
			Mirrors: []string{"docker.io", "*"}, CredentialedHosts: []string{"ghcr.io"}, Embedded: true,
		},
		Runtime: RuntimeStatus{
			K3s: K3sRuntimeStatus{GoMemoryLimit: "448Mi", GoGC: 50, Debug: true},
			Kubelet: KubeletRuntimeStatus{ImageGC: ImageGCStatus{
				HighThresholdPercent: 70, LowThresholdPercent: 60,
				MinimumAge: "5m", MaximumAge: "168h",
			}},
			Containerd: ContainerdRuntimeStatus{LogLevel: "warn"},
			Etcd:       EtcdRuntimeStatus{ExposeMetrics: true},
		},
		Boot: BootStatus{
			Time:           &booted,
			ManifestSource: ManifestSourceProven, ManifestHash: "aaa",
			ClusterManifestSource: ManifestSourceStaged, ClusterManifestHash: "bbb",
			CredentialsSource: ManifestSourceProven, CredentialsHash: "ccc",
			ImportsSource: ManifestSourceStaged, ImportsHash: "ddd", ImportsDiscarded: true,
			Slot: "A", Restarts: 2,
			// A scalar fact is one line, so the spaces in a command line
			// must not split it.
			CommandLine: "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A panic=10",
			Modules:     []string{"nvme", "e1000e", "snd_hda_intel"},
			Serio: []SerioAttachment{
				{Protocol: "pulse8-cec", USB: SerioUSB{Vendor: "2548", Product: "1002"}},
				{Protocol: "rainshadow-cec", USB: SerioUSB{Vendor: "0000", Product: "0001", Serial: "RS-7"}},
			},
			// The declared parameters, keyed module.parameter. The
			// kernel reads back a boolean declared as "1" as "Y".
			ModuleParameters: map[string]string{
				"snd_hda_intel.power_save": "1",
				"snd_hda_intel.enable_msi": "1",
			},
			// One limit in each form that the spec accepts: a number,
			// the word infinity, and a soft:hard pair.
			Rlimits: map[string]string{
				"nofile": "1048576",
				"core":   "infinity",
				"nproc":  "1024:infinity",
			},
			Storage: StorageSpec{
				ClusterState: &StorageRole{Device: "/dev/vda", Size: "2Gi"},
				PodStorage:   &StorageRole{Device: "/dev/vdb"},
			},
			Network: &NetworkSpec{Interfaces: []InterfaceSpec{
				{Name: "eth0"},
				{
					Name:    "eth1",
					Address: "192.168.1.10/24", Gateway: "192.168.1.1",
					Nameservers: []string{"192.168.1.1", "10.0.2.3"},
				},
				{
					Name: "wlan0", Address: "192.168.1.11/24",
					Wireless: &WirelessSpec{SSID: "homenet", Security: WirelessWPAPSK},
				},
			}},
			Rejection:            &Rejection{Hash: "r1", Reason: "bad spec", RejectedAt: rejected},
			ClusterRejection:     &Rejection{Hash: "r2", Reason: "bad cluster", RejectedAt: rejected},
			SystemRejection:      &Rejection{Hash: "r3", Reason: "fell back", RejectedAt: rejected},
			CredentialsRejection: &Rejection{Reason: "unparseable", RejectedAt: rejected},
		},
	}
}

func sparseFacts() *MachineStatus {
	booted := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	lease := booted.Add(time.Hour)
	return &MachineStatus{
		Role:    api.RoleFollower,
		Version: VersionStatus{Liken: "0.1.0", Kernel: "6.15.4"},
		// A machine that declares no limits still has the limits that
		// ship with the release, so status.rlimits is not empty when
		// the boot record has none.
		Rlimits: map[string]string{"nofile": "1048576", "nproc": "infinity"},
		Network: NetworkStatus{
			Interface: "eth0", MAC: "52:54:00:12:34:56",
			Addresses: []string{"10.0.2.15/24"}, Gateway: "10.0.2.2",
			Nameservers: []string{"10.0.2.3"}, LeaseExpires: &lease,
			Interfaces: []InterfaceStatus{
				{
					Name: "eth0", MAC: "52:54:00:12:34:56", Address: "10.0.2.15/24",
					Method: MethodDHCP, Gateway: "10.0.2.2",
					Nameservers: []string{"10.0.2.3"}, LeaseExpires: &lease,
				},
			},
		},
		Storage: AllRolesInMemory(),
		Boot: BootStatus{
			Time: &booted, ManifestSource: ManifestSourceSeed, ManifestHash: "seed", Slot: "A",
			// A machine that declares no interface uses the default
			// network setup, and the boot still records that.
			Network: &NetworkSpec{},
		},
	}
}

func nilPointerFacts() *MachineStatus {
	return &MachineStatus{
		Role:    api.RoleFollower,
		Version: VersionStatus{Liken: "0.1.0"},
		Storage: AllRolesInMemory(),
	}
}

func TestFactsTreeRoundTrip(t *testing.T) {
	cases := map[string]*MachineStatus{
		"everythingSet": everythingSet(),
		"sparse":        sparseFacts(),
		"nilPointer":    nilPointerFacts(),
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			tree := FactsTree{Dir: t.TempDir()}
			writeAll(t, tree, want)
			got, err := tree.Read()
			if err != nil {
				t.Fatalf("reading the facts tree: %v", err)
			}
			sortCollections(want)
			sortCollections(got)
			wantJSON, gotJSON := jsonBytes(t, want), jsonBytes(t, got)
			if wantJSON != gotJSON {
				t.Errorf("round trip changed the facts:\nwant %s\ngot  %s", wantJSON, gotJSON)
			}
		})
	}
}

// WriteVersion and readVersion list the fields of VersionStatus by
// hand. A field missing from either list drops that component's
// version from the Machine's status with no error, so this test sets
// every field and requires each one back.
func TestEveryVersionFieldReadsBackFromTheFactsTree(t *testing.T) {
	var want VersionStatus
	fields := reflect.ValueOf(&want).Elem()
	for i := range fields.NumField() {
		fields.Field(i).SetString(fmt.Sprintf("value-%d", i))
	}

	tree := FactsTree{Dir: t.TempDir()}
	if err := tree.WriteVersion(want); err != nil {
		t.Fatalf("writing the version: %v", err)
	}
	got, err := tree.readVersion()
	if err != nil {
		t.Fatalf("reading the version: %v", err)
	}
	if got != want {
		t.Errorf("round trip changed the version:\nwant %+v\ngot  %+v", want, got)
	}
}

// A boot that declared no interface and a boot that recorded no
// network must read back differently. The operator converges the
// network of the first and reports no drift for the second.
func TestBootNetworkDistinguishesAnEmptySpecFromAnAbsentRecord(t *testing.T) {
	declaredNothing := FactsTree{Dir: t.TempDir()}
	if err := declaredNothing.WriteBootNetwork(&NetworkSpec{}); err != nil {
		t.Fatalf("writing the boot network: %v", err)
	}
	facts, err := declaredNothing.Read()
	if err != nil {
		t.Fatalf("reading the facts tree: %v", err)
	}
	if facts.Boot.Network == nil {
		t.Error("Boot.Network is nil, want a network recorded for a boot that declared no interface")
	}

	recordedNothing := FactsTree{Dir: t.TempDir()}
	if err := recordedNothing.WriteBootNetwork(nil); err != nil {
		t.Fatalf("writing the boot network: %v", err)
	}
	facts, err = recordedNothing.Read()
	if err != nil {
		t.Fatalf("reading the facts tree: %v", err)
	}
	if facts.Boot.Network != nil {
		t.Errorf("Boot.Network is %+v, want nil for a boot that recorded nothing", facts.Boot.Network)
	}
}

// When the new spec declares no radio, rewriting the boot record
// removes the old radio's record, so the old SSID does not remain.
func TestBootNetworkDropsAWirelessRecordThatIsNoLongerDeclared(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	wireless := &NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "wlan0", Wireless: &WirelessSpec{SSID: "homenet", Security: WirelessOpen}},
	}}
	if err := tree.WriteBootNetwork(wireless); err != nil {
		t.Fatalf("writing the boot network: %v", err)
	}
	if err := tree.WriteBootNetwork(&NetworkSpec{Interfaces: []InterfaceSpec{{Name: "wlan0"}}}); err != nil {
		t.Fatalf("writing the boot network: %v", err)
	}
	facts, err := tree.Read()
	if err != nil {
		t.Fatalf("reading the facts tree: %v", err)
	}
	if got := facts.Boot.Network.Interfaces[0].Wireless; got != nil {
		t.Errorf("the record is %+v, want no radio", got)
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, "boot", "network", "interfaces", "0", "wireless")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the wireless directory exists (stat error %v), want it removed", err)
	}
}

func TestReadRejectsACorruptRuntimeGoGC(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	writeAll(t, tree, sparseFacts())
	if err := os.MkdirAll(filepath.Join(tree.Dir, "runtime", "k3s"), 0o755); err != nil {
		t.Fatalf("creating the runtime directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tree.Dir, "runtime", "k3s", "goGC"), []byte("banana\n"), 0o644); err != nil {
		t.Fatalf("writing the goGC file: %v", err)
	}
	if _, err := tree.Read(); err == nil {
		t.Error("Read accepted a goGC that is not an integer, want an error")
	}
}

func TestReadRejectsACorruptImageGCThreshold(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	writeAll(t, tree, sparseFacts())
	dir := filepath.Join(tree.Dir, "runtime", "kubelet", "imageGC")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the imageGC directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "highThresholdPercent"), []byte("most\n"), 0o644); err != nil {
		t.Fatalf("writing the threshold file: %v", err)
	}
	if _, err := tree.Read(); err == nil {
		t.Error("Read accepted a highThresholdPercent that is not an integer, want an error")
	}
}

// listFiles returns the sorted paths of the files under root, relative
// to root.
func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the facts tree: %v", err)
	}
	sort.Strings(files)
	return files
}

func TestFactsTreeGoldenLayout(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	writeAll(t, tree, &MachineStatus{
		Role:    api.RoleLeader,
		Version: VersionStatus{Liken: "0.1.0"},
		Network: NetworkStatus{Interface: "eth0", Addresses: []string{"10.0.2.15/24"}},
		Boot: BootStatus{
			ManifestSource: ManifestSourceProven, ManifestHash: "abc",
			Network: &NetworkSpec{Interfaces: []InterfaceSpec{{Name: "eth1", Address: "10.10.0.1/24"}}},
		},
	})

	want := []string{
		"boot/manifest",
		"boot/network/interfaces/0/address",
		"boot/network/interfaces/0/name",
		"network/addresses",
		"network/interface",
		"role",
		"version/liken",
	}
	if got := listFiles(t, tree.Dir); !slices.Equal(got, want) {
		t.Errorf("tree layout:\nwant %v\ngot  %v", want, got)
	}

	contents := map[string]string{
		"boot/manifest": "source=Proven\nhash=abc\n",
		"role":          "leader\n",
		// The boot's interfaces are keyed by their position in the
		// declared list, because the order of that list matters.
		"boot/network/interfaces/0/name": "eth1\n",
		"network/addresses":              "10.0.2.15/24\n",
	}
	for rel, expected := range contents {
		raw, err := os.ReadFile(filepath.Join(tree.Dir, rel))
		if err != nil {
			t.Fatalf("reading the file: %v", err)
		}
		if string(raw) != expected {
			t.Errorf("%s:\nwant %q\ngot  %q", rel, expected, string(raw))
		}
	}
}
