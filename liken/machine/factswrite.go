package machine

// The writers of the facts tree. Each writer writes one subtree, and
// the part of init that discovers those facts calls it. Each subtree
// has one owner, so the tree needs no lock.
//
// A writer for a collection writes a directory for each element of the
// new set, then removes the directories of elements that are no longer
// in it. So a fact that stops being true loses its file, as an empty
// scalar does in writeFact.

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/liken-sh/liken/liken/api"
)

// RejectionKind selects which document's rejection record under boot/
// to write. The value is the record's directory name, which is also the
// field name in BootStatus.
type RejectionKind string

const (
	RejectMachine     RejectionKind = "rejection"
	RejectCluster     RejectionKind = "clusterRejection"
	RejectSystem      RejectionKind = "systemRejection"
	RejectCredentials RejectionKind = "credentialsRejection"
)

// WriteRole writes the machine's role in its cluster, leader or
// follower.
func (t FactsTree) WriteRole(role api.Role) error {
	return t.report(t.writeFact("role", string(role)))
}

// WriteBootTime writes the time the machine booted, computed from the
// kernel's uptime counter. It is part of the boot record because a
// reboot changes it and an in-place k3s restart does not.
func (t FactsTree) WriteBootTime(bootedAt *time.Time) error {
	return t.report(t.writeFact("boot/time", formatTime(bootedAt)))
}

// WriteLastCrash writes the newest kernel crash that the machine still
// has records for. A nil crash removes the subtree.
func (t FactsTree) WriteLastCrash(c *CrashStatus) error {
	if c == nil {
		return t.report(os.RemoveAll(filepath.Join(t.Dir, "lastCrash")))
	}
	return t.report(firstError(
		t.writeFact("lastCrash/time", formatTime(c.Time)),
		t.writeFact("lastCrash/reason", string(c.Reason)),
		t.writeFact("lastCrash/message", c.Message),
		t.writeFact("lastCrash/records", c.Records),
	))
}

// WriteLastFailStop writes the last boot that this machine refused to
// run. A nil record removes the subtree.
func (t FactsTree) WriteLastFailStop(f *FailStop) error {
	if f == nil {
		return t.report(os.RemoveAll(filepath.Join(t.Dir, "lastFailStop")))
	}
	return t.report(firstError(
		t.writeFact("lastFailStop/time", formatTime(&f.Time)),
		t.writeFact("lastFailStop/reason", f.Reason),
	))
}

func (t FactsTree) WriteVersion(v VersionStatus) error {
	return t.report(firstError(
		t.writeFact("version/liken", v.Liken),
		t.writeFact("version/kernel", v.Kernel),
		t.writeFact("version/xtables", v.Xtables),
		t.writeFact("version/k3s", v.K3s),
		t.writeFact("version/trust", v.Trust),
		t.writeFact("version/e2fsprogs", v.E2fsprogs),
		t.writeFact("version/openIscsi", v.OpenISCSI),
		t.writeFact("version/nfsUtils", v.NFSUtils),
		t.writeFact("version/wpaSupplicant", v.WPASupplicant),
		t.writeFact("version/systemdBoot", v.SystemdBoot),
		t.writeFact("version/grub", v.Grub),
		t.writeFact("version/hwdata", v.Hwdata),
		t.writeFact("version/tzdata", v.Tzdata),
		t.writeFact("version/linuxFirmware", v.LinuxFirmware),
		t.writeFact("version/wirelessRegdb", v.WirelessRegdb),
		t.writeFact("version/microcode", v.Microcode),
		t.writeFact("version/microcodeRevision", v.MicrocodeRevision),
	))
}

