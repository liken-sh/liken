package machine

// HostsFile renders /etc/hosts. It lives here, rather than in init or
// the operator, because both programs write this file and must never
// disagree about its shape (drift.go states the same rule for the
// rest of this package). Init calls it at every boot, and the machine
// operator calls it on every reconcile pass to check the file for
// drift and to heal it. One function, called from two places, is what
// keeps the file to one shape no matter which of the two wrote it
// last.

import (
	"fmt"
	"strings"
)

// HostsFile renders the three fixed lines that resolve this
// machine's own identity, localhost, its IPv6 form, and the
// machine's own name at 127.0.1.1, followed by one line for each host
// entry, in the order the caller passes them. Entry order has no
// effect on how a name resolves, since each address answers on its
// own line, but keeping the caller's order is what lets a person
// compare the file against the manifest that produced it.
//
// The fixed lines always come first. A resolver takes its first
// match, so an entry can add a name but can never override localhost
// or the machine's own name.
func HostsFile(hostname string, entries []HostEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "127.0.0.1 localhost\n::1 localhost\n127.0.1.1 %s\n", hostname)
	for _, entry := range entries {
		fmt.Fprintf(&b, "%s %s\n", entry.Address, strings.Join(entry.Names, " "))
	}
	return b.String()
}
