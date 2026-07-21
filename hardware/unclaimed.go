package hardware

// This file builds the unclaimed report. It judges which undriven
// devices are worth an operator's attention, and states what would
// fix each one.
//
// Not every driverless device is a problem. Sysfs contains many
// devices that legitimately have no driver, such as bridges, stubs,
// and firmware-described devices. A report that listed all of them
// would hide the one entry that matters, such as a plugged-in disk
// that nobody can use, among entries that nobody can act on. The
// filter is actionability. A device appears in the report exactly
// when some loadable module in the kernel build could drive it,
// because that is exactly when an edit to spec.modules, or a release
// that ships the module, would change something.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// Catalog holds everything needed to judge a device. It holds the
// kernel build's complete alias table, which lists the modules that
// could drive a device. It holds the shipped set, which lists which
// of those modules this image carries. It holds the builtin set,
// which lists the drivers that are resident in vmlinuz and need no
// loading. It optionally holds the PCI naming database. The catalog
// loads once at boot, and the judgments run again each time the
// hardware changes.
type Catalog struct {
	Aliases *AliasTable
	Shipped map[string]bool
	Builtin map[string]bool
	PCI     *PCIIDs
}

// LoadCatalog assembles the catalog from a module directory
// (/lib/modules/<release>) and a pci.ids path. Only the alias table
// is essential. Without it, no candidate can be named, and the
// report would produce nothing. For this reason, only a missing
// alias table is an error. When the naming database is missing, it
// falls back to numeric IDs.
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
// devices. init makes this one call at boot, and again each time a
// uevent reports that the hardware changed.
func (c *Catalog) Discover(sysRoot string) []machine.UnclaimedDevice {
	return c.Unclaimed(DiscoverDevices(sysRoot, c.PCI))
}

// Unclaimed judges a walked device list. Unclaimed reports a device
// when nothing drives it, and at least one loadable module's alias
// patterns match its fingerprint. Unclaimed excludes candidates that
// are already built into the kernel, because they are resident and
// loading is not the missing step. Unclaimed skips a device with no
// loadable candidate at all, because no action can fix it. Unclaimed
// sorts the result so that the report stays stable across walks. A
// status object that reorders on every read causes watches to fire
// for no reason.
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

// unclaimedMessage states the fix in words. When the image carries
// a candidate module, the fix is an edit, and the message names only
// the modules that a person can actually declare. On a stock image
// this is every candidate, because the image carries the kernel's
// whole module tree. When the image carries none (a composed image
// can remove modules), the fix is a different image, and the message
// still names the modules, so a person can find an image that
// carries them.
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
		" would drive it, but this image " + verb + "; use an image that does"
}

// LoadShippedModules finds which modules are actually present on
// the machine. It does this by checking, with stat, every file that
// modules.dep names, instead of trusting the index. This distinction
// matters on composed systems. A deployment layer that adds modules
// ships the kernel's complete index, so that dependency resolution
// covers everything present. This makes index membership mean
// "exists in the kernel build", not "exists on this machine".
// Telling a person to declare a module that the machine does not
// carry would send them through the same failed fix twice.
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

// LoadModuleSet reads a depmod module list into a set of normalized
// names. The list can be modules.builtin, or any file of module
// paths, one per line, with optional ':'-terminated fields. A
// normalized name is the file's base name with its extensions
// dropped, and its hyphens changed to underscores. This is the same
// equivalence that the kernel applies. A missing file reads as an
// empty set. Judgment then degrades, so that every module looks
// unshipped, but reporting still continues.
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