// WriteNetwork writes the result of the boot's network setup. The
// top-level files describe the primary interface, and network/interfaces
// has one directory for each interface.
func (t FactsTree) WriteNetwork(n NetworkStatus) error {
	if err := firstError(
		t.writeFact("network/interface", n.Interface),
		t.writeFact("network/mac", n.MAC),
		t.writeFact("network/gateway", n.Gateway),
		t.writeFact("network/leaseExpires", formatTime(n.LeaseExpires)),
		t.writeListFact("network/addresses", n.Addresses),
		t.writeListFact("network/nameservers", n.Nameservers),
	); err != nil {
		return t.report(err)
	}
	want := map[string]bool{}
	for _, iface := range n.Interfaces {
		if err := assertKey("interface", iface.Name); err != nil {
			return t.report(err)
		}
		want[iface.Name] = true
		base := filepath.Join("network", "interfaces", iface.Name)
		if err := os.MkdirAll(filepath.Join(t.Dir, base), 0o755); err != nil {
			return t.report(err)
		}
		if err := firstError(
			t.writeFact(filepath.Join(base, "mac"), iface.MAC),
			t.writeFact(filepath.Join(base, "address"), iface.Address),
			t.writeFact(filepath.Join(base, "method"), string(iface.Method)),
			t.writeFact(filepath.Join(base, "gateway"), iface.Gateway),
			t.writeFact(filepath.Join(base, "leaseExpires"), formatTime(iface.LeaseExpires)),
			t.writeListFact(filepath.Join(base, "nameservers"), iface.Nameservers),
		); err != nil {
			return t.report(err)
		}
		if err := t.writeInterfaceWireless(base, iface.Wireless); err != nil {
			return t.report(err)
		}
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, "network", "interfaces"), want))
}

// writeInterfaceWireless writes the radio's status for one interface.
// A nil status removes the wireless directory, so a wired interface has
// none.
func (t FactsTree) writeInterfaceWireless(base string, w *WirelessStatus) error {
	dir := filepath.Join(base, "wireless")
	if w == nil {
		return os.RemoveAll(filepath.Join(t.Dir, dir))
	}
	// Init rewrites the association while the machine runs, and its
	// fields change together, so they are one record file. It is
	// written before the state, so a reader that sees a new state also
	// reads the access point of that state.
	return firstError(
		t.writeRecordFact(filepath.Join(dir, "association"), [][2]string{
			{"bssid", w.BSSID},
			{"frequency", formatInt(w.Frequency)},
			{"band", w.Band},
			{"associatedAt", formatTime(w.AssociatedAt)},
			{"lastDisconnectReason", formatInt(w.LastDisconnectReason)},
		}),
		t.writeFact(filepath.Join(dir, "ssid"), w.SSID),
		t.writeFact(filepath.Join(dir, "state"), string(w.State)),
		t.writeFact(filepath.Join(dir, "message"), w.Message),
	)
}

// WriteTime writes the state of the machine's clock to the record file
// time/standing. Init rewrites it while the machine runs, so the fields
// are one record file that one rename replaces. Each write then causes one inotify event and one
// reconcile pass, not one for each field.
func (t FactsTree) WriteTime(ts TimeStatus) error {
	return t.report(t.writeRecordFact("time/standing", [][2]string{
		{"state", string(ts.State)},
		{"source", ts.Source},
		{"stratum", formatInt(ts.Stratum)},
		{"offset", ts.Offset},
	}))
}

// WriteHardwareBasics writes the processor count and total memory. The
// disks and unclaimed devices have their own writers, because the
// hardware watch updates them after boot.
func (t FactsTree) WriteHardwareBasics(cpus int, memoryBytes uint64) error {
	return t.report(firstError(
		t.writeFact("hardware/cpus", formatInt(cpus)),
		t.writeFact("hardware/memoryBytes", formatUint(memoryBytes)),
	))
}

