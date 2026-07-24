package machine

// The read side of the facts tree. Only the operator reads the tree,
// and only the operator assembles a MachineStatus from it. Read walks
// every subtree that a writer fills, and returns the status that those
// files describe.
//
// Read leaves four fields at their zero value: phase,
// observedGeneration, sysctls, and conditions. The operator owns these
// fields and overlays them on every pass, because they are the
// operator's own observation, not init's.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/liken-sh/liken/api"
)

// Read assembles a MachineStatus from the facts tree. A missing root is
// an error that wraps fs.ErrNotExist, the same condition as a missing
// facts file: the facts describe a boot, and their absence on a running
// machine is a fact the operator must report, not a default it may
// invent. Read ignores a file or directory it does not know, so a tree
// can carry more than this reader consumes. A file that does not parse
// is an error that names its path.
func (t FactsTree) Read() (*MachineStatus, error) {
	if _, err := os.Stat(t.Dir); err != nil {
		return nil, fmt.Errorf("reading facts tree %s: %w", t.Dir, err)
	}

	s := &MachineStatus{}
	var err error

	if s.Role, err = t.readRole(); err != nil {
		return nil, err
	}
	if s.BootedAt, err = t.readTime("bootedAt"); err != nil {
		return nil, err
	}
	if s.LastCrash, err = t.readLastCrash(); err != nil {
		return nil, err
	}
	if s.Version, err = t.readVersion(); err != nil {
		return nil, err
	}
	if s.Network, err = t.readNetwork(); err != nil {
		return nil, err
	}
	if s.Time, err = t.readTimeStatus(); err != nil {
		return nil, err
	}
	if s.Hardware, err = t.readHardware(); err != nil {
		return nil, err
	}
	if s.Firmware, err = t.readFirmware(); err != nil {
		return nil, err
	}
	if s.Storage, err = t.readStorage(); err != nil {
		return nil, err
	}
	if s.Modules, err = t.readModules(); err != nil {
		return nil, err
	}
	if s.Features, err = t.readFeatures(); err != nil {
		return nil, err
	}
	if s.Registries, err = t.readRegistries(); err != nil {
		return nil, err
	}
	if s.Boot, err = t.readBoot(); err != nil {
		return nil, err
	}
	return s, nil
}

func (t FactsTree) readRole() (api.Role, error) {
	value, err := t.readFact("role")
	return api.Role(value), err
}

