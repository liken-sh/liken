package plugins

// This file reports what is installed against what the cluster runs.
// `kubectl liken plugins list` reads each installed CLI's stamped
// version and the operator version the cluster holds for that domain,
// and marks the ones that differ. It also names the operators that
// run with no local CLI, the cold-start case kubectl reports only as
// a raw "not found".

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Entry pairs one domain's installed CLI version with the operator
// version the cluster holds. Either version is empty when its side
// is absent.
type Entry struct {
	Domain          string
	CLIVersion      string
	OperatorVersion string
}

// Drift reports whether an installed CLI faces an operator of a
// different version.
func (e Entry) Drift() bool {
	return e.CLIVersion != "" && e.OperatorVersion != "" && e.CLIVersion != e.OperatorVersion
}

// MissingCLI reports whether an operator runs with no local CLI. This
// is the cold-start case that `plugins sync` fixes.
func (e Entry) MissingCLI() bool {
	return e.CLIVersion == "" && e.OperatorVersion != ""
}

// Installed reads the plugin directory and reports the domains whose
// CLIs are installed. A directory that does not exist yet reads as no
// domains, the state before the first sync.
func Installed(binDir string) ([]string, error) {
	entries, err := os.ReadDir(binDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := Name("")
	var domains []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if domain, ok := strings.CutPrefix(entry.Name(), prefix); ok && domain != "" {
			domains = append(domains, domain)
		}
	}
	sort.Strings(domains)
	return domains, nil
}

// Merge builds the sorted entry list from the installed CLI versions
// and the operator versions, over the union of their domains.
func Merge(installed, operators map[string]string) []Entry {
	seen := map[string]bool{}
	var domains []string
	for domain := range installed {
		if !seen[domain] {
			seen[domain] = true
			domains = append(domains, domain)
		}
	}
	for domain := range operators {
		if !seen[domain] {
			seen[domain] = true
			domains = append(domains, domain)
		}
	}
	sort.Strings(domains)
	entries := make([]Entry, 0, len(domains))
	for _, domain := range domains {
		entries = append(entries, Entry{
			Domain:          domain,
			CLIVersion:      installed[domain],
			OperatorVersion: operators[domain],
		})
	}
	return entries
}

// Render writes the entry list, one domain per line with its two
// versions and a drift mark, and then names any operators that have
// no local CLI so the person can sync them.
func Render(entries []Entry, out io.Writer) {
	var missing []string
	for _, e := range entries {
		if e.MissingCLI() {
			missing = append(missing, e.Domain)
		}
		cli := e.CLIVersion
		if cli == "" {
			cli = "-"
		}
		operator := e.OperatorVersion
		if operator == "" {
			operator = "-"
		}
		mark := ""
		if e.Drift() {
			mark = "\tdrift"
		}
		fmt.Fprintf(out, "%s\t%s\t%s%s\n", e.Domain, cli, operator, mark)
	}
	if len(missing) > 0 {
		fmt.Fprintf(out, "no local CLI for: %s; run: liken plugins sync\n", strings.Join(missing, ", "))
	}
}