// WriteBlockDevices writes one directory for each real disk the kernel
// found, named by the kernel's name for the disk in this boot.
func (t FactsTree) WriteBlockDevices(devices []BlockDevice) error {
	want := map[string]bool{}
	for _, d := range devices {
		if err := assertKey("blockDevice", d.Name); err != nil {
			return t.report(err)
		}
		want[d.Name] = true
		base := filepath.Join("hardware", "blockDevices", d.Name)
		if err := os.MkdirAll(filepath.Join(t.Dir, base), 0o755); err != nil {
			return t.report(err)
		}
		if err := firstError(
			t.writeFact(filepath.Join(base, "sizeBytes"), formatUint(d.SizeBytes)),
			t.writeFact(filepath.Join(base, "model"), d.Model),
			t.writeFact(filepath.Join(base, "serial"), d.Serial),
			t.writeListFact(filepath.Join(base, "stableNames"), d.StableNames),
			t.writeFact(filepath.Join(base, "maxRequestSize"), d.MaxRequestSize),
			t.writeFact(filepath.Join(base, "maxHardwareRequestSize"), d.MaxHardwareRequestSize),
		); err != nil {
			return t.report(err)
		}
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, "hardware", "blockDevices"), want))
}

// WriteUnclaimed writes one directory for each device that the kernel
// found and no driver has bound. The directory name is
// safeKey(modalias), so the modalias file inside has the exact value.
func (t FactsTree) WriteUnclaimed(devices []UnclaimedDevice) error {
	want := map[string]bool{}
	for _, d := range devices {
		key := safeKey(d.Modalias)
		want[key] = true
		base := filepath.Join("hardware", "unclaimed", key)
		if err := os.MkdirAll(filepath.Join(t.Dir, base), 0o755); err != nil {
			return t.report(err)
		}
		if err := firstError(
			t.writeFact(filepath.Join(base, "modalias"), d.Modalias),
			t.writeFact(filepath.Join(base, "bus"), d.Bus),
			t.writeFact(filepath.Join(base, "name"), d.Name),
			t.writeFact(filepath.Join(base, "class"), d.Class),
			t.writeFact(filepath.Join(base, "message"), d.Message),
			t.writeListFact(filepath.Join(base, "candidates"), d.Candidates),
		); err != nil {
			return t.report(err)
		}
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, "hardware", "unclaimed"), want))
}

// WriteFirmware writes the firmware mode the machine booted in and the
// boot entries from the firmware's non-volatile store.
func (t FactsTree) WriteFirmware(f FirmwareStatus) error {
	return t.report(firstError(
		t.writeFact("firmware/mode", string(f.Mode)),
		t.writeFact("firmware/bootCurrent", f.BootCurrent),
		t.writeFact("firmware/bootNext", f.BootNext),
		t.writeListFact("firmware/bootOrder", f.BootOrder),
		t.writeFact("firmware/bootOrderKept", formatOptionalBool(f.BootOrderKept)),
	))
}

// WriteStorage writes what backs each storage role in this boot. The
// roles are the fixed StorageRoleNames, so no directory is removed. A
// role that moves from a partition back to memory loses its device,
// partition, and capacity files, because their values are empty.
func (t FactsTree) WriteStorage(s StorageStatus) error {
	for _, name := range StorageRoleNames {
		role := s.Role(name)
		base := filepath.Join("storage", string(name))
		if err := firstError(
			t.writeFact(filepath.Join(base, "backing"), string(role.Backing)),
			t.writeFact(filepath.Join(base, "device"), role.Device),
			t.writeFact(filepath.Join(base, "partition"), role.Partition),
			t.writeFact(filepath.Join(base, "capacityBytes"), formatUint(role.CapacityBytes)),
			t.writeFact(filepath.Join(base, "lastStopUnclean"), formatBool(role.LastStopUnclean)),
		); err != nil {
			return t.report(err)
		}
	}
	return nil
}

