package image

// This file builds the deployment layer: everything that makes a
// generic liken image one particular deployment's image.
//
// The generic archive that build.sh produces carries the operating
// system and nothing else: no cluster identity, no manifests. This
// file packs the rest, the deployment's half, as a second cpio
// archive. Concatenation joins the two archives: the kernel's
// initramfs unpacker processes concatenated archives in order into
// one filesystem, with later entries overriding earlier ones (the
// same mechanism the install image uses to carry its payload). This
// split is what makes liken releasable: the generic archive's digest
// never changes with the deployment, and producing a bootable image
// from a release is composition, not compilation.
//
// The layer holds these contents, at the paths init and k3s read:
//
//	etc/liken/cluster.yaml       the deployment's cluster document
//	etc/liken/machines/*.yaml    one manifest per machine
//	etc/liken/token              the join token (0600; init hands k3s
//	                             the path, never the value)
//	var/lib/rancher/k3s/         the certificate authorities, exactly
//	  server/tls/**              where k3s looks before generating
//	                             its own
//	lib/modules/<release>/**     the machines' declared kernel
//	                             modules, when any are declared
//
// The identity's kubeconfig, and the admin keypair inside it, stay
// behind on purpose. The kubeconfig is the operator's credential, not
// the machine's. A machine image carrying it would hand cluster-admin
// access to anyone who reads the disk.
//
// Modules need one extra piece. Init resolves module names and their
// dependencies through depmod's index (modules.dep), and the generic
// archive ships an index that covers exactly its own modules. So a
// layer that adds module files also ships the kernel's complete
// index. This index overrides the generic one at unpack, so the
// composed system can resolve everything actually present. The index
// is a few hundred kilobytes of text. The alternative, running
// depmod over a composed tree, would pull the whole build system back
// into work that is supposed to be composition, not a build step.

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/identity"
	"github.com/liken-sh/liken/machine"
)

// Layer packs a deployment's archive from its manifests and identity.
// It pulls declared kernel modules, and their full set of
// dependencies, from the vendored kernel dist.
func Layer(manifests, identityDir, kdist, out string, log io.Writer) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	a := newArchive(f)
	dirs := map[string]bool{}

	// ensure writes the missing parents of a path, root first. This is
	// the order the kernel's unpacker needs to create them.
	ensure := func(path string) error {
		var parents []string
		for d := filepath.Dir(path); d != "."; d = filepath.Dir(d) {
			parents = append(parents, d)
		}
		slices.Reverse(parents)
		for _, d := range parents {
			if !dirs[d] {
				if err := a.dir(d, 0o755); err != nil {
					return err
				}
				dirs[d] = true
			}
		}
		return nil
	}
	ship := func(src, dst string, perm int) error {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := ensure(dst); err != nil {
			return err
		}
		return a.file(dst, data, perm)
	}

	// This stages the manifests at the paths init reads. It copies
	// them file by file rather than as a tree, so what the layer can
	// carry stays explicit: one cluster document, one manifest per
	// machine.
	if cluster := filepath.Join(manifests, "cluster.yaml"); fileExists(cluster) {
		if err := ship(cluster, "etc/liken/cluster.yaml", 0o644); err != nil {
			return err
		}
	}
	machines, err := filepath.Glob(filepath.Join(manifests, "machines", "*.yaml"))
	if err != nil {
		return err
	}
	for _, m := range machines {
		if err := ship(m, filepath.Join("etc/liken/machines", filepath.Base(m)), 0o644); err != nil {
			return err
		}
	}

	// This stages the identity: the CA tree where k3s looks for it,
	// and the token under /etc/liken, where no disk mount can hide it
	// underneath. The bundle list belongs to the identity package
	// itself, so the layer and the mint can never disagree about what
	// an identity is.
	for _, p := range identity.Bundle {
		src := filepath.Join(identityDir, p)
		dst := filepath.Join("var/lib/rancher/k3s/server", p)
		perm := 0o600
		if strings.HasSuffix(p, ".crt") {
			perm = 0o644
		}
		if p == "token" {
			dst = "etc/liken/token"
		}
		if err := ship(src, dst, perm); err != nil {
			return err
		}
	}

	if err := shipModules(a, ensure, manifests, kdist, log); err != nil {
		return err
	}

	return a.close()
}

