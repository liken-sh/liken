package machine

// The operator reads the facts tree that init writes and builds a
// MachineStatus from it. Read leaves phase, observedGeneration,
// sysctls, hostEntries, pending, and conditions empty, because the
// operator sets those fields itself on every pass.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/api"
)

// Read returns an error that wraps fs.ErrNotExist when the tree does
// not exist, because a running machine always has facts, and an empty
// status would hide that init never wrote them. Read ignores files it
// does not recognize. A file that does not parse returns an error with
// its path.
func (t FactsTree) Read() (*MachineStatus, error) {
	if _, err := os.Stat(t.Dir); err != nil {
		return nil, fmt.Errorf("reading the facts tree: %w", err)
	}

	s := &MachineStatus{}
	var err error

	if s.Role, err = t.readRole(); err != nil {
		return nil, err
	}
	if s.LastCrash, err = t.readLastCrash(); err != nil {
		return nil, err
	}
	if s.LastFailStop, err = t.readLastFailStop(); err != nil {
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
	// The operator reads sysctls from /proc/sys itself. Resource limits
	// are per process, and the limits that matter are init's, so only
	// init can report them.
	if s.Rlimits, err = t.readKeyedScalars("rlimits"); err != nil {
		return nil, err
	}
	if s.Storage, err = t.readStorage(); err != nil {
		return nil, err
	}
	if s.Modules, err = t.readModules(); err != nil {
		return nil, err
	}
	if s.Serio, err = t.readSerio(); err != nil {
		return nil, err
	}
	if s.Features, err = t.readFeatures(); err != nil {
		return nil, err
	}
	if s.Registries, err = t.readRegistries(); err != nil {
		return nil, err
	}
	if s.Runtime, err = t.readRuntime(); err != nil {
		return nil, err
	}
	if s.Boot, err = t.readBoot(); err != nil {
		return nil, err
	}
	return s, nil
}

// readKeyedScalars reads a directory that writeKeyedScalars wrote,
// one file per key, into a map. A missing or empty directory returns
// nil, so the omitempty field it fills stays out of the status.
func (t FactsTree) readKeyedScalars(rel string) (map[string]string, error) {
	entries, err := os.ReadDir(filepath.Join(t.Dir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// writeAtomic creates its temporary file here as .liken-*, and
		// a read can run before the rename.
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		value, err := t.readFact(filepath.Join(rel, entry.Name()))
		if err != nil {
			return nil, err
		}
		values[entry.Name()] = value
	}
	if len(values) == 0 {
		return nil, nil
	}
	return values, nil
}

func (t FactsTree) readRuntime() (RuntimeStatus, error) {
	r := RuntimeStatus{}
	var err error
	if r.K3s.GoMemoryLimit, err = t.readFact("runtime/k3s/goMemoryLimit"); err != nil {
		return RuntimeStatus{}, err
	}
	if r.K3s.GoGC, err = t.readInt("runtime/k3s/goGC"); err != nil {
		return RuntimeStatus{}, err
	}
	debug, err := t.readFact("runtime/k3s/debug")
	if err != nil {
		return RuntimeStatus{}, err
	}
	r.K3s.Debug = debug == "true"
	if r.Containerd.LogLevel, err = t.readFact("runtime/containerd/logLevel"); err != nil {
		return RuntimeStatus{}, err
	}
	exposeMetrics, err := t.readFact("runtime/etcd/exposeMetrics")
	if err != nil {
		return RuntimeStatus{}, err
	}
	r.Etcd.ExposeMetrics = exposeMetrics == "true"
	gc := &r.Kubelet.ImageGC
	if gc.HighThresholdPercent, err = t.readInt("runtime/kubelet/imageGC/highThresholdPercent"); err != nil {
		return RuntimeStatus{}, err
	}
	if gc.LowThresholdPercent, err = t.readInt("runtime/kubelet/imageGC/lowThresholdPercent"); err != nil {
		return RuntimeStatus{}, err
	}
	if gc.MaximumAge, err = t.readFact("runtime/kubelet/imageGC/maximumAge"); err != nil {
		return RuntimeStatus{}, err
	}
	if gc.MinimumAge, err = t.readFact("runtime/kubelet/imageGC/minimumAge"); err != nil {
		return RuntimeStatus{}, err
	}
	return r, nil
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

func (t FactsTree) readLastFailStop() (*FailStop, error) {
	if _, err := os.Stat(filepath.Join(t.Dir, "lastFailStop")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	f := &FailStop{}
	when, err := t.readTime("lastFailStop/time")
	if err != nil {
		return nil, err
	}
	if when != nil {
		f.Time = *when
	}
	if f.Reason, err = t.readFact("lastFailStop/reason"); err != nil {
		return nil, err
	}
	return f, nil
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
		{"version/wpaSupplicant", &v.WPASupplicant},
		{"version/systemdBoot", &v.SystemdBoot},
		{"version/grub", &v.Grub},
		{"version/hwdata", &v.Hwdata},
		{"version/tzdata", &v.Tzdata},
		{"version/linuxFirmware", &v.LinuxFirmware},
		{"version/wirelessRegdb", &v.WirelessRegdb},
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
		if iface.Wireless, err = t.readInterfaceWireless(base); err != nil {
			return NetworkStatus{}, err
		}
		n.Interfaces = append(n.Interfaces, iface)
	}
	return n, nil
}

// readInterfaceWireless returns nil for a wired interface, which has
// no ssid file.
func (t FactsTree) readInterfaceWireless(base string) (*WirelessStatus, error) {
	dir := filepath.Join(base, "wireless")
	ssid, err := t.readFact(filepath.Join(dir, "ssid"))
	if err != nil || ssid == "" {
		return nil, err
	}
	w := &WirelessStatus{SSID: ssid}
	state, err := t.readFact(filepath.Join(dir, "state"))
	if err != nil {
		return nil, err
	}
	w.State = WirelessState(state)
	if w.Message, err = t.readFact(filepath.Join(dir, "message")); err != nil {
		return nil, err
	}
	association, err := t.readRecordFact(filepath.Join(dir, "association"))
	if err != nil {
		return nil, err
	}
	w.BSSID = association["bssid"]
	w.Band = association["band"]
	path := filepath.Join(t.Dir, dir, "association")
	if w.Frequency, err = recordInt(association, "frequency", path); err != nil {
		return nil, err
	}
	if w.LastDisconnectReason, err = recordInt(association, "lastDisconnectReason", path); err != nil {
		return nil, err
	}
	if at := association["associatedAt"]; at != "" {
		parsed, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, fmt.Errorf("reading associatedAt in %s: %w", path, err)
		}
		w.AssociatedAt = &parsed
	}
	return w, nil
}

// recordInt returns zero for a missing field.
func recordInt(record map[string]string, key, path string) (int, error) {
	value := record[key]
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("reading %s in %s: %w", key, path, err)
	}
	return n, nil
}

func (t FactsTree) readTimeStatus() (TimeStatus, error) {
	record, err := t.readRecordFact("time/standing")
	if err != nil {
		return TimeStatus{}, err
	}
	ts := TimeStatus{
		State:  TimeState(record["state"]),
		Source: record["source"],
		Offset: record["offset"],
	}
	if stratum := record["stratum"]; stratum != "" {
		if ts.Stratum, err = strconv.Atoi(stratum); err != nil {
			return TimeStatus{}, fmt.Errorf("reading stratum in %s: %w", filepath.Join(t.Dir, "time", "standing"), err)
		}
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
		if d.StableNames, err = t.readListFact(filepath.Join(base, "stableNames")); err != nil {
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
	kept, err := t.readFact("firmware/bootOrderKept")
	if err != nil {
		return FirmwareStatus{}, err
	}
	f.BootOrderKept = parseOptionalBool(kept)
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
		unclean, err := t.readFact(filepath.Join(base, "lastStopUnclean"))
		if err != nil {
			return StorageStatus{}, err
		}
		role.LastStopUnclean = unclean == "true"
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
		resident, err := t.readFact(filepath.Join(base, "alreadyResident"))
		if err != nil {
			return nil, err
		}
		m.AlreadyResident = resident == "true"
		if m.Parameters, err = t.readKeyedScalars(filepath.Join(base, "parameters")); err != nil {
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

// entryKeys returns the sorted names of the subdirectories of rel,
// one for each element of a collection. It ignores plain files, so a
// stray file does not become an element.
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
