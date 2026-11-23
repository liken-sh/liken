package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// buildroot builds each package that a selected recipe depends on,
// and its SBOM lists every package it built. Its SBOM gives the scope
// excluded only to a host package, so on its own it states that a
// release ships each library that the build compiled. nfs-utils.mk
// depends on libevent, for example, and only the NFS daemons link
// it, which a release does not ship. A scanner that reads the SBOM
// then reports libevent's advisories against the release.
//
// The evidence that liken/userland/build.sh writes into
// dist/evidence/ shows which packages the release contains:
//
//   - shipped has a line for each file of dist/ that a release
//     ships: its kind, program or data, its path in dist/, and the
//     path in buildroot's output that build.sh copied it from;
//   - maps/<program>.map is the part of a program's link map that
//     names each archive member and each object file that the link
//     took;
//   - packages-file-list.txt, packages-file-list-staging.txt, and
//     packages-file-list-host.txt are buildroot's lists of the package
//     that installed each file of the target, staging, and host trees.
//
// A path in buildroot's output has a slash in front and starts at the
// root of the output, as in /staging/usr/lib/libc.a.

// archiveSection is the heading of the section of a link map that
// names each archive member that the link took.
const archiveSection = "Archive member included to satisfy reference by file (symbol)"

// fileLists maps each tree of buildroot's output to buildroot's list
// of the package that installed each of its files.
var fileLists = map[string]string{
	"target":  "packages-file-list.txt",
	"staging": "packages-file-list-staging.txt",
	"host":    "packages-file-list-host.txt",
}

// owners finds the package that a file of buildroot's output came
// from.
type owners struct {
	// installed maps a path to each package that installed a file
	// there.
	installed map[string][]string
	// builds maps the name of a build directory, such as
	// nfs-utils-3.1.1, to its package.
	builds map[string]string
	// deps is buildroot's dependency graph, by package.
	deps map[string][]string
}

// containedPackages reads the evidence in dir and returns each package
// of buildroot's SBOM that a file of the release contains: the package
// that installed each shipped file, and the package that owns each
// archive member and each object file that a shipped program's link
// took. It refuses evidence that it cannot explain, so a package that
// the release contains never passes as one that it does not.
func containedPackages(dir string, in input) (map[string]bool, error) {
	o := owners{installed: map[string][]string{}, builds: map[string]string{}, deps: map[string][]string{}}
	for tree, name := range fileLists {
		if err := o.readFileList(filepath.Join(dir, name), tree); err != nil {
			return nil, err
		}
	}
	known := map[string]bool{}
	for _, c := range in.Components {
		ref, _ := c["bom-ref"].(string)
		version, _ := c["version"].(string)
		known[ref] = true
		o.builds[ref] = ref
		o.builds[ref+"-"+version] = ref
	}
	for _, d := range in.Dependencies {
		o.deps[d.Ref] = d.DependsOn
	}
	shipped, err := readShipped(filepath.Join(dir, "shipped"))
	if err != nil {
		return nil, err
	}
	contained := map[string]bool{}
	add := func(pkg string) error {
		if !known[pkg] {
			return fmt.Errorf("buildroot's SBOM has no package %s", pkg)
		}
		contained[pkg] = true
		return nil
	}
	for _, f := range shipped {
		pkg, err := o.of(f.source, "")
		if err == nil {
			err = add(pkg)
		}
		if err != nil {
			return nil, fmt.Errorf("finding the package of %s, copied from %s: %w", f.path, f.source, err)
		}
		if f.kind == "data" {
			continue
		}
		mapPath := filepath.Join(dir, "maps", path.Base(f.path)+".map")
		files, err := linkedFiles(mapPath)
		if err != nil {
			return nil, fmt.Errorf("reading the link map of %s: %w", f.path, err)
		}
		for _, file := range files {
			owner, err := o.of(file, pkg)
			if err == nil {
				err = add(owner)
			}
			if err != nil {
				return nil, fmt.Errorf("%s: finding the package of %s: %w", mapPath, file, err)
			}
		}
	}
	return contained, nil
}

// A shippedFile is one line of the shipped record.
type shippedFile struct{ kind, path, source string }

func readShipped(name string) ([]shippedFile, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var files []shippedFile
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "program" && fields[0] != "data" || !strings.HasPrefix(fields[2], "/") {
			return nil, fmt.Errorf("%s:%d: %q does not have the form <program|data> <path in dist/> <path in buildroot's output, with a slash in front>", name, i+1, line)
		}
		files = append(files, shippedFile{kind: fields[0], path: fields[1], source: fields[2]})
	}
	return files, nil
}