// WriteModules writes the result of loading each module in
// spec.modules. Init writes it at boot, and after that moduleLoader in
// init rewrites it on each live load.
func (t FactsTree) WriteModules(modules []ModuleStatus) error {
	want := map[string]bool{}
	for _, m := range modules {
		if err := assertKey("module", m.Name); err != nil {
			return t.report(err)
		}
		want[m.Name] = true
		base := filepath.Join("modules", m.Name)
		if err := os.MkdirAll(filepath.Join(t.Dir, base), 0o755); err != nil {
			return t.report(err)
		}
		if err := firstError(
			t.writeFact(filepath.Join(base, "state"), string(m.State)),
			t.writeFact(filepath.Join(base, "message"), m.Message),
			t.writeFact(filepath.Join(base, "alreadyResident"), formatBool(m.AlreadyResident)),
			t.writeKeyedScalars(filepath.Join(base, "parameters"), m.Parameters),
		); err != nil {
			return t.report(err)
		}
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, "modules"), want))
}

// WriteRlimits writes the resource limits of init, read back from the
// kernel. Each resource is one file with the limit in the spec's
// syntax.
func (t FactsTree) WriteRlimits(rlimits map[string]string) error {
	return t.report(t.writeKeyedScalars("rlimits", rlimits))
}

// WriteBootRlimits writes the resource limits that the manifest of
// this boot declared. Drift detection compares the spec with them, so
// they are the declared values and not what the kernel reports, as in
// boot/modules and boot/storage.
func (t FactsTree) WriteBootRlimits(rlimits map[string]string) error {
	return t.report(t.writeKeyedScalars("boot/rlimits", rlimits))
}

// writeKeyedScalars writes a map as a directory with one file for each
// key. An empty map leaves no directory.
func (t FactsTree) writeKeyedScalars(dir string, values map[string]string) error {
	want := map[string]bool{}
	for key := range values {
		if err := assertKey(dir, key); err != nil {
			return err
		}
		want[key] = true
	}
	if len(want) > 0 {
		if err := os.MkdirAll(filepath.Join(t.Dir, dir), 0o755); err != nil {
			return err
		}
	}
	for key, value := range values {
		if err := t.writeFact(filepath.Join(dir, key), value); err != nil {
			return err
		}
	}
	return syncEntryFiles(filepath.Join(t.Dir, dir), want)
}

// WriteFeatures writes the state of each feature that the Cluster
// document enables. An in-place k3s restart rewrites it, because the
// restart can change a feature's result without a reboot.
func (t FactsTree) WriteFeatures(features []FeatureStatus) error {
	want := map[string]bool{}
	for _, f := range features {
		if err := assertKey("feature", f.Name); err != nil {
			return t.report(err)
		}
		want[f.Name] = true
		base := filepath.Join("features", f.Name)
		if err := os.MkdirAll(filepath.Join(t.Dir, base), 0o755); err != nil {
			return t.report(err)
		}
		if err := firstError(
			t.writeFact(filepath.Join(base, "state"), string(f.State)),
			t.writeFact(filepath.Join(base, "message"), f.Message),
		); err != nil {
			return t.report(err)
		}
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, "features"), want))
}

// WriteRegistries writes what this boot put in the registries.yaml of
// k3s: the mirrored hosts, the hosts that have credentials, and whether
// the embedded registry is on. It writes no credentials.
func (t FactsTree) WriteRegistries(r RegistriesStatus) error {
	return t.report(firstError(
		t.writeListFact("registries/mirrors", r.Mirrors),
		t.writeListFact("registries/credentialedHosts", r.CredentialedHosts),
		t.writeFact("registries/embedded", formatBool(r.Embedded)),
	))
}

// WriteRuntime writes the k3s, kubelet, and containerd settings that
// init applied, as resolved values and not the strings of the spec. An
// in-place k3s restart rewrites it, because the restart can change any
// of them without a reboot. A setting that the Cluster does not set has
// no file, and the reader supplies the default.
func (t FactsTree) WriteRuntime(r RuntimeStatus) error {
	gc := r.Kubelet.ImageGC
	return t.report(firstError(
		t.writeFact("runtime/k3s/goMemoryLimit", r.K3s.GoMemoryLimit),
		t.writeFact("runtime/k3s/goGC", formatInt(r.K3s.GoGC)),
		t.writeFact("runtime/k3s/debug", formatBool(r.K3s.Debug)),
		t.writeFact("runtime/containerd/logLevel", r.Containerd.LogLevel),
		t.writeFact("runtime/etcd/exposeMetrics", formatBool(r.Etcd.ExposeMetrics)),
		t.writeFact("runtime/kubelet/imageGC/highThresholdPercent", formatInt(gc.HighThresholdPercent)),
		t.writeFact("runtime/kubelet/imageGC/lowThresholdPercent", formatInt(gc.LowThresholdPercent)),
		t.writeFact("runtime/kubelet/imageGC/maximumAge", gc.MaximumAge),
		t.writeFact("runtime/kubelet/imageGC/minimumAge", gc.MinimumAge),
	))
}

