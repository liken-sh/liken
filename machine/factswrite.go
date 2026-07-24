package machine

// The per-subtree writers. Each writer covers one subtree of the facts
// tree, and one init component calls it at the point where it discovers
// those facts. There is no writer for the whole tree, on purpose. A
// whole-tree writer would be a second owner of every file, and the
// tree exists so that each fact has one owner and needs no lock.
//
// A collection writer reconciles. It writes the element directories
// that the new set holds, then removes the element directories that the
// new set dropped. This one step gives a collection the same omitempty
// semantics that a scalar file gets from writeFact: what is no longer
// true loses its file.

import (
	"os"
	"path/filepath"
	"time"

	"github.com/liken-sh/liken/api"
)

// RejectionKind names one of the four standing quarantine records under
// boot/. Each names the document whose rejection it holds. The value is
// the record's directory name in the tree, which matches the field name
// in BootStatus.
type RejectionKind string

const (
	RejectMachine     RejectionKind = "rejection"
	RejectCluster     RejectionKind = "clusterRejection"
	RejectSystem      RejectionKind = "systemRejection"
	RejectCredentials RejectionKind = "credentialsRejection"
)

// WriteRole publishes the machine's role in its cluster: leader or
// follower. Boot derives it from the Cluster manifest's leaders list.
func (t FactsTree) WriteRole(role api.Role) error {
	return t.report(t.writeFact("role", string(role)))
}

// WriteBootTime publishes the moment the machine booted, derived from
// the kernel's uptime counter. It belongs to the boot record because it
// shares the record's lifetime: a reboot moves it, an in-place k3s
// restart does not.
func (t FactsTree) WriteBootTime(bootedAt *time.Time) error {
	return t.report(t.writeFact("boot/time", formatTime(bootedAt)))
}

// WriteLastCrash publishes the newest kernel crash the machine still
// holds records for. A nil crash means the machine holds no crash on
// record, so the whole subtree is removed.
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

// WriteVersion publishes the machine's version inventory: liken's own
// version, and every outside component the image carries.
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
		t.writeFact("version/systemdBoot", v.SystemdBoot),
		t.writeFact("version/grub", v.Grub),
		t.writeFact("version/hwdata", v.Hwdata),
		t.writeFact("version/linuxFirmware", v.LinuxFirmware),
		t.writeFact("version/microcode", v.Microcode),
		t.writeFact("version/microcodeRevision", v.MicrocodeRevision),
	))
}

// WriteNetwork publishes the outcome of the boot's networking. The
// top-level fields summarize the primary interface, and the interfaces
// collection carries the detail for each interface.
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
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, "network", "interfaces"), want))
}

// WriteTime publishes the state of the machine's clock. The clock loop
// owns this subtree and rewrites it as it disciplines the clock.
func (t FactsTree) WriteTime(ts TimeStatus) error {
	return t.report(firstError(
		t.writeFact("time/state", string(ts.State)),
		t.writeFact("time/source", ts.Source),
		t.writeFact("time/stratum", formatInt(ts.Stratum)),
		t.writeFact("time/offset", ts.Offset),
		t.writeFact("time/lastSync", formatTime(ts.LastSync)),
	))
}

// WriteHardwareBasics publishes the machine's processor count and total
// memory. The disks and the unclaimed devices have their own writers,
// because the hardware watch keeps them current after boot.
func (t FactsTree) WriteHardwareBasics(cpus int, memoryBytes uint64) error {
	return t.report(firstError(
		t.writeFact("hardware/cpus", formatInt(cpus)),
		t.writeFact("hardware/memoryBytes", formatUint(memoryBytes)),
	))
}

// WriteBlockDevices publishes the machine's storage inventory: every
// real disk the kernel found. The directory name is the kernel's name
// for the disk this boot.
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
		); err != nil {
			return t.report(err)
		}
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, "hardware", "blockDevices"), want))
}

// WriteUnclaimed publishes every device the kernel enumerated but that
// nothing drives. The directory name is safeKey(modalias), and the
// exact modalias lives in the modalias file inside. The reconcile
// removes a device's directory once a module claims it.
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

// WriteFirmware publishes the machine's standing firmware state: the
// mode it boots in, and the boot menu from its non-volatile store.
func (t FactsTree) WriteFirmware(f FirmwareStatus) error {
	return t.report(firstError(
		t.writeFact("firmware/mode", string(f.Mode)),
		t.writeFact("firmware/bootCurrent", f.BootCurrent),
		t.writeFact("firmware/bootNext", f.BootNext),
		t.writeListFact("firmware/bootOrder", f.BootOrder),
	))
}