// readFileList reads one of buildroot's lists. Each line names a
// package and a file that it installed, as in
// "musl,./lib/libc.a". A file that several packages installed has a
// line for each of them.
func (o *owners) readFileList(name, tree string) error {
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		pkg, file, ok := strings.Cut(line, ",./")
		if !ok || pkg == "" || file == "" {
			return fmt.Errorf("%s:%d: %q does not have the form <package>,./<path>", name, i+1, line)
		}
		key := "/" + tree + "/" + file
		if !slices.Contains(o.installed[key], pkg) {
			o.installed[key] = append(o.installed[key], pkg)
		}
	}
	return nil
}

// of returns the package that a file came from. A relative path is
// one that the linker took relative to the directory that it ran in,
// and buildroot runs each package's build inside the package's build
// directory, so the file belongs to linker, the package whose link
// wrote the map. A file under the build directory of a package belongs
// to that package. Any other file belongs to the package that
// buildroot's lists say installed it.
func (o *owners) of(file, linker string) (string, error) {
	if !strings.HasPrefix(file, "/") {
		if linker == "" {
			return "", fmt.Errorf("%q is not a path in buildroot's output", file)
		}
		return linker, nil
	}
	if rest, ok := strings.CutPrefix(file, "/build/"); ok {
		dir, _, _ := strings.Cut(rest, "/")
		if pkg, ok := o.builds[dir]; ok {
			return pkg, nil
		}
		return "", fmt.Errorf("no package of buildroot's SBOM has the build directory %s", dir)
	}
	return o.lastInstaller(file)
}

// lastInstaller returns the package whose copy of a file is the one in
// the tree. When several packages installed the same path, such as
// host-gcc-initial and host-gcc-final with libgcc.a, buildroot's lists
// name each of them, in the order of their build directories' names.
// buildroot installs a package after each package that it depends on,
// so the copy in the tree is from the package that depends, directly
// or through others, on each of the others.
func (o *owners) lastInstaller(file string) (string, error) {
	pkgs := o.installed[file]
	if len(pkgs) == 0 {
		return "", fmt.Errorf("no package of buildroot's lists installed it")
	}
	for _, pkg := range pkgs {
		if !slices.ContainsFunc(pkgs, func(other string) bool { return other != pkg && !o.dependsOn(pkg, other) }) {
			return pkg, nil
		}
	}
	return "", fmt.Errorf("the packages %s each installed it, and no one of them depends on all of the others, so the evidence does not show whose copy is in the tree", strings.Join(pkgs, ", "))
}

// dependsOn is true when buildroot's graph has a path from one package
// to the other.
func (o *owners) dependsOn(pkg, other string) bool {
	seen := map[string]bool{}
	next := []string{pkg}
	for len(next) > 0 {
		p := next[len(next)-1]
		next = next[:len(next)-1]
		for _, d := range o.deps[p] {
			if d == other {
				return true
			}
			if !seen[d] {
				seen[d] = true
				next = append(next, d)
			}
		}
	}
	return false
}

// linkedFiles reads the evidence of one link map and returns each file
// that the link took code from: the archive of each member that the
// section "Archive member included to satisfy reference by file
// (symbol)" names, and each file of a LOAD line that is not an
// archive. The section names a member as archive(member), or by the
// member's own path when the archive is a thin archive, the kind that
// meson writes, so the file is the archive or the member itself. ld
// writes a LOAD line for each archive on the command line, whether or
// not the link took a member of it, so an archive counts only when
// that section names one of its members. "linker stubs" is ld's own
// input, which no package provides.
//
// Every static program takes members of the C library's archive, so a
// map with no member is a map that lost its section, as it would when
// an ld release gives the section another heading. linkedFiles refuses
// such a map, because the merge would otherwise read it as a program
// that links no library, and exclude each library that the program
// contains.
func linkedFiles(name string) ([]string, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var files []string
	section, members := false, 0
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "LOAD "):
			file := strings.TrimPrefix(line, "LOAD ")
			if file != "linker stubs" && !strings.HasSuffix(file, ".a") {
				files = append(files, file)
			}
		case line == archiveSection:
			section = true
		case line == "" || line[0] == ' ' || line[0] == '\t':
		default:
			member := strings.Fields(line)[0]
			archive, _, _ := strings.Cut(member, "(")
			files = append(files, archive)
			members++
		}
	}
	if !section {
		return nil, fmt.Errorf("the map has no section %q, which ld writes for a link that takes a member of an archive", archiveSection)
	}
	if members == 0 {
		return nil, fmt.Errorf("the map's section %q names no member, and every static program takes members of the C library", archiveSection)
	}
	return files, nil
}