// WriteBootSlot writes the system slot that this boot started from, A
// or B. A direct kernel boot has no slot, so the file is absent.
func (t FactsTree) WriteBootSlot(slot string) error {
	return t.report(t.writeFact("boot/slot", slot))
}

// WriteBootCommandLine writes the kernel command line of this boot,
// for a reader that has no console to read it from.
func (t FactsTree) WriteBootCommandLine(cmdline string) error {
	return t.report(t.writeFact("boot/commandLine", cmdline))
}

// WriteBootRestarts writes how many in-place k3s restarts this boot has
// done. A reboot sets the count to zero.
func (t FactsTree) WriteBootRestarts(n int) error {
	return t.report(t.writeFact("boot/restarts", formatInt(n)))
}

// WriteBootModules writes the modules that count as applied, whether
// or not each load succeeded. The list is in the order that the machine
// loaded them, because the operator compares that order with the
// declared one.
func (t FactsTree) WriteBootModules(modules []string) error {
	return t.report(t.writeListFact("boot/modules", modules))
}

// WriteBootModuleParameters writes the module parameters that count as
// applied. A boot writes the whole declared map. A live load writes
// only the parameters it set, and leaves out those of a module that was
// already loaded, so they show as drift until a reboot sets them. The
// files under modules/ report what the kernel has.
func (t FactsTree) WriteBootModuleParameters(parameters map[string]string) error {
	return t.report(t.writeKeyedScalars("boot/moduleParameters", parameters))
}

// WriteBootManifest writes which Machine manifest this boot used. A
// live load writes it after the module records, because the operator
// reads a match of this record as convergence.
func (t FactsTree) WriteBootManifest(src ManifestSource, hash string) error {
	return t.report(t.writeRecordFact("boot/manifest", [][2]string{
		{"source", string(src)},
		{"hash", hash},
	}))
}

// WriteBootClusterManifest writes which Cluster manifest this boot
// used. An in-place k3s restart writes it after the features and
// registries, because the operator promotes the staged Cluster
// document when this record matches it.
func (t FactsTree) WriteBootClusterManifest(src ManifestSource, hash string) error {
	return t.report(t.writeRecordFact("boot/clusterManifest", [][2]string{
		{"source", string(src)},
		{"hash", hash},
	}))
}

// WriteBootCredentials writes which registry credentials document this
// boot, or its latest k3s restart, put in registries.yaml.
func (t FactsTree) WriteBootCredentials(src ManifestSource, hash string) error {
	return t.report(t.writeRecordFact("boot/credentials", [][2]string{
		{"source", string(src)},
		{"hash", hash},
	}))
}

// WriteBootImports writes which imported-images record this boot used.
// discarded is true when an earlier boot died before its imports were
// proven, so this boot deleted the container store.
func (t FactsTree) WriteBootImports(src ManifestSource, hash string, discarded bool) error {
	return t.report(t.writeRecordFact("boot/imports", [][2]string{
		{"source", string(src)},
		{"hash", hash},
		{"discarded", formatBool(discarded)},
	}))
}