// WriteStorage publishes where each storage role is actually backed
// this boot. The nine roles are a fixed set, so this writer needs no
// reconcile: a role that moves from a partition back to memory loses
// its device, partition, and capacity files through writeFact.
func (t FactsTree) WriteStorage(s StorageStatus) error {
	for _, name := range StorageRoleNames {
		role := s.Role(name)
		base := filepath.Join("storage", string(name))
		if err := firstError(
			t.writeFact(filepath.Join(base, "backing"), string(role.Backing)),
			t.writeFact(filepath.Join(base, "device"), role.Device),
			t.writeFact(filepath.Join(base, "partition"), role.Partition),
			t.writeFact(filepath.Join(base, "capacityBytes"), formatUint(role.CapacityBytes)),
		); err != nil {
			return t.report(err)
		}
	}
	return nil
}

// WriteModules publishes the outcome of every module named in
// spec.modules. The module loader owns this subtree.
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
		); err != nil {
			return t.report(err)
		}
	}
	return t.report(syncEntryDirs(filepath.Join(t.Dir, "modules"), want))
}

// WriteFeatures publishes this machine's standing on every feature the
// cluster document enables. The restart path owns this subtree, because
// a k3s restart can change a feature's outcome without a reboot.
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

// WriteRegistries publishes what this boot rendered into k3s's
// registries.yaml: the mirrored hosts, the credentialed hosts, and
// whether the embedded registry is on. It never carries credential
// material.
func (t FactsTree) WriteRegistries(r RegistriesStatus) error {
	return t.report(firstError(
		t.writeListFact("registries/mirrors", r.Mirrors),
		t.writeListFact("registries/credentialedHosts", r.CredentialedHosts),
		t.writeFact("registries/embedded", formatBool(r.Embedded)),
	))
}

// WriteBootSlot publishes the system slot this boot came from, A or B.
// It is empty when the boot did not come from a slot, as in a
// direct-kernel boot.
func (t FactsTree) WriteBootSlot(slot string) error {
	return t.report(t.writeFact("boot/slot", slot))
}

// WriteBootRestarts publishes the count of in-place k3s restarts this
// boot has performed. The restart path owns it, and it returns to zero
// on the next reboot.
func (t FactsTree) WriteBootRestarts(n int) error {
	return t.report(t.writeFact("boot/restarts", formatInt(n)))
}

// WriteBootModules publishes the module list the winning manifest
// declared, recorded as actuated regardless of each load's outcome.
func (t FactsTree) WriteBootModules(modules []string) error {
	return t.report(t.writeListFact("boot/modules", modules))
}

// WriteBootManifest publishes the Machine manifest this boot ran under.
// The module loader writes it last, because it is the commit point the
// operator's convergence judges.
func (t FactsTree) WriteBootManifest(src ManifestSource, hash string) error {
	return t.report(t.writeRecordFact("boot/manifest", [][2]string{
		{"source", string(src)},
		{"hash", hash},
	}))
}

// WriteBootClusterManifest publishes the Cluster manifest this boot ran
// under. The restart path writes it last, because promotion keys on it.
func (t FactsTree) WriteBootClusterManifest(src ManifestSource, hash string) error {
	return t.report(t.writeRecordFact("boot/clusterManifest", [][2]string{
		{"source", string(src)},
		{"hash", hash},
	}))
}

// WriteBootCredentials publishes the registry-credentials document this
// boot, or the latest k3s restart, rendered into registries.yaml.
func (t FactsTree) WriteBootCredentials(src ManifestSource, hash string) error {
	return t.report(t.writeRecordFact("boot/credentials", [][2]string{
		{"source", string(src)},
		{"hash", hash},
	}))
}

// WriteBootImports publishes the imported-images record this boot ran
// under. The discarded field records that this boot found a trial still
// standing from a boot that died unproven, and threw the container
// store away.
func (t FactsTree) WriteBootImports(src ManifestSource, hash string, discarded bool) error {
	return t.report(t.writeRecordFact("boot/imports", [][2]string{
		{"source", string(src)},
		{"hash", hash},
		{"discarded", formatBool(discarded)},
	}))
}

// WriteBootStorage publishes the storage the boot actuated, one
// directory for each declared role. Only declared roles appear, so the
// reconcile removes a role's directory once the spec drops it.
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

// WriteRejection publishes one of the four standing quarantine records.
// A nil rejection means the document has no standing rejection, so the
// whole record directory is removed.
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

// firstError returns the first non-nil error of a sequence of writes,
// or nil when all succeed. Go evaluates the arguments before the call,
// so every write in the sequence runs even when an early one fails.
// The writes are independent files, so the later writes stay correct,
// and the caller learns about the first failure.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