// shipModules resolves the machines' declared modules against the
// kernel's depmod index and ships their files, dependencies included.
// A name the kernel does not have fails right here. This is the
// point: a deployment learns about a misspelled module when it
// builds the layer, not on a booted fleet. A name that is built into
// the kernel needs no file and ships nothing.
func shipModules(a *archive, ensure func(string) error, manifests, kdist string, log io.Writer) error {
	declared, err := declaredModules(manifests)
	if err != nil {
		return err
	}
	if len(declared) == 0 {
		return nil
	}

	releaseRaw, err := os.ReadFile(filepath.Join(kdist, "release"))
	if err != nil {
		return err
	}
	release := strings.TrimSpace(string(releaseRaw))
	base := filepath.Join(kdist, "lib", "modules", release)

	deps, err := readModulesDep(filepath.Join(base, "modules.dep"))
	if err != nil {
		return err
	}
	builtin, err := readModulesBuiltin(filepath.Join(base, "modules.builtin"))
	if err != nil {
		return err
	}

	fmt.Fprintln(log, "modules declared by this deployment's machines:")
	files := map[string]bool{}
	for _, name := range declared {
		fmt.Fprintf(log, "  %s\n", name)
		if builtin[moduleName(name)] {
			continue
		}
		path, ok := deps.path[moduleName(name)]
		if !ok {
			return fmt.Errorf("module %s is not in the kernel's modules.dep (is the name right?)", name)
		}
		files[path] = true
		for _, dep := range deps.needs[path] {
			files[dep] = true
		}
	}
	if len(files) == 0 {
		return nil
	}

	for _, path := range slices.Sorted(maps.Keys(files)) {
		dst := filepath.Join("lib/modules", release, path)
		if err := ensure(dst); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(base, path))
		if err != nil {
			return err
		}
		if err := a.file(dst, data, 0o644); err != nil {
			return err
		}
	}

	// Shipping any module means shipping the complete index too (the
	// override described in the header comment), plus the companion
	// files that init and depmod expect to find beside it.
	for _, f := range []string{"modules.dep", "modules.builtin", "modules.builtin.modinfo", "modules.order"} {
		data, err := os.ReadFile(filepath.Join(base, f))
		if err != nil {
			return err
		}
		if err := a.file(filepath.Join("lib/modules", release, f), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// declaredModules is the union of spec.modules across every Machine
// manifest in the deployment. One image boots the whole fleet, so the
// layer must carry what any of its machines might load. A missing
// machines directory means a deployment that declared no machines,
// which is fine. A manifest that exists but does not parse is a
// configuration error, and the build must not hide it.
func declaredModules(manifestsDir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(manifestsDir, "machines", "*.yaml"))
	if err != nil {
		return nil, err
	}
	union := map[string]bool{}
	for _, path := range paths {
		m, err := machine.Load(path)
		if err != nil {
			return nil, err
		}
		for _, name := range m.Spec.Modules {
			union[name] = true
		}
	}
	modules := make([]string, 0, len(union))
	for name := range union {
		modules = append(modules, name)
	}
	slices.Sort(modules)
	return modules, nil
}

// modulesDep is depmod's index, read the same way init reads it at
// boot (init/modules.go). Each line maps one module's path to the
// paths it needs, already ordered for loading. The keys are
// normalized module names, because declarations say "iptable_nat"
// while paths say "iptable-nat.ko.zst", and depmod treats dash and
// underscore as the same letter.
type modulesDep struct {
	path  map[string]string   // normalized name -> module path
	needs map[string][]string // module path -> dependency paths
}

func readModulesDep(path string) (*modulesDep, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	deps := &modulesDep{path: map[string]string{}, needs: map[string][]string{}}
	for line := range strings.SplitSeq(string(raw), "\n") {
		mod, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		deps.path[moduleName(mod)] = mod
		deps.needs[mod] = strings.Fields(rest)
	}
	return deps, nil
}

// readModulesBuiltin reads the list of module names compiled into the
// kernel itself. A declared name found in this list never needs a
// file.
func readModulesBuiltin(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	builtin := map[string]bool{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		builtin[moduleName(line)] = true
	}
	return builtin, nil
}

// moduleName normalizes a module path or name the way depmod and init
// do: it strips the file suffixes and folds dash to underscore.
func moduleName(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, ".zst")
	name = strings.TrimSuffix(name, ".ko")
	return strings.ReplaceAll(name, "-", "_")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
