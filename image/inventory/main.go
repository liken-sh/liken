// inventory answers the image build's questions about a deployment's
// manifests. Its one question so far: which extra kernel modules the
// deployment's machines declare (spec.modules), so build.sh can ship
// exactly those beside the OS's own fixed list.
//
// This is a Go program rather than shell parsing for one reason: it
// reads the manifests with the same strict parser init uses at boot
// (machine.Load), so the build and the booted machine can never
// disagree about what a manifest says, and a misspelled field fails
// the build with the same error a boot would print. It lives in the
// image domain because reading the deployment's manifests to decide
// what the image carries is image assembly.
//
// The output is line-oriented, one module name per line, sorted and
// deduplicated, so build.sh consumes it exactly the way it consumes
// modules.conf. A deployment with no manifests, or manifests declaring
// no modules, produces no output and succeeds: the image just carries
// nothing extra, which is a valid machine.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/chrisguidry/liken/machine"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "inventory:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: inventory modules <manifests-dir>")
	}
	question, dir := args[0], args[1]
	if question != "modules" {
		return fmt.Errorf("unknown question %q (only modules for now)", question)
	}
	modules, err := declaredModules(dir)
	if err != nil {
		return err
	}
	for _, name := range modules {
		fmt.Fprintln(out, name)
	}
	return nil
}

// declaredModules is the union of spec.modules across every Machine
// manifest in the deployment: one image boots the whole fleet, so the
// image must carry what any of its machines might load. A missing
// machines directory is a deployment that declared no machines, which
// is fine; a manifest that exists but does not parse is a
// configuration error the build must not paper over.
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