// WriteBootStorage writes the storage that the boot set up, with one
// directory for each declared role.
func (t FactsTree) WriteBootStorage(spec StorageSpec) error {
	want := map[string]bool{}
	for _, role := range spec.Roles() {
		name := string(role.Name)
		want[name] = true
		base := filepath.Join("boot", "storage", name)
		if err := os.MkdirAll(filepath.Join(t.Dir, base), 0o755); err != nil {
			return t.report(err)
		}
		if err := firstError(
			t.writeFact(filepath.Join(base, "device"), role.Device),
			t.writeFact(filepath.Join(base, "size"), role.Size),
		); err != nil {
			return t.report(err)
		}
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, "boot", "storage"), want))
}

// WriteBootNetwork writes the network that the boot set up, with one
// directory for each declared interface. A nil spec removes the
// subtree.
//
// boot/network exists even when the spec declares no interface,
// because a machine that declares no interface and a boot that recorded
// nothing are different facts. Drift can be checked only for the first
// (see drift.go).
//
// Each interface's directory is named by its index in the declared
// list, not by its port name. The order is part of the declaration,
// because the nameservers reach resolv.conf in that order, and drift.go
// compares interfaces by index.
//
// Host entries have no boot record, because the machine operator
// applies spec.network.hostEntries to /etc/hosts while the machine
// runs. status.hostEntries has the current value, as status.sysctls
// does for sysctls.
func (t FactsTree) WriteBootNetwork(spec *NetworkSpec) error {
	base := filepath.Join("boot", "network")
	if spec == nil {
		return t.report(os.RemoveAll(filepath.Join(t.Dir, base)))
	}
	interfaces := filepath.Join(base, "interfaces")
	if err := os.MkdirAll(filepath.Join(t.Dir, interfaces), 0o755); err != nil {
		return t.report(err)
	}
	want := map[string]bool{}
	for i, ifc := range spec.Interfaces {
		key := strconv.Itoa(i)
		want[key] = true
		dir := filepath.Join(interfaces, key)
		if err := os.MkdirAll(filepath.Join(t.Dir, dir), 0o755); err != nil {
			return t.report(err)
		}
		if err := firstError(
			t.writeFact(filepath.Join(dir, "name"), ifc.Name),
			t.writeFact(filepath.Join(dir, "address"), ifc.Address),
			t.writeFact(filepath.Join(dir, "gateway"), ifc.Gateway),
			t.writeListFact(filepath.Join(dir, "nameservers"), ifc.Nameservers),
		); err != nil {
			return t.report(err)
		}
		if err := t.writeBootWireless(dir, ifc.Wireless); err != nil {
			return t.report(err)
		}
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, interfaces), want))
}

// writeBootWireless writes the wireless part of one interface's boot
// record. A nil spec removes the wireless directory, so the SSID of a
// network that the spec no longer declares does not stay in the tree.
func (t FactsTree) writeBootWireless(dir string, w *WirelessSpec) error {
	base := filepath.Join(dir, "wireless")
	if w == nil {
		return os.RemoveAll(filepath.Join(t.Dir, base))
	}
	return firstError(
		t.writeFact(filepath.Join(base, "ssid"), w.SSID),
		t.writeFact(filepath.Join(base, "security"), string(w.Security)),
	)
}

// WriteRejection writes the record of a rejected document of the given
// kind. A nil rejection removes the record.
func (t FactsTree) WriteRejection(kind RejectionKind, r *Rejection) error {
	base := filepath.Join("boot", string(kind))
	if r == nil {
		return t.report(os.RemoveAll(filepath.Join(t.Dir, base)))
	}
	if err := os.MkdirAll(filepath.Join(t.Dir, base), 0o755); err != nil {
		return t.report(err)
	}
	rejectedAt := r.RejectedAt
	return t.report(firstError(
		t.writeFact(filepath.Join(base, "hash"), r.Hash),
		t.writeFact(filepath.Join(base, "reason"), r.Reason),
		t.writeFact(filepath.Join(base, "rejectedAt"), formatTime(&rejectedAt)),
	))
}

// firstError returns the first non-nil error of its arguments. Go
// evaluates every argument before the call, so each write in the list
// runs even when an earlier one fails. Each write is its own file, so
// the later writes are still correct.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
