package hardware

// The unclaimed report: judging which undriven devices are worth an
// operator's attention, and saying what would fix each one.
//
// Not every driverless device is a problem. Sysfs is full of
// devices that legitimately have no driver (bridges, stubs,
// firmware furniture), and a report that listed them would bury the
// one entry that matters — the plugged-in disk nobody can use —
// under noise nobody can act on. The filter is actionability: a
// device appears exactly when some loadable module in the kernel
// build could drive it, because that is precisely when a
// spec.modules edit (or a release that ships the module) would
// change something.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// Catalog is everything needed to judge a device: the kernel
// build's complete alias table (which modules could drive it), the
// shipped set (which of those this image carries), the builtin set
// (which drivers are resident in vmlinuz and need no loading), and
// optionally the PCI naming database. Loaded once at boot; the
// judgments re-run whenever the hardware changes.
type Catalog struct {
	Aliases *AliasTable
	Shipped map[string]bool
	Builtin map[string]bool
	PCI     *PCIIDs
}

// LoadCatalog assembles the catalog from a module directory
// (/lib/modules/<release>) and a pci.ids path. Only the alias table
// is essential — without it no candidate can be named and the whole
// report would be silence — so only its absence is an error; the
// naming database degrades to numeric IDs.
func LoadCatalog(moduleDir, pciIDsPath string) (*Catalog, error) {
	aliases, err := LoadAliasTable(filepath.Join(moduleDir, "modules.alias"))
	if err != nil {
		return nil, err
	}
	c := &Catalog{
		Aliases: aliases,
		Shipped: LoadShippedModules(moduleDir),
		Builtin: LoadModuleSet(filepath.Join(moduleDir, "modules.builtin")),
	}
	c.PCI, _ = LoadPCIIDs(pciIDsPath)
	return c, nil
}

// Discover walks sysfs and returns the machine's current unclaimed
// devices: the one call init makes, at boot and again whenever a
// uevent says the hardware changed.
func (c *Catalog) Discover(sysRoot string) []machine.UnclaimedDevice {
	return c.Unclaimed(DiscoverDevices(sysRoot, c.PCI))
}

// Unclaimed judges a walked device list. A device is reported when
// nothing drives it and at least one loadable module's alias
// patterns match its fingerprint; candidates already built into the
// kernel are excluded (they are resident, so loading is not the
// missing step), and a device with no loadable candidate at all is
// skipped as unactionable. The result is sorted so the report is
// stable across walks: a status object that reorders on every read
// churns watches for nothing.
func (c *Catalog) Unclaimed(devices []Device) []machine.UnclaimedDevice {
	var unclaimed []machine.UnclaimedDevice
	for _, d := range devices {
		if d.Driver != "" {
			continue
		}
		var candidates, aboard []string
		for _, module := range c.Aliases.Candidates(d.Modalias) {
			key := strings.ReplaceAll(module, "-", "_")
			if c.Builtin[key] {
				continue
			}
			candidates = append(candidates, module)
			if c.Shipped[key] {
				aboard = append(aboard, module)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		unclaimed = append(unclaimed, machine.UnclaimedDevice{
			Modalias:   d.Modalias,
			Bus:        d.Bus,
			Name:       d.Name,
			Class:      d.Class,
			Candidates: candidates,
			Message:    unclaimedMessage(candidates, aboard),
		})
	}
	slices.SortFunc(unclaimed, func(a, b machine.UnclaimedDevice) int {
		if a.Bus != b.Bus {
			return strings.Compare(a.Bus, b.Bus)
		}
		return strings.Compare(a.Modalias, b.Modalias)
	})
	return unclaimed
}

// unclaimedMessage phrases the fix. When the image carries a
// candidate, the fix is an edit and the message names only what is
// actually declarable; when it carries none, the fix is a different
// image, and the message still names the modules so a person can
// find the release (or build the image) that has them.
func unclaimedMessage(candidates, aboard []string) string {
	if len(aboard) > 0 {
		return "declare " + strings.Join(aboard, " or ") + " in spec.modules"
	}
	verb := "carries neither"
	if len(candidates) == 1 {
		verb = "doesn't carry it"
	} else if len(candidates) > 2 {
		verb = "carries none of them"
	}
	return strings.Join(candidates, " or ") +
		" would drive it, but this image " + verb + "; upgrade to a release that does"
}

// LoadShippedModules answers "which modules are actually aboard"
// by statting every file modules.dep names, not by trusting the
// index. The distinction matters on composed systems: a deployment
// layer that adds modules ships the kernel's *complete* index (so
// dependency resolution covers everything present), which makes
// index membership mean "exists in the kernel build", not "exists
// on this machine" — and telling someone to declare a module the
// machine doesn't carry would send them around the loop twice.
func LoadShippedModules(moduleDir string) map[string]bool {
	set := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(moduleDir, "modules.dep"))
	if err != nil {
		return set
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		path, _, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if _, err := os.Stat(filepath.Join(moduleDir, path)); err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(path), ".zst")
		name = strings.TrimSuffix(name, ".ko")
		set[strings.ReplaceAll(name, "-", "_")] = true
	}
	return set
}

// LoadModuleSet reads a depmod module list (modules.builtin, or any
// file of module paths one per line, ':'-terminated fields
// allowed) into a set of normalized names: the file's base with its
// extensions dropped and hyphens normalized to underscores, the
// same equivalence the kernel applies. A missing file reads as an
// empty set: judgment degrades (everything looks unshipped),
// reporting continues.
func LoadModuleSet(path string) map[string]bool {
	set := map[string]bool{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return set
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		name, _, _ := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		name = strings.TrimSuffix(filepath.Base(name), ".zst")
		name = strings.TrimSuffix(name, ".ko")
		set[strings.ReplaceAll(name, "-", "_")] = true
	}
	return set
}