func (t FactsTree) readLastCrash() (*CrashStatus, error) {
	if _, err := os.Stat(filepath.Join(t.Dir, "lastCrash")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	c := &CrashStatus{}
	var err error
	if c.Time, err = t.readTime("lastCrash/time"); err != nil {
		return nil, err
	}
	reason, err := t.readFact("lastCrash/reason")
	if err != nil {
		return nil, err
	}
	c.Reason = CrashReason(reason)
	if c.Message, err = t.readFact("lastCrash/message"); err != nil {
		return nil, err
	}
	if c.Records, err = t.readFact("lastCrash/records"); err != nil {
		return nil, err
	}
	return c, nil
}

func (t FactsTree) readVersion() (VersionStatus, error) {
	v := VersionStatus{}
	fields := []struct {
		rel string
		dst *string
	}{
		{"version/liken", &v.Liken},
		{"version/kernel", &v.Kernel},
		{"version/xtables", &v.Xtables},
		{"version/k3s", &v.K3s},
		{"version/trust", &v.Trust},
		{"version/e2fsprogs", &v.E2fsprogs},
		{"version/openIscsi", &v.OpenISCSI},
		{"version/nfsUtils", &v.NFSUtils},
		{"version/systemdBoot", &v.SystemdBoot},
		{"version/grub", &v.Grub},
		{"version/hwdata", &v.Hwdata},
		{"version/linuxFirmware", &v.LinuxFirmware},
		{"version/microcode", &v.Microcode},
		{"version/microcodeRevision", &v.MicrocodeRevision},
	}
	for _, f := range fields {
		value, err := t.readFact(f.rel)
		if err != nil {
			return VersionStatus{}, err
		}
		*f.dst = value
	}
	return v, nil
}

func (t FactsTree) readNetwork() (NetworkStatus, error) {
	n := NetworkStatus{}
	var err error
	if n.Interface, err = t.readFact("network/interface"); err != nil {
		return NetworkStatus{}, err
	}
	if n.MAC, err = t.readFact("network/mac"); err != nil {
		return NetworkStatus{}, err
	}
	if n.Gateway, err = t.readFact("network/gateway"); err != nil {
		return NetworkStatus{}, err
	}
	if n.LeaseExpires, err = t.readTime("network/leaseExpires"); err != nil {
		return NetworkStatus{}, err
	}
	if n.Addresses, err = t.readListFact("network/addresses"); err != nil {
		return NetworkStatus{}, err
	}
	if n.Nameservers, err = t.readListFact("network/nameservers"); err != nil {
		return NetworkStatus{}, err
	}
	names, err := t.entryKeys("network/interfaces")
	if err != nil {
		return NetworkStatus{}, err
	}
	for _, name := range names {
		iface := InterfaceStatus{Name: name}
		base := filepath.Join("network", "interfaces", name)
		if iface.MAC, err = t.readFact(filepath.Join(base, "mac")); err != nil {
			return NetworkStatus{}, err
		}
		if iface.Address, err = t.readFact(filepath.Join(base, "address")); err != nil {
			return NetworkStatus{}, err
		}
		method, err := t.readFact(filepath.Join(base, "method"))
		if err != nil {
			return NetworkStatus{}, err
		}
		iface.Method = AddressMethod(method)
		if iface.Gateway, err = t.readFact(filepath.Join(base, "gateway")); err != nil {
			return NetworkStatus{}, err
		}
		if iface.LeaseExpires, err = t.readTime(filepath.Join(base, "leaseExpires")); err != nil {
			return NetworkStatus{}, err
		}
		if iface.Nameservers, err = t.readListFact(filepath.Join(base, "nameservers")); err != nil {
			return NetworkStatus{}, err
		}
		n.Interfaces = append(n.Interfaces, iface)
	}
	return n, nil
}

func (t FactsTree) readTimeStatus() (TimeStatus, error) {
	ts := TimeStatus{}
	state, err := t.readFact("time/state")
	if err != nil {
		return TimeStatus{}, err
	}
	ts.State = TimeState(state)
	if ts.Source, err = t.readFact("time/source"); err != nil {
		return TimeStatus{}, err
	}
	if ts.Stratum, err = t.readInt("time/stratum"); err != nil {
		return TimeStatus{}, err
	}
	if ts.Offset, err = t.readFact("time/offset"); err != nil {
		return TimeStatus{}, err
	}
	if ts.LastSync, err = t.readTime("time/lastSync"); err != nil {
		return TimeStatus{}, err
	}
	return ts, nil
}

func (t FactsTree) readHardware() (HardwareStatus, error) {
	h := HardwareStatus{}
	var err error
	if h.CPUs, err = t.readInt("hardware/cpus"); err != nil {
		return HardwareStatus{}, err
	}
	if h.MemoryBytes, err = t.readUint("hardware/memoryBytes"); err != nil {
		return HardwareStatus{}, err
	}
	if h.BlockDevices, err = t.readBlockDevices(); err != nil {
		return HardwareStatus{}, err
	}
	if h.Unclaimed, err = t.readUnclaimed(); err != nil {
		return HardwareStatus{}, err
	}
	return h, nil
}

func (t FactsTree) readBlockDevices() ([]BlockDevice, error) {
	names, err := t.entryKeys("hardware/blockDevices")
	if err != nil {
		return nil, err
	}
	var devices []BlockDevice
	for _, name := range names {
		d := BlockDevice{Name: name}
		base := filepath.Join("hardware", "blockDevices", name)
		if d.SizeBytes, err = t.readUint(filepath.Join(base, "sizeBytes")); err != nil {
			return nil, err
		}
		if d.Model, err = t.readFact(filepath.Join(base, "model")); err != nil {
			return nil, err
		}
		if d.Serial, err = t.readFact(filepath.Join(base, "serial")); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (t FactsTree) readUnclaimed() ([]UnclaimedDevice, error) {
	keys, err := t.entryKeys("hardware/unclaimed")
	if err != nil {
		return nil, err
	}
	var devices []UnclaimedDevice
	for _, key := range keys {
		d := UnclaimedDevice{}
		base := filepath.Join("hardware", "unclaimed", key)
		if d.Modalias, err = t.readFact(filepath.Join(base, "modalias")); err != nil {
			return nil, err
		}
		if d.Bus, err = t.readFact(filepath.Join(base, "bus")); err != nil {
			return nil, err
		}
		if d.Name, err = t.readFact(filepath.Join(base, "name")); err != nil {
			return nil, err
		}
		if d.Class, err = t.readFact(filepath.Join(base, "class")); err != nil {
			return nil, err
		}
		if d.Message, err = t.readFact(filepath.Join(base, "message")); err != nil {
			return nil, err
		}
		if d.Candidates, err = t.readListFact(filepath.Join(base, "candidates")); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (t FactsTree) readFirmware() (FirmwareStatus, error) {
	f := FirmwareStatus{}
	mode, err := t.readFact("firmware/mode")
	if err != nil {
		return FirmwareStatus{}, err
	}
	f.Mode = FirmwareMode(mode)
	if f.BootCurrent, err = t.readFact("firmware/bootCurrent"); err != nil {
		return FirmwareStatus{}, err
	}
	if f.BootNext, err = t.readFact("firmware/bootNext"); err != nil {
		return FirmwareStatus{}, err
	}
	if f.BootOrder, err = t.readListFact("firmware/bootOrder"); err != nil {
		return FirmwareStatus{}, err
	}
	return f, nil
}

func (t FactsTree) readStorage() (StorageStatus, error) {
	s := StorageStatus{}
	for _, name := range StorageRoleNames {
		role := s.Role(name)
		base := filepath.Join("storage", string(name))
		backing, err := t.readFact(filepath.Join(base, "backing"))
		if err != nil {
			return StorageStatus{}, err
		}
		role.Backing = Backing(backing)
		if role.Device, err = t.readFact(filepath.Join(base, "device")); err != nil {
			return StorageStatus{}, err
		}
		if role.Partition, err = t.readFact(filepath.Join(base, "partition")); err != nil {
			return StorageStatus{}, err
		}
		if role.CapacityBytes, err = t.readUint(filepath.Join(base, "capacityBytes")); err != nil {
			return StorageStatus{}, err
		}
	}
	return s, nil
}

func (t FactsTree) readModules() ([]ModuleStatus, error) {
	names, err := t.entryKeys("modules")
	if err != nil {
		return nil, err
	}
	var modules []ModuleStatus
	for _, name := range names {
		m := ModuleStatus{Name: name}
		base := filepath.Join("modules", name)
		state, err := t.readFact(filepath.Join(base, "state"))
		if err != nil {
			return nil, err
		}
		m.State = ModuleState(state)
		if m.Message, err = t.readFact(filepath.Join(base, "message")); err != nil {
			return nil, err
		}
		modules = append(modules, m)
	}
	return modules, nil
}

func (t FactsTree) readFeatures() ([]FeatureStatus, error) {
	names, err := t.entryKeys("features")
	if err != nil {
		return nil, err
	}
	var features []FeatureStatus
	for _, name := range names {
		f := FeatureStatus{Name: name}
		base := filepath.Join("features", name)
		state, err := t.readFact(filepath.Join(base, "state"))
		if err != nil {
			return nil, err
		}
		f.State = FeatureState(state)
		if f.Message, err = t.readFact(filepath.Join(base, "message")); err != nil {
			return nil, err
		}
		features = append(features, f)
	}
	return features, nil
}

func (t FactsTree) readRegistries() (RegistriesStatus, error) {
	r := RegistriesStatus{}
	var err error
	if r.Mirrors, err = t.readListFact("registries/mirrors"); err != nil {
		return RegistriesStatus{}, err
	}
	if r.CredentialedHosts, err = t.readListFact("registries/credentialedHosts"); err != nil {
		return RegistriesStatus{}, err
	}
	embedded, err := t.readFact("registries/embedded")
	if err != nil {
		return RegistriesStatus{}, err
	}
	r.Embedded = embedded == "true"
	return r, nil
}

// entryKeys returns the sorted names of the subdirectories under a
// collection directory. It is how the reader discovers a collection's
// elements. It ignores a plain file, so a stray file left beside the
// element directories does not become a phantom element. A directory
// that does not exist is an empty collection. The order is sorted, so
// a round trip through the tree returns the elements in one stable
// order.
func (t FactsTree) entryKeys(rel string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(t.Dir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, entry := range entries {
		if entry.IsDir() {
			keys = append(keys, entry.Name())
		}
	}
	sort.Strings(keys)
	return keys, nil
}
