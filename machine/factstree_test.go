package machine

import (
	"encoding/json"
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

	"github.com/liken-sh/liken/api"
)

// writeAll drives every per-subtree writer with the matching fields of
// a status, so a round-trip test can prove the codec whole. It lives in
// the test only, because production code must never gain a whole-tree
// writer: the tree exists so each fact has one owner.
func writeAll(t *testing.T, tree FactsTree, s *MachineStatus) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
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
	must(tree.WriteBootStorage(s.Boot.Storage))
	must(tree.WriteBootNetwork(s.Boot.Network))
	must(tree.WriteRejection(RejectMachine, s.Boot.Rejection))
	must(tree.WriteRejection(RejectCluster, s.Boot.ClusterRejection))
	must(tree.WriteRejection(RejectSystem, s.Boot.SystemRejection))
	must(tree.WriteRejection(RejectCredentials, s.Boot.CredentialsRejection))
}

// sortCollections sorts the directory-keyed collections by their key. A
// directory-keyed collection has no order on disk, so a reader returns
// its elements in sorted-key order. A fixture built in any order must
// match that, so the comparison sorts both sides the same way.
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
		t.Fatal(err)
	}
	return string(raw)
}

func everythingSet() *MachineStatus {
	booted := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	lease := booted.Add(time.Hour)
	synced := booted.Add(30 * time.Second)
	crashed := booted.Add(-5 * time.Minute)
	refused := booted.Add(-2 * time.Hour)
	rejected := booted.Add(-time.Hour)
	storage := AllRolesInMemory()
	storage.ClusterState = StorageRoleStatus{
		Backing: BackingPartition, Device: "vda1",
		Partition: "liken:clusterState", CapacityBytes: 2_146_435_072,
	}
	// A FAT role that the last stop did not release, so the round trip
	// covers the one storage fact that is a boolean.
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
			},
		},
		Time: TimeStatus{
			State: TimeSynchronized, Source: "pool.ntp.org",
			Stratum: 2, Offset: "1.28ms", LastSync: &synced,
		},
		Hardware: HardwareStatus{
			CPUs: 4, MemoryBytes: 4_294_967_296,
			BlockDevices: []BlockDevice{
				{Name: "vda", SizeBytes: 2_147_483_648, Serial: "liken-lab-state"},
				{Name: "sda", SizeBytes: 4_294_967_296, Model: "QEMU HARDDISK"},
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
		},
		Storage: storage,
		Modules: []ModuleStatus{
			{Name: "nvme", State: ModuleLoaded},
			{Name: "e1000e", State: ModuleMissing, Message: "not in image"},
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
		},
		Boot: BootStatus{
			Time:           &booted,
			ManifestSource: ManifestSourceProven, ManifestHash: "aaa",
			ClusterManifestSource: ManifestSourceStaged, ClusterManifestHash: "bbb",
			CredentialsSource: ManifestSourceProven, CredentialsHash: "ccc",
			ImportsSource: ManifestSourceStaged, ImportsHash: "ddd", ImportsDiscarded: true,
			Slot: "A", Restarts: 2,
			// A real command line, spaces and all, because it goes into
			// a scalar fact and a space is not a separator there.
			CommandLine: "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A panic=10",
			Modules:     []string{"nvme", "e1000e"},
			// One limit in each of the spec's three forms, so the round
			// trip covers a bare number, the word, and a pair.
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
		// A machine that declares no limits still holds the ones that
		// ship with the release, so status.rlimits is populated on a
		// machine whose boot record carries none.
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
			// A machine that declares no interface and takes the
			// zero-configuration default still records that it made
			// that choice.
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
				t.Fatal(err)
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

// WriteVersion and readVersion each name their fields by hand, so a
// new component reaches the Machine only when both lists grow. A
// field that one list forgot fails quietly: init reads the pin from
// the components record, the tree drops it, and the operator
// publishes a status that is missing a component with no error
// anywhere. This fills every field with a distinct value and demands
// all of them back.
func TestEveryVersionFieldSurvivesTheFactsTree(t *testing.T) {
	var want VersionStatus
	fields := reflect.ValueOf(&want).Elem()
	for i := range fields.NumField() {
		fields.Field(i).SetString(fmt.Sprintf("value-%d", i))
	}

	tree := FactsTree{Dir: t.TempDir()}
	if err := tree.WriteVersion(want); err != nil {
		t.Fatal(err)
	}
	got, err := tree.readVersion()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round trip changed the version:\nwant %+v\ngot  %+v", want, got)
	}
}

// The boot's network record must tell two states apart that every
// other fact in the tree is allowed to blur: a boot that declared no
// interface, and a boot that reported no network at all. The operator
// converges the first one and refuses to judge the second, so the
// tree has to carry the difference.
func TestBootNetworkTellsAnEmptySpecFromAnAbsentRecord(t *testing.T) {
	declaredNothing := FactsTree{Dir: t.TempDir()}
	if err := declaredNothing.WriteBootNetwork(&NetworkSpec{}); err != nil {
		t.Fatal(err)
	}
	facts, err := declaredNothing.Read()
	if err != nil {
		t.Fatal(err)
	}
	if facts.Boot.Network == nil {
		t.Error("a boot that declared no interface still recorded a network")
	}

	recordedNothing := FactsTree{Dir: t.TempDir()}
	if err := recordedNothing.WriteBootNetwork(nil); err != nil {
		t.Fatal(err)
	}
	facts, err = recordedNothing.Read()
	if err != nil {
		t.Fatal(err)
	}
	if facts.Boot.Network != nil {
		t.Errorf("a boot that recorded nothing must read as nothing: %+v", facts.Boot.Network)
	}
}

// A goGC file that does not hold an integer is a corrupt fact, and
// Read reports it as an error that names the path, rather than reading
// a silent zero. This is the same contract every integer fact holds.
func TestReadRejectsACorruptRuntimeGoGC(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	writeAll(t, tree, sparseFacts())
	if err := os.MkdirAll(filepath.Join(tree.Dir, "runtime", "k3s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.Dir, "runtime", "k3s", "goGC"), []byte("banana\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Read(); err == nil {
		t.Error("a non-integer goGC must be an error, not a silent zero")
	}
}

// The image collection policy holds the same contract: a threshold
// file that does not hold an integer is corrupt, and Read reports it
// rather than reading a percent of zero.
func TestReadRejectsACorruptImageGCThreshold(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	writeAll(t, tree, sparseFacts())
	dir := filepath.Join(tree.Dir, "runtime", "kubelet", "imageGC")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "highThresholdPercent"), []byte("most\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Read(); err == nil {
		t.Error("a non-integer threshold must be an error, not a silent zero")
	}
}

// listFiles returns the sorted relative paths of every regular file
// under root. The golden test uses it to pin the tree's shape.
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
		t.Fatal(err)
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
		// The boot's interfaces are keyed by position, counted from
		// zero, because the order of the declared list is part of
		// what the spec asks for.
		"boot/network/interfaces/0/name": "eth1\n",
		"network/addresses":              "10.0.2.15/24\n",
	}
	for rel, expected := range contents {
		raw, err := os.ReadFile(filepath.Join(tree.Dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != expected {
			t.Errorf("%s:\nwant %q\ngot  %q", rel, expected, string(raw))
		}
	}
}
